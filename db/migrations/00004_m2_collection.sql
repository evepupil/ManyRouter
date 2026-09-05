-- +goose Up
CREATE TABLE channel_binding_history (
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    external_channel_id bigint NOT NULL,
    relation_id uuid NOT NULL REFERENCES site_suppliers(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    managed_tag varchar(96) NOT NULL,
    valid_from timestamptz NOT NULL,
    valid_to timestamptz,
    CHECK (valid_to IS NULL OR valid_to > valid_from),
    PRIMARY KEY (site_id, external_channel_id, valid_from)
);

CREATE UNIQUE INDEX channel_binding_history_current_idx
    ON channel_binding_history(site_id, external_channel_id)
    WHERE valid_to IS NULL;
CREATE INDEX channel_binding_history_relation_idx
    ON channel_binding_history(relation_id, valid_from DESC);

INSERT INTO channel_binding_history (
    site_id, external_channel_id, relation_id, supplier_id, managed_tag, valid_from
)
SELECT relation.site_id, channel.external_channel_id, relation.id, relation.supplier_id,
       channel.managed_tag, channel.updated_at
FROM site_supplier_channels channel
JOIN site_suppliers relation ON relation.id = channel.site_supplier_id
WHERE channel.external_channel_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_channel_binding_history()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    relation_site_id uuid;
    relation_supplier_id uuid;
    changed_at timestamptz;
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.external_channel_id IS NULL THEN
            RETURN OLD;
        END IF;
        SELECT site_id, supplier_id INTO relation_site_id, relation_supplier_id
        FROM site_suppliers WHERE id = OLD.site_supplier_id;
        changed_at := GREATEST(NOW(), OLD.updated_at + INTERVAL '1 microsecond');
        UPDATE channel_binding_history
        SET valid_to = changed_at
        WHERE site_id = relation_site_id
          AND external_channel_id = OLD.external_channel_id
          AND relation_id = OLD.site_supplier_id
          AND valid_to IS NULL;
        RETURN OLD;
    END IF;

    IF TG_OP = 'UPDATE'
       AND OLD.external_channel_id IS NOT DISTINCT FROM NEW.external_channel_id
       AND OLD.site_supplier_id = NEW.site_supplier_id THEN
        RETURN NEW;
    END IF;

    SELECT site_id, supplier_id INTO relation_site_id, relation_supplier_id
    FROM site_suppliers WHERE id = NEW.site_supplier_id;
    changed_at := NEW.updated_at;

    IF TG_OP = 'UPDATE' AND OLD.external_channel_id IS NOT NULL THEN
        UPDATE channel_binding_history
        SET valid_to = GREATEST(changed_at, valid_from + INTERVAL '1 microsecond')
        WHERE relation_id = OLD.site_supplier_id
          AND external_channel_id = OLD.external_channel_id
          AND valid_to IS NULL;
    END IF;

    IF NEW.external_channel_id IS NOT NULL THEN
        UPDATE channel_binding_history
        SET valid_to = GREATEST(changed_at, valid_from + INTERVAL '1 microsecond')
        WHERE site_id = relation_site_id
          AND external_channel_id = NEW.external_channel_id
          AND valid_to IS NULL;

        INSERT INTO channel_binding_history (
            site_id, external_channel_id, relation_id, supplier_id, managed_tag, valid_from
        ) VALUES (
            relation_site_id, NEW.external_channel_id, NEW.site_supplier_id,
            relation_supplier_id, NEW.managed_tag, changed_at
        );
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER site_supplier_channels_binding_history
AFTER INSERT OR UPDATE OR DELETE ON site_supplier_channels
FOR EACH ROW EXECUTE FUNCTION record_channel_binding_history();

CREATE TABLE collection_cursors (
    site_id uuid PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    source_kind text NOT NULL DEFAULT 'new_api_http'
        CHECK (source_kind IN ('new_api_http', 'new_api_view')),
    contract_version text NOT NULL DEFAULT 'new_api_http_v1',
    committed_created_at bigint NOT NULL DEFAULT 0 CHECK (committed_created_at >= 0),
    committed_source_id varchar(256) NOT NULL DEFAULT '',
    scanned_through_at bigint NOT NULL DEFAULT 0 CHECK (scanned_through_at >= 0),
    overlap_seconds integer NOT NULL DEFAULT 600 CHECK (overlap_seconds BETWEEN 60 AND 86400),
    source_latest_created_at bigint CHECK (source_latest_created_at IS NULL OR source_latest_created_at >= 0),
    last_read_at timestamptz,
    last_success_at timestamptz,
    last_error_at timestamptz,
    last_error_code text,
    last_error_message text,
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    data_gap boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

CREATE TABLE measurement_requests (
    id uuid PRIMARY KEY,
    site_id uuid REFERENCES sites(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('real_traffic', 'direct_probe', 'site_probe')),
    request_hash char(64) NOT NULL CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    revision integer NOT NULL CHECK (revision > 0),
    is_current boolean NOT NULL DEFAULT TRUE,
    superseded_at timestamptz,
    source_contract text NOT NULL,
    source_event_key char(64) NOT NULL CHECK (source_event_key ~ '^[0-9a-f]{64}$'),
    source_event_id bigint,
    source_created_at bigint,
    terminal_created_at bigint NOT NULL CHECK (terminal_created_at >= 0),
    terminal_source_id varchar(256) NOT NULL CHECK (terminal_source_id <> ''),
    request_id varchar(128),
    observed_at timestamptz NOT NULL,
    model varchar(191) NOT NULL,
    request_group varchar(64) NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled', 'rejected', 'incomplete')),
    final_relation_id uuid REFERENCES site_suppliers(id),
    final_supplier_id uuid REFERENCES suppliers(id),
    final_external_channel_id bigint,
    attribution_status text NOT NULL CHECK (attribution_status IN ('mapped', 'pending', 'not_applicable')),
    is_stream boolean NOT NULL,
    stream_completed boolean,
    ttft_ms bigint CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    duration_resolution_ms integer,
    input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens >= 0),
    upstream_status_code integer CHECK (upstream_status_code IS NULL OR upstream_status_code BETWEEN 100 AND 599),
    error_category text,
    error_responsibility text CHECK (error_responsibility IN ('user', 'supplier', 'site', 'unknown')),
    error_code text,
    classification_version text NOT NULL,
    completeness text NOT NULL CHECK (completeness IN ('complete', 'partial', 'unusable')),
    missing_reason text,
    recorded_at timestamptz NOT NULL,
    UNIQUE NULLS NOT DISTINCT (site_id, source, source_event_key),
    UNIQUE NULLS NOT DISTINCT (site_id, source, request_hash, revision),
    CHECK ((is_current AND superseded_at IS NULL) OR (NOT is_current AND superseded_at IS NOT NULL)),
    CHECK (
        (outcome = 'succeeded' AND error_responsibility IS NULL)
        OR (outcome <> 'succeeded' AND error_responsibility IS NOT NULL)
    ),
    CHECK (
        (duration_ms IS NULL AND duration_resolution_ms IS NULL)
        OR (duration_ms IS NOT NULL AND duration_resolution_ms IN (1, 1000))
    ),
    CHECK (
        (
            attribution_status = 'mapped'
            AND final_supplier_id IS NOT NULL
            AND (source = 'direct_probe' OR final_relation_id IS NOT NULL)
        )
        OR (attribution_status <> 'mapped')
    )
);

CREATE UNIQUE INDEX measurement_requests_current_idx
    ON measurement_requests(site_id, source, request_hash) NULLS NOT DISTINCT
    WHERE is_current;
CREATE INDEX measurement_requests_scope_idx
    ON measurement_requests(site_id, final_supplier_id, model, observed_at DESC)
    WHERE is_current;
CREATE INDEX measurement_requests_request_idx
    ON measurement_requests(site_id, request_id, observed_at DESC)
    WHERE request_id IS NOT NULL AND is_current;
CREATE INDEX measurement_requests_unmapped_idx
    ON measurement_requests(site_id, observed_at DESC)
    WHERE attribution_status = 'pending' AND is_current;
CREATE INDEX measurement_requests_observed_idx
    ON measurement_requests(observed_at)
    WHERE is_current;

CREATE TABLE measurement_quarantines (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source IN ('real_traffic', 'site_probe')),
    source_event_key char(64) NOT NULL CHECK (source_event_key ~ '^[0-9a-f]{64}$'),
    source_created_at bigint NOT NULL CHECK (source_created_at > 0),
    source_id varchar(256) NOT NULL CHECK (source_id <> ''),
    reason_code varchar(80) NOT NULL CHECK (reason_code ~ '^[a-z0-9][a-z0-9_.:-]{0,79}$'),
    recorded_at timestamptz NOT NULL,
    resolved_at timestamptz,
    UNIQUE (site_id, source, source_event_key)
);

CREATE INDEX measurement_quarantines_site_time_idx
    ON measurement_quarantines(site_id, source_created_at DESC);
CREATE INDEX measurement_quarantines_unresolved_idx
    ON measurement_quarantines(site_id, source_created_at DESC)
    WHERE resolved_at IS NULL;

CREATE TABLE measurement_attempts (
    id uuid PRIMARY KEY,
    request_measurement_id uuid NOT NULL REFERENCES measurement_requests(id) ON DELETE CASCADE,
    attempt_index integer NOT NULL CHECK (attempt_index >= 1),
    relation_id uuid REFERENCES site_suppliers(id),
    supplier_id uuid REFERENCES suppliers(id),
    external_channel_id bigint,
    attribution_status text NOT NULL CHECK (attribution_status IN ('mapped', 'pending', 'not_applicable')),
    model varchar(191) NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'cancelled', 'rejected', 'incomplete', 'unknown')),
    is_final boolean NOT NULL,
    is_stream boolean NOT NULL,
    stream_completed boolean,
    produced_visible_output boolean,
    ttft_ms bigint CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    duration_resolution_ms integer,
    upstream_status_code integer CHECK (upstream_status_code IS NULL OR upstream_status_code BETWEEN 100 AND 599),
    error_category text,
    error_responsibility text CHECK (error_responsibility IN ('user', 'supplier', 'site', 'unknown')),
    error_code text,
    classification_version text NOT NULL,
    observed_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL,
    UNIQUE (request_measurement_id, attempt_index),
    CHECK (
        (outcome = 'succeeded' AND error_responsibility IS NULL)
        OR (outcome <> 'succeeded' AND error_responsibility IS NOT NULL)
    ),
    CHECK (
        (duration_ms IS NULL AND duration_resolution_ms IS NULL)
        OR (duration_ms IS NOT NULL AND duration_resolution_ms IN (1, 1000))
    ),
    CHECK (
        (attribution_status = 'mapped' AND supplier_id IS NOT NULL)
        OR (attribution_status <> 'mapped')
    )
);

CREATE INDEX measurement_attempts_scope_idx
    ON measurement_attempts(supplier_id, model, observed_at DESC);
CREATE INDEX measurement_attempts_request_idx
    ON measurement_attempts(request_measurement_id, attempt_index);
CREATE INDEX measurement_attempts_observed_idx
    ON measurement_attempts(observed_at);

-- +goose Down
DROP INDEX IF EXISTS measurement_attempts_observed_idx;
DROP INDEX IF EXISTS measurement_attempts_request_idx;
DROP INDEX IF EXISTS measurement_attempts_scope_idx;
DROP TABLE IF EXISTS measurement_attempts;
DROP INDEX IF EXISTS measurement_quarantines_unresolved_idx;
DROP INDEX IF EXISTS measurement_quarantines_site_time_idx;
DROP TABLE IF EXISTS measurement_quarantines;
DROP INDEX IF EXISTS measurement_requests_observed_idx;
DROP INDEX IF EXISTS measurement_requests_unmapped_idx;
DROP INDEX IF EXISTS measurement_requests_request_idx;
DROP INDEX IF EXISTS measurement_requests_scope_idx;
DROP INDEX IF EXISTS measurement_requests_current_idx;
DROP TABLE IF EXISTS measurement_requests;
DROP TABLE IF EXISTS collection_cursors;
DROP TRIGGER IF EXISTS site_supplier_channels_binding_history ON site_supplier_channels;
DROP FUNCTION IF EXISTS record_channel_binding_history();
DROP INDEX IF EXISTS channel_binding_history_relation_idx;
DROP INDEX IF EXISTS channel_binding_history_current_idx;
DROP TABLE IF EXISTS channel_binding_history;

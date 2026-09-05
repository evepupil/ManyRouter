-- +goose Up
CREATE TABLE supplier_model_price_history (
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    input_price numeric(20, 10) NOT NULL CHECK (input_price >= 0),
    output_price numeric(20, 10) NOT NULL CHECK (output_price >= 0),
    currency varchar(12) NOT NULL,
    valid_from timestamptz NOT NULL,
    valid_to timestamptz,
    PRIMARY KEY (supplier_id, model, version),
    CHECK (valid_to IS NULL OR valid_to > valid_from)
);

CREATE UNIQUE INDEX supplier_model_price_history_current_idx
    ON supplier_model_price_history(supplier_id, model)
    WHERE valid_to IS NULL;

INSERT INTO supplier_model_price_history (
    supplier_id, model, version, input_price, output_price, currency, valid_from
)
SELECT supplier_id, model, 1, input_price, output_price, currency, created_at
FROM supplier_models;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_supplier_model_price_history()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    changed_at timestamptz;
    next_version bigint;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT GREATEST(
            NEW.created_at,
            COALESCE(MAX(valid_from) + INTERVAL '1 microsecond', NEW.created_at)
        ), COALESCE(MAX(version), 0) + 1
        INTO changed_at, next_version
        FROM supplier_model_price_history
        WHERE supplier_id = NEW.supplier_id AND model = NEW.model;

        INSERT INTO supplier_model_price_history (
            supplier_id, model, version, input_price, output_price, currency, valid_from
        ) VALUES (
            NEW.supplier_id, NEW.model, next_version,
            NEW.input_price, NEW.output_price, NEW.currency, changed_at
        );
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        UPDATE supplier_model_price_history
        SET valid_to = GREATEST(NOW(), valid_from + INTERVAL '1 microsecond')
        WHERE supplier_id = OLD.supplier_id AND model = OLD.model AND valid_to IS NULL;
        RETURN OLD;
    END IF;

    IF OLD.input_price IS NOT DISTINCT FROM NEW.input_price
       AND OLD.output_price IS NOT DISTINCT FROM NEW.output_price
       AND OLD.currency IS NOT DISTINCT FROM NEW.currency THEN
        RETURN NEW;
    END IF;

    SELECT GREATEST(
        NEW.updated_at,
        COALESCE(MAX(valid_from) + INTERVAL '1 microsecond', NEW.updated_at)
    ), COALESCE(MAX(version), 0) + 1
    INTO changed_at, next_version
    FROM supplier_model_price_history
    WHERE supplier_id = NEW.supplier_id AND model = NEW.model;

    UPDATE supplier_model_price_history
    SET valid_to = changed_at
    WHERE supplier_id = NEW.supplier_id AND model = NEW.model AND valid_to IS NULL;

    INSERT INTO supplier_model_price_history (
        supplier_id, model, version, input_price, output_price, currency, valid_from
    ) VALUES (
        NEW.supplier_id, NEW.model, next_version,
        NEW.input_price, NEW.output_price, NEW.currency, changed_at
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER supplier_models_price_history
AFTER INSERT OR UPDATE OF input_price, output_price, currency ON supplier_models
FOR EACH ROW EXECUTE FUNCTION record_supplier_model_price_history();

CREATE TRIGGER supplier_models_price_history_delete
AFTER DELETE ON supplier_models
FOR EACH ROW EXECUTE FUNCTION record_supplier_model_price_history();

CREATE TABLE scoring_aggregation_state (
    name text PRIMARY KEY,
    initialized_at timestamptz,
    facts_through timestamptz,
    updated_at timestamptz NOT NULL,
    CHECK (name = 'minute_metrics_v1')
);

INSERT INTO scoring_aggregation_state(name, updated_at)
VALUES ('minute_metrics_v1', NOW());

CREATE TABLE request_metrics_1m (
    bucket_start timestamptz NOT NULL,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    source text NOT NULL CHECK (source IN ('real_traffic', 'direct_probe', 'site_probe')),
    request_count bigint NOT NULL DEFAULT 0,
    success_count bigint NOT NULL DEFAULT 0,
    failure_count bigint NOT NULL DEFAULT 0,
    mapped_count bigint NOT NULL DEFAULT 0,
    stream_count bigint NOT NULL DEFAULT 0,
    stream_completed_count bigint NOT NULL DEFAULT 0,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    ttft_sum_ms bigint NOT NULL DEFAULT 0,
    ttft_count bigint NOT NULL DEFAULT 0,
    success_duration_sum_ms bigint NOT NULL DEFAULT 0,
    success_duration_count bigint NOT NULL DEFAULT 0,
    failure_duration_sum_ms bigint NOT NULL DEFAULT 0,
    failure_duration_count bigint NOT NULL DEFAULT 0,
    coarse_duration_count bigint NOT NULL DEFAULT 0,
    computed_at timestamptz NOT NULL,
    PRIMARY KEY (bucket_start, site_id, model, source)
);

CREATE INDEX request_metrics_scope_idx
    ON request_metrics_1m(site_id, model, bucket_start DESC);

CREATE TABLE attempt_metrics_1m (
    bucket_start timestamptz NOT NULL,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    source text NOT NULL CHECK (source IN ('real_traffic', 'direct_probe', 'site_probe')),
    attempt_count bigint NOT NULL DEFAULT 0,
    sla_attempt_count bigint NOT NULL DEFAULT 0,
    success_count bigint NOT NULL DEFAULT 0,
    failure_count bigint NOT NULL DEFAULT 0,
    sla_failure_count bigint NOT NULL DEFAULT 0,
    rate_limited_count bigint NOT NULL DEFAULT 0,
    authentication_count bigint NOT NULL DEFAULT 0,
    balance_count bigint NOT NULL DEFAULT 0,
    timeout_count bigint NOT NULL DEFAULT 0,
    transport_count bigint NOT NULL DEFAULT 0,
    upstream_count bigint NOT NULL DEFAULT 0,
    stream_count bigint NOT NULL DEFAULT 0,
    stream_completed_count bigint NOT NULL DEFAULT 0,
    ttft_sum_ms bigint NOT NULL DEFAULT 0,
    ttft_count bigint NOT NULL DEFAULT 0,
    success_duration_sum_ms bigint NOT NULL DEFAULT 0,
    success_duration_count bigint NOT NULL DEFAULT 0,
    failure_duration_sum_ms bigint NOT NULL DEFAULT 0,
    failure_duration_count bigint NOT NULL DEFAULT 0,
    coarse_duration_count bigint NOT NULL DEFAULT 0,
    computed_at timestamptz NOT NULL,
    PRIMARY KEY (bucket_start, site_id, supplier_id, model, source)
);

CREATE INDEX attempt_metrics_scope_idx
    ON attempt_metrics_1m(site_id, supplier_id, model, bucket_start DESC);

CREATE TABLE request_latency_histograms_1m (
    bucket_start timestamptz NOT NULL,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    source text NOT NULL CHECK (source IN ('real_traffic', 'direct_probe', 'site_probe')),
    metric text NOT NULL CHECK (metric IN ('ttft', 'duration_success', 'duration_failure')),
    upper_bound_ms bigint NOT NULL CHECK (upper_bound_ms IN (
        50, 100, 250, 500, 1000, 2000, 3000, 5000, 8000,
        10000, 15000, 30000, 60000, 120000, 300000, 600000, 9223372036854775807
    )),
    sample_count bigint NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    PRIMARY KEY (bucket_start, site_id, model, source, metric, upper_bound_ms)
);

CREATE TABLE attempt_latency_histograms_1m (
    bucket_start timestamptz NOT NULL,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    source text NOT NULL CHECK (source IN ('real_traffic', 'direct_probe', 'site_probe')),
    metric text NOT NULL CHECK (metric IN ('ttft', 'duration_success', 'duration_failure')),
    upper_bound_ms bigint NOT NULL CHECK (upper_bound_ms IN (
        50, 100, 250, 500, 1000, 2000, 3000, 5000, 8000,
        10000, 15000, 30000, 60000, 120000, 300000, 600000, 9223372036854775807
    )),
    sample_count bigint NOT NULL DEFAULT 0 CHECK (sample_count >= 0),
    PRIMARY KEY (bucket_start, site_id, supplier_id, model, source, metric, upper_bound_ms)
);

CREATE INDEX attempt_latency_histograms_scope_idx
    ON attempt_latency_histograms_1m(site_id, supplier_id, model, bucket_start DESC, metric);

CREATE TABLE scoring_policy_versions (
    version text PRIMARY KEY,
    minimum_passive_samples integer NOT NULL CHECK (minimum_passive_samples > 0),
    window_weights jsonb NOT NULL,
    strategy_weights jsonb NOT NULL,
    thresholds jsonb NOT NULL,
    created_at timestamptz NOT NULL
);

INSERT INTO scoring_policy_versions (
    version, minimum_passive_samples, window_weights, strategy_weights, thresholds, created_at
) VALUES (
    'm2-shadow-v1',
    50,
    '{"15m":0.50,"1h":0.30,"24h":0.20}'::jsonb,
    '{
      "lowest_price":{"price":0.55,"latency":0.15,"sla":0.15,"quality":0.15},
      "low_latency":{"price":0.15,"latency":0.55,"sla":0.20,"quality":0.10},
      "high_sla":{"price":0.10,"latency":0.20,"sla":0.60,"quality":0.10},
      "high_quality":{"price":0.10,"latency":0.15,"sla":0.15,"quality":0.60},
      "balanced":{"price":0.25,"latency":0.25,"sla":0.30,"quality":0.20}
    }'::jsonb,
    '{
      "collection_fresh_seconds":900,
      "collection_stale_seconds":3600,
      "consecutive_failure_exclusion":3,
      "authenticity_valid_days":7,
      "capability_valid_days":7,
      "health_valid_hours":24,
      "join_threshold":75,
      "exit_threshold":50,
      "required_consecutive_windows":2,
      "recommendation_max_gap_seconds":600,
      "measurement_rule_version":"measurement-v1",
      "error_classification_version":"error-classification-v2",
      "aggregation_version":"minute-metrics-v1",
      "latency_buckets_ms":[50,100,250,500,1000,2000,3000,5000,8000,10000,15000,30000,60000,120000,300000,600000,"+Inf"],
      "price_relative_cost":{"best":1,"worst":3,"weight":0.70},
      "price_changes_per_day":{"best":0,"worst":4,"weight":0.15},
      "price_change_magnitude":{"best":0,"worst":0.5,"weight":0.15},
      "latency_ttft_p50_ms":{"best":500,"worst":10000,"weight":0.35},
      "latency_ttft_p95_ms":{"best":1000,"worst":20000,"weight":0.25},
      "latency_duration_p50_ms":{"best":1000,"worst":60000,"weight":0.15},
      "latency_duration_p95_ms":{"best":3000,"worst":120000,"weight":0.15},
      "latency_variability":{"best":1,"worst":5,"weight":0.10},
      "sla_attempt_success":{"best":1,"worst":0.8,"weight":0.35},
      "sla_rate_limit":{"best":0,"worst":0.2,"weight":0.20},
      "sla_consecutive_failures":{"best":0,"worst":3,"weight":0.15},
      "sla_stream_completion":{"best":1,"worst":0.8,"weight":0.10},
      "sla_recovery_ms":{"best":0,"worst":3600000,"weight":0.10},
      "sla_capacity":{"best":1,"worst":0.8,"weight":0.10},
      "quality_authenticity":{"best":1,"worst":0,"weight":0.40},
      "quality_capability":{"best":1,"worst":0,"weight":0.35},
      "quality_stability":{"best":1,"worst":0,"weight":0.10},
      "quality_evidence":{"best":1,"worst":0,"weight":0.15}
    }'::jsonb,
    NOW()
);

CREATE TABLE score_snapshots (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    policy_version text NOT NULL REFERENCES scoring_policy_versions(version),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    facts_through timestamptz,
    passive_samples bigint NOT NULL DEFAULT 0,
    active_samples bigint NOT NULL DEFAULT 0,
    price_score numeric(6, 3),
    latency_score numeric(6, 3),
    sla_score numeric(6, 3),
    quality_score numeric(6, 3),
    total_score numeric(6, 3),
    confidence text NOT NULL CHECK (confidence IN ('high', 'medium', 'low', 'insufficient')),
    eligibility text NOT NULL CHECK (eligibility IN ('eligible', 'excluded', 'insufficient')),
    hard_reasons jsonb NOT NULL DEFAULT '[]'::jsonb,
    explanation jsonb NOT NULL,
    authenticity_assessment_id uuid REFERENCES authenticity_assessments(id),
    capability_assessment_id uuid REFERENCES capability_assessments(id),
    created_at timestamptz NOT NULL,
    CHECK (window_end > window_start),
    CHECK (price_score IS NULL OR price_score BETWEEN 0 AND 100),
    CHECK (latency_score IS NULL OR latency_score BETWEEN 0 AND 100),
    CHECK (sla_score IS NULL OR sla_score BETWEEN 0 AND 100),
    CHECK (quality_score IS NULL OR quality_score BETWEEN 0 AND 100),
    CHECK (total_score IS NULL OR total_score BETWEEN 0 AND 100),
    UNIQUE (site_id, supplier_id, model, policy_version, window_end)
);

CREATE INDEX score_snapshots_current_idx
    ON score_snapshots(site_id, supplier_id, model, created_at DESC);

CREATE TABLE shadow_recommendations (
    id uuid PRIMARY KEY,
    score_snapshot_id uuid NOT NULL REFERENCES score_snapshots(id) ON DELETE CASCADE,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    strategy_kind text NOT NULL CHECK (strategy_kind IN ('lowest_price', 'low_latency', 'high_sla', 'high_quality', 'balanced')),
    action text NOT NULL CHECK (action IN ('join', 'keep', 'exit', 'watch', 'exclude')),
    current_member boolean NOT NULL,
    score numeric(6, 3),
    confidence text NOT NULL CHECK (confidence IN ('high', 'medium', 'low', 'insufficient')),
    reasons jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (score_snapshot_id, strategy_kind)
);

CREATE INDEX shadow_recommendations_current_idx
    ON shadow_recommendations(site_id, strategy_kind, model, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS shadow_recommendations_current_idx;
DROP TABLE IF EXISTS shadow_recommendations;
DROP INDEX IF EXISTS score_snapshots_current_idx;
DROP TABLE IF EXISTS score_snapshots;
DROP TABLE IF EXISTS scoring_policy_versions;
DROP INDEX IF EXISTS attempt_latency_histograms_scope_idx;
DROP TABLE IF EXISTS attempt_latency_histograms_1m;
DROP TABLE IF EXISTS request_latency_histograms_1m;
DROP INDEX IF EXISTS attempt_metrics_scope_idx;
DROP TABLE IF EXISTS attempt_metrics_1m;
DROP INDEX IF EXISTS request_metrics_scope_idx;
DROP TABLE IF EXISTS request_metrics_1m;
DROP TABLE IF EXISTS scoring_aggregation_state;
DROP TRIGGER IF EXISTS supplier_models_price_history_delete ON supplier_models;
DROP TRIGGER IF EXISTS supplier_models_price_history ON supplier_models;
DROP FUNCTION IF EXISTS record_supplier_model_price_history();
DROP INDEX IF EXISTS supplier_model_price_history_current_idx;
DROP TABLE IF EXISTS supplier_model_price_history;

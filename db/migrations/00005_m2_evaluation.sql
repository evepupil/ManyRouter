-- +goose Up
CREATE TABLE evaluation_daily_budgets (
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    bucket_date date NOT NULL,
    reserved_samples integer NOT NULL DEFAULT 0 CHECK (reserved_samples >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (supplier_id, model, bucket_date)
);

CREATE TABLE evaluation_runs (
    id uuid PRIMARY KEY,
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    relation_id uuid REFERENCES site_suppliers(id),
    site_id uuid REFERENCES sites(id),
    model varchar(191) NOT NULL,
    upstream_model varchar(191) NOT NULL,
    target_kind text NOT NULL CHECK (target_kind IN ('supplier_direct', 'site_route')),
    purpose text NOT NULL CHECK (purpose IN ('health', 'authenticity', 'quality', 'recovery')),
    status text NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'uncertain', 'cancelled')),
    suite_version text NOT NULL,
    algorithm_version text NOT NULL,
    random_seed bigint NOT NULL,
    reference_id uuid,
    planned_samples integer NOT NULL CHECK (planned_samples > 0),
    completed_samples integer NOT NULL DEFAULT 0 CHECK (completed_samples >= 0),
    requested_by text NOT NULL,
    request_reason text NOT NULL,
    error_code text,
    error_message text,
    requested_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    next_retry_at timestamptz,
    request_key varchar(128),
    request_hash char(64),
    CHECK (
        (target_kind = 'supplier_direct' AND site_id IS NULL AND relation_id IS NULL)
        OR (target_kind = 'site_route' AND site_id IS NOT NULL AND relation_id IS NOT NULL)
    ),
    CHECK (completed_samples <= planned_samples),
    CHECK (
        (request_key IS NULL AND request_hash IS NULL)
        OR (request_key IS NOT NULL AND request_hash ~ '^[0-9a-f]{64}$')
    )
);

CREATE UNIQUE INDEX evaluation_runs_active_target_idx
    ON evaluation_runs(
        supplier_id,
        model,
        target_kind,
        COALESCE(site_id, '00000000-0000-0000-0000-000000000000'::uuid),
        purpose
    )
    WHERE status IN ('pending', 'running', 'uncertain');
CREATE INDEX evaluation_runs_target_time_idx
    ON evaluation_runs(supplier_id, model, requested_at DESC);
CREATE INDEX evaluation_runs_ready_idx
    ON evaluation_runs(status, next_retry_at, requested_at);
CREATE UNIQUE INDEX evaluation_runs_request_key_idx
    ON evaluation_runs(request_key)
    WHERE request_key IS NOT NULL;

CREATE TABLE evaluation_budget_reservations (
    run_id uuid PRIMARY KEY REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    bucket_date date NOT NULL,
    planned_samples integer NOT NULL CHECK (planned_samples > 0),
    created_at timestamptz NOT NULL,
    FOREIGN KEY (supplier_id, model, bucket_date)
        REFERENCES evaluation_daily_budgets(supplier_id, model, bucket_date)
);

CREATE TABLE evaluation_samples (
    run_id uuid NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    probe_key varchar(96) NOT NULL,
    sample_index integer NOT NULL CHECK (sample_index >= 0),
    prompt_variant integer NOT NULL CHECK (prompt_variant >= 0),
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'failed', 'rejected', 'incomplete', 'uncertain')),
    normalized_answer varchar(256),
    answer_digest char(64) CHECK (answer_digest IS NULL OR answer_digest ~ '^[0-9a-f]{64}$'),
    response_model varchar(200),
    response_shape jsonb NOT NULL DEFAULT '{}'::jsonb,
    ttft_ms bigint CHECK (ttft_ms IS NULL OR ttft_ms >= 0),
    duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0),
    input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens >= 0),
    stream_completed boolean,
    upstream_status_code integer CHECK (upstream_status_code IS NULL OR upstream_status_code BETWEEN 100 AND 599),
    error_category text,
    error_code text,
    classification_version text NOT NULL,
    measurement_request_id uuid REFERENCES measurement_requests(id),
    collected_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, probe_key, sample_index)
);

CREATE INDEX evaluation_samples_measurement_idx
    ON evaluation_samples(measurement_request_id)
    WHERE measurement_request_id IS NOT NULL;

CREATE TABLE evaluation_fingerprints (
    run_id uuid PRIMARY KEY REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    protocol_version text NOT NULL,
    cells jsonb NOT NULL,
    valid_cells integer NOT NULL CHECK (valid_cells >= 0),
    valid_samples integer NOT NULL CHECK (valid_samples >= 0),
    self_distance numeric(8, 7),
    stable boolean NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE trusted_model_references (
    id uuid PRIMARY KEY,
    model varchar(191) NOT NULL,
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    fingerprint_run_id uuid NOT NULL REFERENCES evaluation_fingerprints(run_id),
    trust_level text NOT NULL CHECK (trust_level IN ('official', 'operator_trusted', 'community')),
    protocol_version text NOT NULL,
    reason text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    request_key varchar(128),
    request_hash char(64),
    CHECK (expires_at > created_at),
    CHECK (
        (request_key IS NULL AND request_hash IS NULL)
        OR (request_key IS NOT NULL AND request_hash ~ '^[0-9a-f]{64}$')
    )
);

CREATE UNIQUE INDEX trusted_model_references_current_idx
    ON trusted_model_references(model, supplier_id)
    WHERE revoked_at IS NULL;
CREATE INDEX trusted_model_references_model_idx
    ON trusted_model_references(model, created_at DESC);
CREATE UNIQUE INDEX trusted_model_references_request_key_idx
    ON trusted_model_references(request_key)
    WHERE request_key IS NOT NULL;

ALTER TABLE evaluation_runs
    ADD CONSTRAINT evaluation_runs_reference_fk
    FOREIGN KEY (reference_id) REFERENCES trusted_model_references(id);

CREATE TABLE authenticity_assessments (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES evaluation_runs(id),
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    site_id uuid REFERENCES sites(id),
    model varchar(191) NOT NULL,
    verdict text NOT NULL CHECK (verdict IN ('consistent', 'suspicious', 'inconsistent', 'insufficient')),
    confidence numeric(6, 5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    reference_id uuid REFERENCES trusted_model_references(id),
    mean_distance numeric(8, 7),
    self_distance numeric(8, 7),
    comparable_cells integer NOT NULL DEFAULT 0 CHECK (comparable_cells >= 0),
    evidence_conflict boolean NOT NULL DEFAULT false,
    evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
    algorithm_version text NOT NULL,
    checked_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > checked_at),
    CHECK ((verdict = 'consistent' AND reference_id IS NOT NULL) OR verdict <> 'consistent')
);

CREATE INDEX authenticity_assessments_target_idx
    ON authenticity_assessments(supplier_id, model, checked_at DESC);

CREATE TABLE capability_assessments (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL UNIQUE REFERENCES evaluation_runs(id),
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    site_id uuid REFERENCES sites(id),
    model varchar(191) NOT NULL,
    score numeric(6, 3) NOT NULL CHECK (score >= 0 AND score <= 100),
    confidence numeric(6, 5) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    completed_checks integer NOT NULL CHECK (completed_checks >= 0),
    total_checks integer NOT NULL CHECK (total_checks > 0),
    suite_version text NOT NULL,
    checked_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (completed_checks <= total_checks),
    CHECK (expires_at > checked_at)
);

CREATE INDEX capability_assessments_target_idx
    ON capability_assessments(supplier_id, model, checked_at DESC);

-- +goose Down
DROP INDEX IF EXISTS capability_assessments_target_idx;
DROP TABLE IF EXISTS capability_assessments;
DROP INDEX IF EXISTS authenticity_assessments_target_idx;
DROP TABLE IF EXISTS authenticity_assessments;
ALTER TABLE evaluation_runs DROP CONSTRAINT IF EXISTS evaluation_runs_reference_fk;
DROP INDEX IF EXISTS trusted_model_references_model_idx;
DROP INDEX IF EXISTS trusted_model_references_request_key_idx;
DROP INDEX IF EXISTS trusted_model_references_current_idx;
DROP TABLE IF EXISTS trusted_model_references;
DROP TABLE IF EXISTS evaluation_fingerprints;
DROP INDEX IF EXISTS evaluation_samples_measurement_idx;
DROP TABLE IF EXISTS evaluation_samples;
DROP TABLE IF EXISTS evaluation_budget_reservations;
DROP INDEX IF EXISTS evaluation_runs_request_key_idx;
DROP INDEX IF EXISTS evaluation_runs_ready_idx;
DROP INDEX IF EXISTS evaluation_runs_target_time_idx;
DROP INDEX IF EXISTS evaluation_runs_active_target_idx;
DROP TABLE IF EXISTS evaluation_runs;
DROP TABLE IF EXISTS evaluation_daily_budgets;

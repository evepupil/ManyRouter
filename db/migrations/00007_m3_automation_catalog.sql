-- +goose Up
CREATE TABLE score_runs (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    policy_version text NOT NULL REFERENCES scoring_policy_versions(version),
    window_end timestamptz NOT NULL,
    expected_targets integer NOT NULL CHECK (expected_targets > 0),
    completed_targets integer NOT NULL DEFAULT 0 CHECK (completed_targets >= 0 AND completed_targets <= expected_targets),
    status text NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    error_summary varchar(500),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (site_id, policy_version, window_end),
    CHECK ((status = 'running') = (completed_at IS NULL)),
    CHECK (status <> 'succeeded' OR completed_targets = expected_targets)
);

ALTER TABLE score_snapshots
    ADD COLUMN score_run_id uuid REFERENCES score_runs(id) ON DELETE SET NULL;

CREATE INDEX score_snapshots_run_idx
    ON score_snapshots(score_run_id)
    WHERE score_run_id IS NOT NULL;

CREATE TABLE strategy_automation_settings (
    strategy_id uuid PRIMARY KEY REFERENCES site_strategies(id) ON DELETE CASCADE,
    mode text NOT NULL DEFAULT 'manual' CHECK (mode IN ('manual', 'automatic')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    entry_closed_by_automation boolean NOT NULL DEFAULT false,
    reason varchar(500) NOT NULL,
    updated_by varchar(191) NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE automation_runs (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    score_run_id uuid NOT NULL REFERENCES score_runs(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('frozen', 'preview', 'no_change', 'pending_sync')),
    trigger_kind text NOT NULL CHECK (trigger_kind IN ('scheduled', 'operator')),
    route_plan_id uuid REFERENCES route_plan_versions(id) ON DELETE SET NULL,
    summary varchar(500) NOT NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX automation_runs_score_apply_idx
    ON automation_runs(site_id, score_run_id)
    WHERE status <> 'preview';

CREATE TABLE automation_decisions (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES automation_runs(id) ON DELETE CASCADE,
    strategy_id uuid NOT NULL REFERENCES site_strategies(id) ON DELETE CASCADE,
    relation_id uuid NOT NULL REFERENCES site_suppliers(id) ON DELETE CASCADE,
    action text NOT NULL CHECK (action IN ('join', 'keep', 'exit', 'exclude', 'watch', 'recover')),
    current_member boolean NOT NULL,
    target_member boolean NOT NULL,
    hold_action text NOT NULL CHECK (hold_action IN ('none', 'apply', 'clear')),
    reasons jsonb NOT NULL,
    score_snapshot_ids uuid[] NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (run_id, strategy_id, relation_id),
    CHECK (cardinality(score_snapshot_ids) > 0)
);

CREATE TABLE site_supplier_automation_holds (
    relation_id uuid PRIMARY KEY REFERENCES site_suppliers(id) ON DELETE CASCADE,
    active boolean NOT NULL,
    reason_codes jsonb NOT NULL,
    source_run_id uuid NOT NULL REFERENCES automation_runs(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    cleared_at timestamptz,
    CHECK (active = (cleared_at IS NULL))
);

CREATE TABLE site_product_tokens (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    token_hash char(64) NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('active', 'revoked')),
    reason varchar(500) NOT NULL,
    created_by varchar(191) NOT NULL,
    created_at timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at timestamptz,
    CHECK ((status = 'active') = (revoked_at IS NULL))
);

CREATE INDEX site_product_tokens_site_idx
    ON site_product_tokens(site_id, created_at DESC);

CREATE TABLE site_product_snapshots (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    version bigint NOT NULL CHECK (version > 0),
    route_plan_id uuid NOT NULL REFERENCES route_plan_versions(id) ON DELETE RESTRICT,
    score_run_id uuid REFERENCES score_runs(id) ON DELETE SET NULL,
    content jsonb NOT NULL,
    content_hash char(64) NOT NULL,
    facts_through timestamptz,
    created_at timestamptz NOT NULL,
    UNIQUE (site_id, version)
);

CREATE INDEX site_product_snapshots_current_idx
    ON site_product_snapshots(site_id, version DESC);

CREATE INDEX measurement_requests_group_metrics_idx
    ON measurement_requests(site_id, request_group, model, observed_at DESC)
    WHERE is_current AND source = 'real_traffic';

-- +goose Down
DROP INDEX IF EXISTS measurement_requests_group_metrics_idx;
DROP INDEX IF EXISTS site_product_snapshots_current_idx;
DROP TABLE IF EXISTS site_product_snapshots;
DROP INDEX IF EXISTS site_product_tokens_site_idx;
DROP TABLE IF EXISTS site_product_tokens;
DROP TABLE IF EXISTS site_supplier_automation_holds;
DROP TABLE IF EXISTS automation_decisions;
DROP INDEX IF EXISTS automation_runs_score_apply_idx;
DROP TABLE IF EXISTS automation_runs;
DROP TABLE IF EXISTS strategy_automation_settings;
DROP INDEX IF EXISTS score_snapshots_run_idx;
ALTER TABLE score_snapshots DROP COLUMN IF EXISTS score_run_id;
DROP TABLE IF EXISTS score_runs;

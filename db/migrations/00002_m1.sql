-- +goose Up
ALTER TABLE sites ADD COLUMN admin_user_id bigint NOT NULL DEFAULT 1 CHECK (admin_user_id > 0);
ALTER TABLE suppliers ADD COLUMN pending_credential_id uuid REFERENCES credentials(id),
    ADD COLUMN pending_credential_version integer,
    ADD CONSTRAINT suppliers_pending_credential_pair CHECK (
        (pending_credential_id IS NULL AND pending_credential_version IS NULL)
        OR (pending_credential_id IS NOT NULL AND pending_credential_version > credential_version)
    );
ALTER TABLE site_supplier_channels
    ADD COLUMN last_confirmed_credential_id uuid REFERENCES credentials(id),
    ADD COLUMN last_confirmed_credential_version integer,
    ADD COLUMN last_confirmed_enabled boolean,
    ADD CONSTRAINT site_supplier_channels_confirmed_credential_pair CHECK (
        (last_confirmed_credential_id IS NULL AND last_confirmed_credential_version IS NULL)
        OR (last_confirmed_credential_id IS NOT NULL AND last_confirmed_credential_version > 0)
    );
UPDATE site_supplier_channels SET last_confirmed_enabled = true WHERE last_confirmed_plan_version IS NOT NULL;
ALTER TABLE route_plan_versions DROP CONSTRAINT route_plan_versions_status_check;
ALTER TABLE route_plan_versions ADD CONSTRAINT route_plan_versions_status_check
    CHECK (status IN ('pending','applying','confirmed','failed','uncertain','superseded'));
ALTER TABLE sync_operations DROP CONSTRAINT sync_operations_status_check;
ALTER TABLE sync_operations ADD CONSTRAINT sync_operations_status_check
    CHECK (status IN ('pending','running','uncertain','succeeded','retryable_failed','manual_required','superseded'));

CREATE TABLE site_strategies (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id),
    kind text NOT NULL CHECK (kind IN ('lowest_price','low_latency','high_sla','high_quality','balanced')),
    group_key varchar(64) NOT NULL,
    display_name varchar(120) NOT NULL,
    enabled boolean NOT NULL,
    visible boolean NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE(site_id,kind),
    UNIQUE(site_id,group_key)
);
CREATE TABLE strategy_members (
    strategy_id uuid NOT NULL REFERENCES site_strategies(id),
    relation_id uuid NOT NULL REFERENCES site_suppliers(id),
    PRIMARY KEY(strategy_id,relation_id)
);
CREATE TABLE strategy_versions (
    strategy_id uuid NOT NULL REFERENCES site_strategies(id),
    version bigint NOT NULL,
    snapshot jsonb NOT NULL,
    reason text NOT NULL,
    actor_id text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY(strategy_id,version)
);
CREATE TABLE price_versions (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id),
    group_key varchar(64) NOT NULL,
    version bigint NOT NULL CHECK(version > 0),
    sale_ratio numeric(12,6) NOT NULL CHECK(sale_ratio > 0),
    reason text NOT NULL,
    status text NOT NULL CHECK(status IN ('draft','published','applied','superseded')),
    billing_basis jsonb NOT NULL,
    basis_hash text NOT NULL CHECK (basis_hash = '' OR basis_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL,
    published_at timestamptz,
    applied_at timestamptz,
    route_plan_id uuid REFERENCES route_plan_versions(id),
    UNIQUE(site_id,group_key,version)
);
CREATE UNIQUE INDEX price_versions_current_idx ON price_versions(site_id,group_key)
    WHERE status IN ('published','applied');
CREATE TABLE operators (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL CHECK(role IN ('owner','admin','observer')),
    enabled boolean NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE TABLE operator_sessions (
    token_hash text PRIMARY KEY,
    operator_id uuid NOT NULL REFERENCES operators(id),
    csrf_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE INDEX operator_sessions_expiry_idx ON operator_sessions(expires_at);
CREATE TABLE auth_login_attempts (
    key text PRIMARY KEY,
    attempts integer NOT NULL,
    window_start timestamptz NOT NULL
);
CREATE INDEX auth_login_attempts_window_idx ON auth_login_attempts(window_start);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM route_plan_versions WHERE snapshot->>'schema_version' = '2') THEN
        RAISE EXCEPTION 'M1 site-plan history exists; restore a compatible database backup before downgrading';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE auth_login_attempts;
DROP TABLE operator_sessions;
DROP TABLE operators;
DROP TABLE price_versions;
DROP TABLE strategy_versions;
DROP TABLE strategy_members;
DROP TABLE site_strategies;
ALTER TABLE site_supplier_channels DROP COLUMN last_confirmed_enabled,
    DROP COLUMN last_confirmed_credential_version, DROP COLUMN last_confirmed_credential_id;
ALTER TABLE sites DROP COLUMN admin_user_id;
ALTER TABLE suppliers DROP COLUMN pending_credential_version, DROP COLUMN pending_credential_id;
ALTER TABLE route_plan_versions DROP CONSTRAINT route_plan_versions_status_check;
ALTER TABLE route_plan_versions ADD CONSTRAINT route_plan_versions_status_check
    CHECK (status IN ('pending','applying','confirmed','failed','uncertain'));
ALTER TABLE sync_operations DROP CONSTRAINT sync_operations_status_check;
ALTER TABLE sync_operations ADD CONSTRAINT sync_operations_status_check
    CHECK (status IN ('pending','running','uncertain','succeeded','retryable_failed','manual_required'));

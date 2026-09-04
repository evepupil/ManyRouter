-- +goose Up
CREATE TABLE credentials (
    id uuid PRIMARY KEY,
    purpose text NOT NULL CHECK (purpose IN ('new_api_admin', 'supplier_api_key')),
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL CHECK (key_version > 0),
    created_at timestamptz NOT NULL,
    revoked_at timestamptz
);

CREATE TABLE sites (
    id uuid PRIMARY KEY,
    code varchar(63) NOT NULL UNIQUE,
    name varchar(120) NOT NULL,
    new_api_base_url text NOT NULL,
    admin_credential_id uuid NOT NULL REFERENCES credentials(id),
    status text NOT NULL CHECK (status IN ('enabled', 'disabled')),
    compatibility_status text NOT NULL CHECK (compatibility_status IN ('unknown', 'compatible', 'incompatible')),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE suppliers (
    id uuid PRIMARY KEY,
    code varchar(63) NOT NULL UNIQUE,
    name varchar(120) NOT NULL,
    protocol text NOT NULL CHECK (protocol IN ('openai_compatible')),
    upstream_base_url text NOT NULL,
    credential_id uuid NOT NULL REFERENCES credentials(id),
    credential_version integer NOT NULL CHECK (credential_version > 0),
    status text NOT NULL CHECK (status IN ('draft', 'enabled', 'disabled')),
    version bigint NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE supplier_models (
    supplier_id uuid NOT NULL REFERENCES suppliers(id) ON DELETE CASCADE,
    model varchar(191) NOT NULL,
    upstream_model varchar(191) NOT NULL,
    input_price numeric(20, 10) NOT NULL CHECK (input_price >= 0),
    output_price numeric(20, 10) NOT NULL CHECK (output_price >= 0),
    currency char(3) NOT NULL,
    enabled boolean NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (supplier_id, model)
);

CREATE TABLE site_suppliers (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id),
    supplier_id uuid NOT NULL REFERENCES suppliers(id),
    group_key varchar(64) NOT NULL,
    group_display_name varchar(120) NOT NULL,
    sale_ratio numeric(12, 6) NOT NULL CHECK (sale_ratio > 0),
    visible boolean NOT NULL,
    desired_status text NOT NULL CHECK (desired_status IN ('observing', 'enabled', 'disabled')),
    sync_status text NOT NULL CHECK (sync_status IN ('pending', 'syncing', 'active', 'failed', 'manual_locked')),
    version bigint NOT NULL CHECK (version > 0),
    current_plan_id uuid,
    last_confirmed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (site_id, supplier_id),
    UNIQUE (site_id, group_key)
);

CREATE TABLE site_supplier_channels (
    id uuid PRIMARY KEY,
    site_supplier_id uuid NOT NULL REFERENCES site_suppliers(id) ON DELETE CASCADE,
    managed_tag varchar(96) NOT NULL,
    external_channel_id bigint,
    last_confirmed_plan_version bigint,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (site_supplier_id, managed_tag),
    UNIQUE (site_supplier_id, external_channel_id)
);

CREATE TABLE route_plan_versions (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id),
    site_supplier_id uuid NOT NULL REFERENCES site_suppliers(id),
    version bigint NOT NULL CHECK (version > 0),
    previous_plan_id uuid REFERENCES route_plan_versions(id),
    reason text NOT NULL,
    snapshot jsonb NOT NULL,
    content_hash char(64) NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'applying', 'confirmed', 'failed', 'uncertain')),
    created_at timestamptz NOT NULL,
    confirmed_at timestamptz,
    UNIQUE (site_id, version)
);

ALTER TABLE site_suppliers
    ADD CONSTRAINT site_suppliers_current_plan_fk
    FOREIGN KEY (current_plan_id) REFERENCES route_plan_versions(id);

CREATE TABLE sync_operations (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id),
    site_supplier_id uuid NOT NULL REFERENCES site_suppliers(id),
    route_plan_id uuid NOT NULL REFERENCES route_plan_versions(id),
    status text NOT NULL CHECK (status IN ('pending', 'running', 'uncertain', 'succeeded', 'retryable_failed', 'manual_required')),
    current_step text,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    last_error_code text,
    last_error_message text,
    next_attempt_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    UNIQUE (route_plan_id)
);

CREATE TABLE sync_steps (
    operation_id uuid NOT NULL REFERENCES sync_operations(id) ON DELETE CASCADE,
    step_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'uncertain')),
    before_summary jsonb,
    after_summary jsonb,
    error_code text,
    error_message text,
    started_at timestamptz,
    finished_at timestamptz,
    PRIMARY KEY (operation_id, step_key)
);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    actor_type text NOT NULL CHECK (actor_type IN ('operator', 'system')),
    actor_id text NOT NULL,
    site_id uuid REFERENCES sites(id),
    object_type text NOT NULL,
    object_id text NOT NULL,
    action text NOT NULL,
    reason text NOT NULL,
    operation_id uuid,
    old_summary jsonb,
    new_summary jsonb,
    result text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE idempotency_records (
    scope text NOT NULL,
    idempotency_key varchar(128) NOT NULL,
    request_hash char(64) NOT NULL,
    status_code integer NOT NULL,
    response_body jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, idempotency_key)
);

CREATE INDEX route_plan_versions_relation_idx ON route_plan_versions (site_supplier_id, version DESC);
CREATE INDEX sync_operations_ready_idx ON sync_operations (status, next_attempt_at);
CREATE INDEX audit_events_object_idx ON audit_events (object_type, object_id, created_at DESC);
CREATE INDEX idempotency_records_expiry_idx ON idempotency_records (expires_at);

-- +goose Down
DROP INDEX IF EXISTS idempotency_records_expiry_idx;
DROP INDEX IF EXISTS audit_events_object_idx;
DROP INDEX IF EXISTS sync_operations_ready_idx;
DROP INDEX IF EXISTS route_plan_versions_relation_idx;
DROP TABLE IF EXISTS idempotency_records;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS sync_steps;
DROP TABLE IF EXISTS sync_operations;
ALTER TABLE site_suppliers DROP CONSTRAINT IF EXISTS site_suppliers_current_plan_fk;
DROP TABLE IF EXISTS route_plan_versions;
DROP TABLE IF EXISTS site_supplier_channels;
DROP TABLE IF EXISTS site_suppliers;
DROP TABLE IF EXISTS supplier_models;
DROP TABLE IF EXISTS suppliers;
DROP TABLE IF EXISTS sites;
DROP TABLE IF EXISTS credentials;

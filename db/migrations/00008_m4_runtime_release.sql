-- +goose Up
CREATE TABLE site_compatibility_checks (
    id uuid PRIMARY KEY,
    site_id uuid NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('managed', 'legacy', 'unknown')),
    verdict text NOT NULL CHECK (verdict IN ('compatible', 'unverified', 'incompatible', 'unreachable')),
    catalog_version varchar(64) NOT NULL,
    new_api_version varchar(128) NOT NULL DEFAULT '',
    contract_version varchar(128) NOT NULL DEFAULT '',
    database_type varchar(32) NOT NULL DEFAULT '',
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    state_hash varchar(64) NOT NULL DEFAULT '',
    billing_basis_hash varchar(64) NOT NULL DEFAULT '',
    conflicts jsonb NOT NULL DEFAULT '[]'::jsonb,
    reasons jsonb NOT NULL DEFAULT '[]'::jsonb,
    error_code varchar(128) NOT NULL DEFAULT '',
    error_message varchar(500) NOT NULL DEFAULT '',
    checked_by varchar(191) NOT NULL,
    checked_at timestamptz NOT NULL
);

CREATE INDEX site_compatibility_checks_latest_idx
    ON site_compatibility_checks(site_id, checked_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS site_compatibility_checks_latest_idx;
DROP TABLE IF EXISTS site_compatibility_checks;

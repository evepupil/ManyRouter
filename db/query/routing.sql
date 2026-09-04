-- name: CreateSiteSupplier :one
INSERT INTO site_suppliers (
    id, site_id, supplier_id, group_key, group_display_name,
    sale_ratio, visible, desired_status, sync_status, version,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12
)
RETURNING *;

-- name: GetSiteSupplier :one
SELECT *
FROM site_suppliers
WHERE id = $1;

-- name: GetSiteSupplierDetails :one
SELECT
    ss.id,
    ss.site_id,
    ss.supplier_id,
    ss.group_key,
    ss.group_display_name,
    ss.sale_ratio,
    ss.visible,
    ss.desired_status,
    ss.sync_status,
    ss.version,
    ss.current_plan_id,
    ss.last_confirmed_at,
    ss.created_at,
    ss.updated_at,
    COALESCE(rp.version, 0)::bigint AS route_plan_version
FROM site_suppliers ss
LEFT JOIN route_plan_versions rp ON rp.id = ss.current_plan_id
WHERE ss.id = $1;

-- name: CreateSiteSupplierChannel :one
INSERT INTO site_supplier_channels (
    id, site_supplier_id, managed_tag, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetSiteSupplierChannel :one
SELECT *
FROM site_supplier_channels
WHERE site_supplier_id = $1
ORDER BY created_at
LIMIT 1;

-- name: LockSitePlanVersion :exec
SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0));

-- name: GetMaxSitePlanVersion :one
SELECT COALESCE(MAX(version), 0)::bigint AS max_version
FROM route_plan_versions
WHERE site_id = $1;

-- name: CreateRoutePlan :one
INSERT INTO route_plan_versions (
    id, site_id, site_supplier_id, version, previous_plan_id,
    reason, snapshot, content_hash, status, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10
)
RETURNING *;

-- name: SetCurrentRoutePlan :exec
UPDATE site_suppliers
SET current_plan_id = $2,
    sync_status = 'pending',
    updated_at = $3,
    version = version + 1
WHERE id = $1;

-- name: GetRoutePlan :one
SELECT *
FROM route_plan_versions
WHERE id = $1;

-- name: GetCurrentRoutePlan :one
SELECT rp.*
FROM route_plan_versions rp
JOIN site_suppliers ss ON ss.current_plan_id = rp.id
WHERE ss.id = $1;

-- name: FindRoutePlanByHash :one
SELECT *
FROM route_plan_versions
WHERE site_id = $1 AND content_hash = $2
ORDER BY version DESC
LIMIT 1;

-- name: SetRoutePlanApplying :exec
UPDATE route_plan_versions
SET status = 'applying'
WHERE id = $1 AND status IN ('pending', 'failed', 'uncertain');

-- name: SetRoutePlanFailed :exec
UPDATE route_plan_versions
SET status = $2
WHERE id = $1;

-- name: ConfirmRoutePlan :exec
UPDATE route_plan_versions
SET status = 'confirmed', confirmed_at = $2
WHERE id = $1;

-- name: BindExternalChannel :exec
UPDATE site_supplier_channels
SET external_channel_id = $2,
    updated_at = $3
WHERE id = $1;

-- name: ConfirmChannelBinding :exec
UPDATE site_supplier_channels
SET external_channel_id = $2,
    last_confirmed_plan_version = $3,
    updated_at = $4
WHERE id = $1;

-- name: SetSiteSupplierSyncing :exec
UPDATE site_suppliers
SET sync_status = 'syncing', updated_at = $2
WHERE id = $1;

-- name: SetSiteSupplierSyncFailure :exec
UPDATE site_suppliers
SET sync_status = $2, updated_at = $3
WHERE id = $1;

-- name: ConfirmSiteSupplier :exec
UPDATE site_suppliers
SET sync_status = 'active',
    desired_status = 'enabled',
    last_confirmed_at = $2,
    updated_at = $2,
    version = version + 1
WHERE id = $1;

-- name: CreateSite :one
INSERT INTO sites (
    id, code, name, new_api_base_url, admin_credential_id,
    status, compatibility_status, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10
)
RETURNING *;

-- name: GetSite :one
SELECT *
FROM sites
WHERE id = $1;

-- name: SetSiteCompatibility :exec
UPDATE sites
SET compatibility_status = $2,
    updated_at = $3,
    version = version + 1
WHERE id = $1;

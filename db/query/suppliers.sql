-- name: CreateSupplier :one
INSERT INTO suppliers (
    id, code, name, protocol, upstream_base_url, credential_id,
    credential_version, status, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11
)
RETURNING *;

-- name: GetSupplier :one
SELECT *
FROM suppliers
WHERE id = $1;

-- name: CreateSupplierModel :one
INSERT INTO supplier_models (
    supplier_id, model, upstream_model, input_price, output_price,
    currency, enabled, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9
)
RETURNING *;

-- name: ListSupplierModels :many
SELECT *
FROM supplier_models
WHERE supplier_id = $1
ORDER BY model;

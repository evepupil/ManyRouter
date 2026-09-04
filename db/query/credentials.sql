-- name: CreateCredential :one
INSERT INTO credentials (
    id, purpose, ciphertext, nonce, key_version, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: GetCredential :one
SELECT *
FROM credentials
WHERE id = $1 AND revoked_at IS NULL;

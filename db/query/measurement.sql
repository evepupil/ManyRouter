-- name: ListCollectionSites :many
SELECT
    s.id AS site_id,
    s.code AS site_code,
    s.name AS site_name,
    s.new_api_base_url,
    s.admin_user_id,
    c.id AS credential_id,
    c.purpose AS credential_purpose,
    c.ciphertext,
    c.nonce,
    c.key_version
FROM sites s
JOIN credentials c ON c.id = s.admin_credential_id
WHERE s.status = 'enabled'
  AND c.revoked_at IS NULL
ORDER BY s.code;

-- name: GetCollectionSite :one
SELECT
    s.id AS site_id,
    s.code AS site_code,
    s.name AS site_name,
    s.new_api_base_url,
    s.admin_user_id,
    c.id AS credential_id,
    c.purpose AS credential_purpose,
    c.ciphertext,
    c.nonce,
    c.key_version
FROM sites s
JOIN credentials c ON c.id = s.admin_credential_id
WHERE s.id = $1
  AND s.status = 'enabled'
  AND c.revoked_at IS NULL;

-- name: EnsureCollectionCursor :exec
INSERT INTO collection_cursors (site_id)
VALUES ($1)
ON CONFLICT (site_id) DO NOTHING;

-- name: GetCollectionCursor :one
SELECT *
FROM collection_cursors
WHERE site_id = $1;

-- name: LockCollectionCursor :one
SELECT *
FROM collection_cursors
WHERE site_id = $1
FOR UPDATE;

-- name: MarkCollectionSuccess :exec
UPDATE collection_cursors
SET committed_created_at = $2,
    committed_source_id = $3,
    scanned_through_at = $4,
    source_latest_created_at = CASE
        WHEN sqlc.narg('source_latest_created_at')::bigint IS NULL THEN source_latest_created_at
        ELSE GREATEST(COALESCE(source_latest_created_at, 0), sqlc.narg('source_latest_created_at')::bigint)
    END,
    last_read_at = sqlc.arg('last_read_at'),
    last_success_at = sqlc.arg('last_read_at'),
    last_error_code = NULL,
    last_error_message = NULL,
    consecutive_failures = 0,
    data_gap = sqlc.arg('data_gap') OR EXISTS (
        SELECT 1
        FROM measurement_quarantines quarantine
        WHERE quarantine.site_id = collection_cursors.site_id
          AND quarantine.resolved_at IS NULL
    ),
    updated_at = sqlc.arg('last_read_at')
WHERE collection_cursors.site_id = $1;

-- name: MarkCollectionFailure :exec
UPDATE collection_cursors
SET last_read_at = $2,
    last_error_at = $2,
    last_error_code = $3,
    last_error_message = $4,
    consecutive_failures = consecutive_failures + 1,
    data_gap = true,
    updated_at = $2
WHERE collection_cursors.site_id = $1;

-- name: ResolveChannelBinding :one
SELECT relation_id, supplier_id, managed_tag
FROM channel_binding_history
WHERE site_id = $1
  AND external_channel_id = $2
  AND valid_from <= $3
  AND (valid_to IS NULL OR valid_to > $3)
ORDER BY valid_from DESC
LIMIT 1;

-- name: ListChannelBindingHistory :many
SELECT site_id, external_channel_id, relation_id, supplier_id, managed_tag, valid_from, valid_to
FROM channel_binding_history
WHERE site_id = $1
  AND valid_from < sqlc.arg('window_end')
  AND (valid_to IS NULL OR valid_to > sqlc.arg('window_start'))
ORDER BY external_channel_id, valid_from;

-- name: InsertMeasurementRequest :execrows
INSERT INTO measurement_requests (
    id, site_id, source, request_hash, revision,
    source_contract, source_event_key, source_event_id, source_created_at,
    terminal_created_at, terminal_source_id,
    request_id, observed_at, model, request_group, outcome,
    final_relation_id, final_supplier_id, final_external_channel_id,
    attribution_status, is_stream, stream_completed, ttft_ms, duration_ms, duration_resolution_ms,
    input_tokens, output_tokens, upstream_status_code, error_category, error_responsibility, error_code,
    classification_version, completeness, missing_reason, recorded_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19,
    $20, $21, $22, $23, $24, $25,
    $26, $27, $28, $29, $30, $31,
    $32, $33, $34, $35
)
ON CONFLICT (site_id, source, source_event_key) DO NOTHING;

-- name: GetCurrentMeasurementRequestRevision :one
SELECT id, revision, source_event_key, terminal_created_at, terminal_source_id
FROM measurement_requests
WHERE site_id IS NOT DISTINCT FROM sqlc.narg('site_id')::uuid
  AND source = sqlc.arg('source')
  AND request_hash = sqlc.arg('request_hash')
  AND is_current
FOR UPDATE;

-- name: SupersedeMeasurementRequest :execrows
UPDATE measurement_requests
SET is_current = FALSE,
    superseded_at = $2
WHERE id = $1 AND is_current;

-- name: InsertMeasurementQuarantine :execrows
INSERT INTO measurement_quarantines (
    id, site_id, source, source_event_key, source_created_at,
    source_id, reason_code, recorded_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (site_id, source, source_event_key) DO NOTHING;

-- name: ResolveMeasurementQuarantine :execrows
UPDATE measurement_quarantines
SET resolved_at = $3
WHERE site_id = $1 AND source_event_key = $2 AND resolved_at IS NULL;

-- name: RefreshCollectionDataGap :exec
UPDATE collection_cursors
SET data_gap = EXISTS (
        SELECT 1
        FROM measurement_quarantines quarantine
        WHERE quarantine.site_id = collection_cursors.site_id
          AND quarantine.resolved_at IS NULL
    ),
    updated_at = $2
WHERE collection_cursors.site_id = $1;

-- name: InsertMeasurementAttempt :execrows
INSERT INTO measurement_attempts (
    id, request_measurement_id, attempt_index, relation_id, supplier_id,
    external_channel_id, attribution_status, model, outcome, is_final,
    is_stream, stream_completed, produced_visible_output, ttft_ms, duration_ms, duration_resolution_ms, upstream_status_code,
    error_category, error_responsibility, error_code, classification_version, observed_at, recorded_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22, $23
)
ON CONFLICT (request_measurement_id, attempt_index) DO NOTHING;

-- name: ListCollectionStatus :many
SELECT
    s.id AS site_id,
    s.name AS site_name,
    c.source_kind,
    c.contract_version,
    c.committed_created_at,
    c.committed_source_id,
    c.scanned_through_at,
    c.source_latest_created_at,
    c.last_read_at,
    c.last_success_at,
    c.last_error_at,
    c.last_error_code,
    c.last_error_message,
    c.consecutive_failures,
    c.data_gap,
    c.updated_at
FROM sites s
LEFT JOIN collection_cursors c ON c.site_id = s.id
WHERE (sqlc.narg('site_id')::uuid IS NULL OR s.id = sqlc.narg('site_id')::uuid)
ORDER BY s.name, s.id;

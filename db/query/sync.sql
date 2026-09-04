-- name: CreateSyncOperation :one
INSERT INTO sync_operations (
    id, site_id, site_supplier_id, route_plan_id, status,
    attempt, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, 'pending',
    0, $5, $5
)
ON CONFLICT (route_plan_id) DO UPDATE
SET route_plan_id = EXCLUDED.route_plan_id
RETURNING *;

-- name: GetSyncOperation :one
SELECT *
FROM sync_operations
WHERE id = $1;

-- name: GetSyncOperationByPlan :one
SELECT *
FROM sync_operations
WHERE route_plan_id = $1;

-- name: MarkSyncRunning :exec
UPDATE sync_operations
SET status = 'running',
    current_step = $2,
    attempt = attempt + 1,
    last_error_code = NULL,
    last_error_message = NULL,
    next_attempt_at = NULL,
    updated_at = $3
WHERE id = $1;

-- name: SetSyncStep :exec
INSERT INTO sync_steps (
    operation_id, step_key, status, before_summary, after_summary,
    error_code, error_message, started_at, finished_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9
)
ON CONFLICT (operation_id, step_key) DO UPDATE
SET status = EXCLUDED.status,
    before_summary = EXCLUDED.before_summary,
    after_summary = EXCLUDED.after_summary,
    error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message,
    started_at = COALESCE(sync_steps.started_at, EXCLUDED.started_at),
    finished_at = EXCLUDED.finished_at;

-- name: MarkSyncSucceeded :exec
UPDATE sync_operations
SET status = 'succeeded',
    current_step = 'confirmed',
    last_error_code = NULL,
    last_error_message = NULL,
    next_attempt_at = NULL,
    updated_at = $2,
    completed_at = $2
WHERE id = $1;

-- name: MarkSyncFailed :exec
UPDATE sync_operations
SET status = $2,
    current_step = $3,
    last_error_code = $4,
    last_error_message = $5,
    next_attempt_at = $6,
    updated_at = $7
WHERE id = $1;

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (
    id, actor_type, actor_id, site_id, object_type, object_id,
    action, reason, operation_id, old_summary, new_summary, result, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13
);

-- name: GetIdempotencyRecord :one
SELECT *
FROM idempotency_records
WHERE scope = $1 AND idempotency_key = $2 AND expires_at > $3;

-- name: CreateIdempotencyRecord :one
INSERT INTO idempotency_records (
    scope, idempotency_key, request_hash, status_code,
    response_body, created_at, expires_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7
)
ON CONFLICT (scope, idempotency_key) DO NOTHING
RETURNING *;

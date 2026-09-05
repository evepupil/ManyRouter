-- name: AuthInitialized :one
SELECT EXISTS(SELECT 1 FROM operators);

-- name: LockOperatorSetup :exec
SELECT pg_advisory_xact_lock(7192210801);

-- name: CreateInitialOperator :exec
INSERT INTO operators (id, username, password_hash, role, enabled, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: FindOperator :one
SELECT * FROM operators WHERE username = $1;

-- name: SaveOperatorSession :exec
INSERT INTO operator_sessions (token_hash, operator_id, csrf_hash, expires_at, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: FindOperatorSession :one
SELECT s.token_hash, s.csrf_hash, s.expires_at, s.created_at,
       o.id, o.username, o.role, o.enabled
FROM operator_sessions s
JOIN operators o ON o.id = s.operator_id
WHERE s.token_hash = $1;

-- name: DeleteOperatorSession :exec
WITH deleted AS (
    DELETE FROM operator_sessions WHERE token_hash = sqlc.arg(token_hash)
    RETURNING operator_id
)
INSERT INTO audit_events (
    id, actor_type, actor_id, object_type, object_id, action, reason, result, created_at
)
SELECT sqlc.arg(id), 'operator', operator_id::text, 'operator', operator_id::text,
       'operator.logout', 'operator logout', 'succeeded', sqlc.arg(created_at)
FROM deleted;

-- name: ConsumeAuthAttempt :one
INSERT INTO auth_login_attempts (key, attempts, window_start)
VALUES (sqlc.arg(key), 1, sqlc.arg(now_at))
ON CONFLICT (key) DO UPDATE
SET attempts = CASE
        WHEN auth_login_attempts.window_start <= sqlc.arg(cutoff)::timestamptz THEN 1
        ELSE LEAST(auth_login_attempts.attempts + 1, 1000)
    END,
    window_start = CASE
        WHEN auth_login_attempts.window_start <= sqlc.arg(cutoff)::timestamptz THEN sqlc.arg(now_at)::timestamptz
        ELSE auth_login_attempts.window_start
    END
RETURNING attempts;

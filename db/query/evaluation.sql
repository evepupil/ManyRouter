-- name: ListEvaluationTargets :many
SELECT
    s.id AS supplier_id,
    s.name AS supplier_name,
    s.upstream_base_url,
    sm.model,
    sm.upstream_model,
    c.id AS credential_id,
    c.purpose AS credential_purpose,
    c.ciphertext,
    c.nonce,
    c.key_version
FROM suppliers s
JOIN supplier_models sm ON sm.supplier_id = s.id
JOIN credentials c ON c.id = s.credential_id
WHERE s.status = 'enabled'
  AND sm.enabled
  AND c.revoked_at IS NULL
ORDER BY s.name, sm.model;

-- name: GetEvaluationTarget :one
SELECT
    s.id AS supplier_id,
    s.name AS supplier_name,
    s.upstream_base_url,
    sm.model,
    sm.upstream_model,
    c.id AS credential_id,
    c.purpose AS credential_purpose,
    c.ciphertext,
    c.nonce,
    c.key_version
FROM suppliers s
JOIN supplier_models sm ON sm.supplier_id = s.id
JOIN credentials c ON c.id = s.credential_id
WHERE s.id = $1
  AND sm.model = $2
  AND s.status = 'enabled'
  AND sm.enabled
  AND c.revoked_at IS NULL;

-- name: EnsureEvaluationDailyBudget :exec
INSERT INTO evaluation_daily_budgets (supplier_id, model, bucket_date, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (supplier_id, model, bucket_date) DO NOTHING;

-- name: LockEvaluationDailyBudget :one
SELECT *
FROM evaluation_daily_budgets
WHERE supplier_id = $1 AND model = $2 AND bucket_date = $3
FOR UPDATE;

-- name: ReserveEvaluationDailyBudget :exec
UPDATE evaluation_daily_budgets
SET reserved_samples = reserved_samples + $4,
    updated_at = $5
WHERE supplier_id = $1 AND model = $2 AND bucket_date = $3;

-- name: CreateEvaluationBudgetReservation :exec
INSERT INTO evaluation_budget_reservations (
    run_id, supplier_id, model, bucket_date, planned_samples, created_at
) VALUES ($1, $2, $3, $4, $5, $6);

-- name: LockEvaluationRequestKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg('lock_key')::text, 0));

-- name: FindEvaluationRunByRequestKey :one
SELECT *
FROM evaluation_runs
WHERE request_key = $1;

-- name: CreateEvaluationRun :one
INSERT INTO evaluation_runs (
    id, supplier_id, relation_id, site_id, model, upstream_model,
    target_kind, purpose, status, suite_version, algorithm_version,
    random_seed, reference_id, planned_samples, completed_samples,
    requested_by, request_reason, requested_at, request_key, request_hash
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, 'pending', $9, $10,
    $11, $12, $13, 0,
    $14, $15, $16, $17, $18
)
RETURNING *;

-- name: GetEvaluationRun :one
SELECT * FROM evaluation_runs WHERE id = $1;

-- name: ListEvaluationRuns :many
SELECT run.*, supplier.name AS supplier_name, COUNT(*) OVER()::bigint AS total_count
FROM evaluation_runs run
JOIN suppliers supplier ON supplier.id = run.supplier_id
WHERE (
    sqlc.narg('site_id')::uuid IS NULL
    OR run.site_id = sqlc.narg('site_id')::uuid
    OR (
        run.site_id IS NULL
        AND EXISTS (
            SELECT 1 FROM site_suppliers relation
            WHERE relation.site_id = sqlc.narg('site_id')::uuid
              AND relation.supplier_id = run.supplier_id
        )
    )
)
  AND (sqlc.narg('supplier_id')::uuid IS NULL OR run.supplier_id = sqlc.narg('supplier_id')::uuid)
  AND (sqlc.narg('model')::text IS NULL OR run.model = sqlc.narg('model')::text)
  AND (sqlc.narg('purpose')::text IS NULL OR run.purpose = sqlc.narg('purpose')::text)
ORDER BY run.requested_at DESC, run.id DESC
LIMIT sqlc.arg('page_limit') OFFSET sqlc.arg('page_offset');

-- name: FindRecentEvaluationRun :one
SELECT *
FROM evaluation_runs
WHERE supplier_id = $1
  AND model = $2
  AND target_kind = $3
  AND purpose = $4
  AND requested_at >= $5
ORDER BY requested_at DESC
LIMIT 1;

-- name: StartEvaluationRun :execrows
UPDATE evaluation_runs
SET status = 'running', started_at = $2, error_code = NULL, error_message = NULL
WHERE id = $1 AND status IN ('pending', 'uncertain');

-- name: AdvanceEvaluationRun :exec
UPDATE evaluation_runs
SET completed_samples = (
    SELECT COUNT(*)::integer FROM evaluation_samples WHERE run_id = $1 AND outcome <> 'uncertain'
)
WHERE id = $1;

-- name: CompleteEvaluationRun :execrows
UPDATE evaluation_runs
SET status = 'succeeded',
    completed_samples = (SELECT COUNT(*)::integer FROM evaluation_samples WHERE run_id = $1 AND outcome <> 'uncertain'),
    completed_at = $2,
    next_retry_at = NULL,
    error_code = NULL,
    error_message = NULL
WHERE id = $1 AND status = 'running';

-- name: FailEvaluationRun :execrows
UPDATE evaluation_runs
SET status = $2,
    completed_samples = (SELECT COUNT(*)::integer FROM evaluation_samples WHERE run_id = $1 AND outcome <> 'uncertain'),
    error_code = $3,
    error_message = $4,
    completed_at = $5,
    next_retry_at = $6
WHERE id = $1 AND status IN ('pending', 'running', 'uncertain');

-- name: InsertEvaluationSample :execrows
INSERT INTO evaluation_samples (
    run_id, probe_key, sample_index, prompt_variant, outcome,
    normalized_answer, answer_digest, response_model, response_shape,
    ttft_ms, duration_ms, input_tokens, output_tokens, stream_completed,
    upstream_status_code, error_category, error_code, classification_version,
    measurement_request_id, collected_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13, $14,
    $15, $16, $17, $18,
    $19, $20
)
ON CONFLICT (run_id, probe_key, sample_index) DO NOTHING;

-- name: ReserveEvaluationSample :execrows
INSERT INTO evaluation_samples (
    run_id, probe_key, sample_index, prompt_variant, outcome,
    response_shape, classification_version, collected_at
) VALUES ($1, $2, $3, $4, 'uncertain', '{}'::jsonb, $5, $6)
ON CONFLICT (run_id, probe_key, sample_index) DO NOTHING;

-- name: CompleteEvaluationSample :execrows
UPDATE evaluation_samples
SET outcome = $4,
    normalized_answer = $5,
    answer_digest = $6,
    response_model = $7,
    response_shape = $8,
    ttft_ms = $9,
    duration_ms = $10,
    input_tokens = $11,
    output_tokens = $12,
    stream_completed = $13,
    upstream_status_code = $14,
    error_category = $15,
    error_code = $16,
    classification_version = $17,
    measurement_request_id = $18,
    collected_at = $19
WHERE run_id = $1 AND probe_key = $2 AND sample_index = $3 AND outcome = 'uncertain';

-- name: ListEvaluationSamples :many
SELECT *
FROM evaluation_samples
WHERE run_id = $1
ORDER BY probe_key, sample_index;

-- name: CreateEvaluationFingerprint :exec
INSERT INTO evaluation_fingerprints (
    run_id, protocol_version, cells, valid_cells, valid_samples,
    self_distance, stable, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (run_id) DO NOTHING;

-- name: GetEvaluationFingerprint :one
SELECT * FROM evaluation_fingerprints WHERE run_id = $1;

-- name: CreateTrustedModelReference :one
INSERT INTO trusted_model_references (
    id, model, supplier_id, fingerprint_run_id, trust_level,
    protocol_version, reason, created_by, created_at, expires_at,
    request_key, request_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: FindTrustedModelReferenceByRequestKey :one
SELECT *
FROM trusted_model_references
WHERE request_key = $1;

-- name: RevokeTrustedModelReferences :exec
UPDATE trusted_model_references
SET revoked_at = $3
WHERE model = $1 AND supplier_id = $2 AND revoked_at IS NULL;

-- name: GetTrustedModelReference :one
SELECT * FROM trusted_model_references WHERE id = $1;

-- name: FindTrustedModelReference :one
SELECT *
FROM trusted_model_references
WHERE model = $1
  AND trust_level IN ('official', 'operator_trusted')
  AND revoked_at IS NULL
  AND expires_at > $2
ORDER BY CASE trust_level WHEN 'official' THEN 0 ELSE 1 END, created_at DESC
LIMIT 1;

-- name: CreateAuthenticityAssessment :exec
INSERT INTO authenticity_assessments (
    id, run_id, supplier_id, site_id, model, verdict, confidence,
    reference_id, mean_distance, self_distance, comparable_cells,
    evidence_conflict, evidence, algorithm_version, checked_at, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16
)
ON CONFLICT (run_id) DO NOTHING;

-- name: FindLatestAuthenticityAssessment :one
SELECT *
FROM authenticity_assessments
WHERE supplier_id = $1 AND model = $2
  AND expires_at > sqlc.arg('valid_at')
  AND checked_at <= sqlc.arg('valid_at')
ORDER BY checked_at DESC
LIMIT 1;

-- name: FindPreviousStableMismatch :one
WITH latest AS (
    SELECT *
    FROM authenticity_assessments
    WHERE supplier_id = $1
      AND model = $2
      AND reference_id = $3
    ORDER BY checked_at DESC, id DESC
    LIMIT 1
)
SELECT *
FROM latest
WHERE verdict IN ('suspicious', 'inconsistent')
  AND mean_distance > $4
  AND checked_at <= $5;

-- name: CreateCapabilityAssessment :exec
INSERT INTO capability_assessments (
    id, run_id, supplier_id, site_id, model, score, confidence,
    completed_checks, total_checks, suite_version, checked_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (run_id) DO NOTHING;

-- name: FindLatestCapabilityAssessment :one
SELECT *
FROM capability_assessments
WHERE supplier_id = $1 AND model = $2
ORDER BY checked_at DESC
LIMIT 1;

-- name: FindLatestCapabilityAssessmentBySuite :one
SELECT *
FROM capability_assessments
WHERE supplier_id = $1 AND model = $2 AND suite_version = $3
  AND expires_at > sqlc.arg('valid_at')
  AND checked_at <= sqlc.arg('valid_at')
ORDER BY checked_at DESC
LIMIT 1;

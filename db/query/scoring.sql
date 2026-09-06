-- name: LockScoringAggregationState :one
SELECT *
FROM scoring_aggregation_state
WHERE name = 'minute_metrics_v1'
FOR UPDATE;

-- name: UpdateScoringAggregationState :execrows
UPDATE scoring_aggregation_state
SET initialized_at = COALESCE(initialized_at, sqlc.arg('initialized_at')),
    facts_through = GREATEST(COALESCE(facts_through, sqlc.arg('facts_through')), sqlc.arg('facts_through')),
    updated_at = sqlc.arg('updated_at')
WHERE name = 'minute_metrics_v1';

-- name: DeleteRequestMetricsRange :exec
DELETE FROM request_metrics_1m
WHERE bucket_start >= $1 AND bucket_start < $2;

-- name: DeleteAttemptMetricsRange :exec
DELETE FROM attempt_metrics_1m
WHERE bucket_start >= $1 AND bucket_start < $2;

-- name: DeleteRequestHistogramsRange :exec
DELETE FROM request_latency_histograms_1m
WHERE bucket_start >= $1 AND bucket_start < $2;

-- name: DeleteAttemptHistogramsRange :exec
DELETE FROM attempt_latency_histograms_1m
WHERE bucket_start >= $1 AND bucket_start < $2;

-- name: AggregateRequestMetricsRange :exec
INSERT INTO request_metrics_1m (
    bucket_start, site_id, model, source, request_count, success_count,
    failure_count, mapped_count, stream_count, stream_completed_count,
    input_tokens, output_tokens, ttft_sum_ms, ttft_count,
    success_duration_sum_ms, success_duration_count,
    failure_duration_sum_ms, failure_duration_count, coarse_duration_count, computed_at
)
SELECT
    date_trunc('minute', observed_at), site_id, model, source,
    COUNT(*),
    COUNT(*) FILTER (WHERE outcome = 'succeeded'),
    COUNT(*) FILTER (WHERE outcome <> 'succeeded'),
    COUNT(*) FILTER (WHERE attribution_status = 'mapped'),
    COUNT(*) FILTER (WHERE is_stream AND ttft_ms IS NOT NULL),
    COUNT(*) FILTER (WHERE is_stream AND ttft_ms IS NOT NULL AND stream_completed IS TRUE),
    COALESCE(SUM(input_tokens), 0),
    COALESCE(SUM(output_tokens), 0),
    COALESCE(SUM(ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL), 0),
    COUNT(ttft_ms),
    COALESCE(SUM(duration_ms) FILTER (WHERE duration_ms IS NOT NULL AND outcome = 'succeeded'), 0),
    COUNT(duration_ms) FILTER (WHERE outcome = 'succeeded'),
    COALESCE(SUM(duration_ms) FILTER (WHERE duration_ms IS NOT NULL AND outcome <> 'succeeded'), 0),
    COUNT(duration_ms) FILTER (WHERE outcome <> 'succeeded'),
    COUNT(duration_ms) FILTER (WHERE duration_resolution_ms > 1),
    $3
FROM measurement_requests
WHERE site_id IS NOT NULL AND is_current AND observed_at >= $1 AND observed_at < $2
GROUP BY 1, 2, 3, 4;

-- name: AggregateAttemptMetricsRange :exec
INSERT INTO attempt_metrics_1m (
    bucket_start, site_id, supplier_id, model, source,
    attempt_count, sla_attempt_count, success_count, failure_count, sla_failure_count, rate_limited_count,
    authentication_count, balance_count, timeout_count, transport_count,
    upstream_count, stream_count, stream_completed_count,
    ttft_sum_ms, ttft_count, success_duration_sum_ms, success_duration_count,
    failure_duration_sum_ms, failure_duration_count, coarse_duration_count, computed_at
)
SELECT
    date_trunc('minute', attempt.observed_at), request.site_id, attempt.supplier_id,
    attempt.model, request.source,
    COUNT(*),
    COUNT(*) FILTER (WHERE attempt.outcome = 'succeeded' OR attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.outcome = 'succeeded'),
    COUNT(*) FILTER (WHERE attempt.outcome <> 'succeeded'),
    COUNT(*) FILTER (WHERE attempt.outcome <> 'succeeded' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category = 'rate_limited' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category = 'authentication' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category = 'balance_exhausted' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category = 'timeout' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category = 'stream_incomplete' AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.error_category IN ('upstream_unavailable', 'unknown') AND attempt.error_responsibility = 'supplier'),
    COUNT(*) FILTER (WHERE attempt.is_stream AND attempt.produced_visible_output),
    COUNT(*) FILTER (WHERE attempt.is_stream AND attempt.produced_visible_output AND attempt.stream_completed IS TRUE),
    COALESCE(SUM(attempt.ttft_ms) FILTER (WHERE attempt.ttft_ms IS NOT NULL), 0),
    COUNT(attempt.ttft_ms),
    COALESCE(SUM(attempt.duration_ms) FILTER (WHERE attempt.duration_ms IS NOT NULL AND attempt.outcome = 'succeeded'), 0),
    COUNT(attempt.duration_ms) FILTER (WHERE attempt.outcome = 'succeeded'),
    COALESCE(SUM(attempt.duration_ms) FILTER (WHERE attempt.duration_ms IS NOT NULL AND attempt.outcome <> 'succeeded'), 0),
    COUNT(attempt.duration_ms) FILTER (WHERE attempt.outcome <> 'succeeded'),
    COUNT(attempt.duration_ms) FILTER (WHERE attempt.duration_resolution_ms > 1),
    $3
FROM measurement_attempts attempt
JOIN measurement_requests request ON request.id = attempt.request_measurement_id
WHERE request.site_id IS NOT NULL
  AND request.is_current
  AND attempt.supplier_id IS NOT NULL
  AND attempt.observed_at >= $1 AND attempt.observed_at < $2
GROUP BY 1, 2, 3, 4, 5;

-- name: AggregateRequestHistogramsRange :exec
INSERT INTO request_latency_histograms_1m (
    bucket_start, site_id, model, source, metric, upper_bound_ms, sample_count
)
SELECT
    date_trunc('minute', request.observed_at), request.site_id, request.model, request.source,
    latency.metric,
    CASE
        WHEN latency.value_ms <= 50 THEN 50 WHEN latency.value_ms <= 100 THEN 100
        WHEN latency.value_ms <= 250 THEN 250 WHEN latency.value_ms <= 500 THEN 500
        WHEN latency.value_ms <= 1000 THEN 1000 WHEN latency.value_ms <= 2000 THEN 2000
        WHEN latency.value_ms <= 3000 THEN 3000 WHEN latency.value_ms <= 5000 THEN 5000
        WHEN latency.value_ms <= 8000 THEN 8000 WHEN latency.value_ms <= 10000 THEN 10000
        WHEN latency.value_ms <= 15000 THEN 15000 WHEN latency.value_ms <= 30000 THEN 30000
        WHEN latency.value_ms <= 60000 THEN 60000 WHEN latency.value_ms <= 120000 THEN 120000
        WHEN latency.value_ms <= 300000 THEN 300000 WHEN latency.value_ms <= 600000 THEN 600000
        ELSE 9223372036854775807
    END,
    COUNT(*)
FROM measurement_requests request
CROSS JOIN LATERAL (
    VALUES
        ('ttft'::text, request.ttft_ms),
        ('duration_success'::text, CASE WHEN request.outcome = 'succeeded' THEN request.duration_ms END),
        ('duration_failure'::text, CASE WHEN request.outcome <> 'succeeded' THEN request.duration_ms END)
) latency(metric, value_ms)
WHERE request.site_id IS NOT NULL
  AND request.is_current
  AND request.observed_at >= $1 AND request.observed_at < $2
  AND latency.value_ms IS NOT NULL AND latency.value_ms >= 0
GROUP BY 1, 2, 3, 4, 5, 6;

-- name: AggregateAttemptHistogramsRange :exec
INSERT INTO attempt_latency_histograms_1m (
    bucket_start, site_id, supplier_id, model, source, metric, upper_bound_ms, sample_count
)
SELECT
    date_trunc('minute', attempt.observed_at), request.site_id, attempt.supplier_id,
    attempt.model, request.source, latency.metric,
    CASE
        WHEN latency.value_ms <= 50 THEN 50 WHEN latency.value_ms <= 100 THEN 100
        WHEN latency.value_ms <= 250 THEN 250 WHEN latency.value_ms <= 500 THEN 500
        WHEN latency.value_ms <= 1000 THEN 1000 WHEN latency.value_ms <= 2000 THEN 2000
        WHEN latency.value_ms <= 3000 THEN 3000 WHEN latency.value_ms <= 5000 THEN 5000
        WHEN latency.value_ms <= 8000 THEN 8000 WHEN latency.value_ms <= 10000 THEN 10000
        WHEN latency.value_ms <= 15000 THEN 15000 WHEN latency.value_ms <= 30000 THEN 30000
        WHEN latency.value_ms <= 60000 THEN 60000 WHEN latency.value_ms <= 120000 THEN 120000
        WHEN latency.value_ms <= 300000 THEN 300000 WHEN latency.value_ms <= 600000 THEN 600000
        ELSE 9223372036854775807
    END,
    COUNT(*)
FROM measurement_attempts attempt
JOIN measurement_requests request ON request.id = attempt.request_measurement_id
CROSS JOIN LATERAL (
    VALUES
        ('ttft'::text, attempt.ttft_ms),
        ('duration_success'::text, CASE WHEN attempt.outcome = 'succeeded' THEN attempt.duration_ms END),
        ('duration_failure'::text, CASE WHEN attempt.outcome <> 'succeeded' THEN attempt.duration_ms END)
) latency(metric, value_ms)
WHERE request.site_id IS NOT NULL
  AND request.is_current
  AND attempt.supplier_id IS NOT NULL
  AND attempt.observed_at >= $1 AND attempt.observed_at < $2
  AND latency.value_ms IS NOT NULL AND latency.value_ms >= 0
GROUP BY 1, 2, 3, 4, 5, 6, 7;

-- name: GetAttemptWindowMetrics :one
SELECT
    COALESCE(SUM(attempt_count), 0)::bigint AS attempt_count,
    COALESCE(SUM(sla_attempt_count), 0)::bigint AS sla_attempt_count,
    COALESCE(SUM(success_count), 0)::bigint AS success_count,
    COALESCE(SUM(failure_count), 0)::bigint AS failure_count,
    COALESCE(SUM(sla_failure_count), 0)::bigint AS sla_failure_count,
    COALESCE(SUM(rate_limited_count), 0)::bigint AS rate_limited_count,
    COALESCE(SUM(authentication_count), 0)::bigint AS authentication_count,
    COALESCE(SUM(balance_count), 0)::bigint AS balance_count,
    COALESCE(SUM(timeout_count), 0)::bigint AS timeout_count,
    COALESCE(SUM(transport_count), 0)::bigint AS transport_count,
    COALESCE(SUM(upstream_count), 0)::bigint AS upstream_count,
    COALESCE(SUM(stream_count), 0)::bigint AS stream_count,
    COALESCE(SUM(stream_completed_count), 0)::bigint AS stream_completed_count,
    COALESCE(SUM(ttft_sum_ms), 0)::bigint AS ttft_sum_ms,
    COALESCE(SUM(ttft_count), 0)::bigint AS ttft_count,
    COALESCE(SUM(success_duration_sum_ms), 0)::bigint AS success_duration_sum_ms,
    COALESCE(SUM(success_duration_count), 0)::bigint AS success_duration_count,
    COALESCE(SUM(failure_duration_sum_ms), 0)::bigint AS failure_duration_sum_ms,
    COALESCE(SUM(failure_duration_count), 0)::bigint AS failure_duration_count,
    COALESCE(SUM(coarse_duration_count), 0)::bigint AS coarse_duration_count,
    COALESCE(MAX(bucket_start), '-infinity'::timestamptz)::timestamptz AS facts_through
FROM attempt_metrics_1m
WHERE site_id = $1 AND supplier_id = $2 AND model = $3 AND source = $4
  AND bucket_start >= $5 AND bucket_start < $6;

-- name: GetWindowRecoveryMillis :one
WITH attempt_events AS (
    SELECT
        attempt.id,
        attempt.request_measurement_id,
        attempt.attempt_index,
        attempt.outcome = 'succeeded' AS succeeded,
        attempt.observed_at AS event_at
    FROM measurement_attempts attempt
    JOIN measurement_requests request ON request.id = attempt.request_measurement_id
    WHERE request.site_id = sqlc.arg('site_id')
      AND request.is_current
      AND request.source = 'real_traffic'
      AND attempt.supplier_id = sqlc.arg('supplier_id')
      AND attempt.model = sqlc.arg('model')
      AND attempt.observed_at >= sqlc.arg('window_start')
      AND attempt.observed_at < sqlc.arg('window_end')
      AND (
          attempt.outcome = 'succeeded'
          OR attempt.error_responsibility = 'supplier'
      )
), ordered AS (
    SELECT
        id,
        succeeded,
        event_at,
        MIN(event_at) FILTER (WHERE succeeded) OVER (
            ORDER BY event_at, request_measurement_id, attempt_index, id
            ROWS BETWEEN 1 FOLLOWING AND UNBOUNDED FOLLOWING
        ) AS recovered_at
    FROM attempt_events
), failure_episodes AS (
    SELECT recovered_at, MIN(event_at) AS failure_at
    FROM ordered
    WHERE NOT succeeded
    GROUP BY recovered_at
), recovery_durations AS (
    SELECT ROUND(EXTRACT(EPOCH FROM (
        (CASE
            WHEN recovered_at IS NULL THEN sqlc.arg('window_end')
            ELSE recovered_at
        END) - failure_at
    )) * 1000) AS recovery_millis
    FROM failure_episodes
    WHERE failure_at < sqlc.arg('window_end')
      AND (recovered_at IS NULL OR recovered_at > sqlc.arg('window_start'))
)
SELECT COALESCE(
    MAX(recovery_millis),
    0
)::bigint AS recovery_millis
FROM recovery_durations;

-- name: GetAttemptLatencyHistogram :many
SELECT upper_bound_ms, COALESCE(SUM(sample_count), 0)::bigint AS sample_count
FROM attempt_latency_histograms_1m
WHERE site_id = $1 AND supplier_id = $2 AND model = $3 AND source = $4
  AND metric = $5 AND bucket_start >= $6 AND bucket_start < $7
GROUP BY upper_bound_ms
ORDER BY upper_bound_ms;

-- name: GetFailureStreak :one
WITH request_outcomes AS (
    SELECT
        request.id,
        BOOL_OR(attempt.outcome = 'succeeded') AS succeeded,
        MAX(attempt.observed_at) FILTER (WHERE attempt.outcome = 'succeeded') AS succeeded_at,
        MAX(attempt.observed_at) FILTER (
            WHERE attempt.outcome <> 'succeeded'
              AND attempt.error_responsibility = 'supplier'
        ) AS failed_at,
        BOOL_OR(
            attempt.outcome <> 'succeeded'
            AND attempt.error_category = 'authentication'
            AND attempt.error_responsibility = 'supplier'
        ) AS authentication,
        BOOL_OR(
            attempt.outcome <> 'succeeded'
            AND attempt.error_category = 'balance_exhausted'
            AND attempt.error_responsibility = 'supplier'
        ) AS balance
    FROM measurement_attempts attempt
    JOIN measurement_requests request ON request.id = attempt.request_measurement_id
    WHERE request.site_id = sqlc.arg('site_id')
      AND request.is_current
      AND request.source = 'real_traffic'
      AND attempt.supplier_id = sqlc.arg('supplier_id')
      AND attempt.model = sqlc.arg('model')
      AND attempt.observed_at < sqlc.arg('before_time')
    GROUP BY request.id
    HAVING BOOL_OR(attempt.outcome = 'succeeded')
        OR BOOL_OR(
            attempt.outcome <> 'succeeded'
            AND attempt.error_responsibility = 'supplier'
        )
), last_success AS (
    SELECT MAX(candidate.succeeded_at) AS succeeded_at
    FROM (
        SELECT MAX(succeeded_at) AS succeeded_at
        FROM request_outcomes
        WHERE succeeded

        UNION ALL

        SELECT MAX(attempt.observed_at) AS succeeded_at
        FROM measurement_attempts attempt
        JOIN measurement_requests request ON request.id = attempt.request_measurement_id
        WHERE request.site_id IS NULL
          AND request.is_current
          AND request.source = 'direct_probe'
          AND attempt.supplier_id = sqlc.arg('supplier_id')
          AND attempt.model = sqlc.arg('model')
          AND attempt.outcome = 'succeeded'
          AND attempt.observed_at < sqlc.arg('before_time')
    ) candidate
)
SELECT
    COUNT(*) FILTER (
        WHERE NOT outcome.succeeded
          AND outcome.failed_at > COALESCE(success.succeeded_at, '-infinity'::timestamptz)
    )::bigint AS total,
    COUNT(*) FILTER (
        WHERE NOT outcome.succeeded
          AND outcome.authentication
          AND outcome.failed_at > COALESCE(success.succeeded_at, '-infinity'::timestamptz)
    )::bigint AS authentication,
    COUNT(*) FILTER (
        WHERE NOT outcome.succeeded
          AND outcome.balance
          AND outcome.failed_at > COALESCE(success.succeeded_at, '-infinity'::timestamptz)
    )::bigint AS balance
FROM request_outcomes outcome
CROSS JOIN last_success success;

-- name: CountPendingAttribution :one
SELECT COUNT(*)::bigint
FROM measurement_attempts attempt
JOIN measurement_requests request ON request.id = attempt.request_measurement_id
WHERE request.site_id = $1
  AND request.is_current
  AND attempt.model = $2
  AND request.source = 'real_traffic'
  AND (
      attempt.attribution_status = 'pending'
      OR (attempt.outcome <> 'succeeded' AND attempt.error_responsibility = 'unknown')
  )
  AND attempt.observed_at >= $3 AND attempt.observed_at < $4;

-- name: ListScoringTargets :many
SELECT
    relation.site_id,
    relation.id AS relation_id,
    relation.supplier_id,
    supplier.name AS supplier_name,
    model.model,
    model.input_price,
    model.output_price,
    model.currency,
    relation.desired_status,
    relation.sync_status,
    COALESCE(
        ARRAY_AGG(DISTINCT strategy.kind) FILTER (
            WHERE member.relation_id IS NOT NULL AND strategy.enabled
        ),
        '{}'
    )::text[] AS current_strategies
FROM site_suppliers relation
JOIN sites site ON site.id = relation.site_id
JOIN suppliers supplier ON supplier.id = relation.supplier_id
JOIN supplier_models model ON model.supplier_id = supplier.id
LEFT JOIN strategy_members member ON member.relation_id = relation.id
LEFT JOIN site_strategies strategy ON strategy.id = member.strategy_id AND strategy.site_id = relation.site_id
WHERE site.status = 'enabled'
  AND supplier.status = 'enabled'
  AND model.enabled
  AND relation.desired_status = 'enabled'
  AND relation.sync_status = 'active'
GROUP BY relation.site_id, relation.id, relation.supplier_id, supplier.name,
         model.model, model.input_price, model.output_price, model.currency,
         relation.desired_status, relation.sync_status
ORDER BY relation.site_id, model.model, supplier.name;

-- name: GetLowestPeerCost :one
SELECT COALESCE(MIN((model.input_price + model.output_price) / 2), 0)::numeric(20, 10)
FROM site_suppliers relation
JOIN sites site ON site.id = relation.site_id
JOIN suppliers supplier ON supplier.id = relation.supplier_id
JOIN supplier_models model ON model.supplier_id = supplier.id
WHERE relation.site_id = $1
  AND model.model = $2
  AND model.currency = $3
  AND site.status = 'enabled'
  AND supplier.status = 'enabled'
  AND model.enabled
  AND relation.desired_status = 'enabled'
  AND relation.sync_status = 'active';

-- name: ListSupplierModelPriceHistory :many
SELECT *
FROM supplier_model_price_history
WHERE supplier_id = sqlc.arg('supplier_id')
  AND model = sqlc.arg('model')
  AND valid_from <= sqlc.arg('window_end')
  AND (valid_to IS NULL OR valid_to >= sqlc.arg('window_start'))
ORDER BY valid_from, version;

-- name: CreateScoreSnapshot :exec
INSERT INTO score_snapshots (
    id, site_id, supplier_id, model, policy_version, window_start, window_end,
    facts_through, passive_samples, active_samples, price_score, latency_score,
    sla_score, quality_score, total_score, confidence, eligibility,
    hard_reasons, explanation, authenticity_assessment_id,
    capability_assessment_id, score_run_id, created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19, $20,
    $21, $22, $23
)
ON CONFLICT (site_id, supplier_id, model, policy_version, window_end) DO NOTHING;

-- name: CreateShadowRecommendation :exec
INSERT INTO shadow_recommendations (
    id, score_snapshot_id, site_id, supplier_id, model, strategy_kind,
    action, current_member, score, confidence, reasons, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (score_snapshot_id, strategy_kind) DO NOTHING;

-- name: ListLatestScoreSnapshots :many
SELECT
    snapshot.*,
    supplier.name AS supplier_name,
    COALESCE(authenticity.verdict, 'insufficient')::text AS authenticity_verdict,
    COUNT(*) OVER()::bigint AS total_count
FROM score_snapshots snapshot
JOIN suppliers supplier ON supplier.id = snapshot.supplier_id
LEFT JOIN authenticity_assessments authenticity ON authenticity.id = snapshot.authenticity_assessment_id
WHERE snapshot.id IN (
    SELECT DISTINCT ON (site_id, supplier_id, model) id
    FROM score_snapshots
    ORDER BY site_id, supplier_id, model, created_at DESC, id DESC
)
  AND (sqlc.narg('site_id')::uuid IS NULL OR snapshot.site_id = sqlc.narg('site_id')::uuid)
  AND (sqlc.narg('supplier_id')::uuid IS NULL OR snapshot.supplier_id = sqlc.narg('supplier_id')::uuid)
  AND (sqlc.narg('model')::text IS NULL OR snapshot.model = sqlc.narg('model')::text)
ORDER BY snapshot.created_at DESC, snapshot.id DESC
LIMIT sqlc.arg('page_limit') OFFSET sqlc.arg('page_offset');

-- name: ListShadowRecommendationsForSnapshot :many
SELECT *
FROM shadow_recommendations
WHERE score_snapshot_id = $1
ORDER BY strategy_kind;

-- name: FindPreviousShadowRecommendation :one
SELECT recommendation.*
FROM shadow_recommendations recommendation
JOIN score_snapshots snapshot ON snapshot.id = recommendation.score_snapshot_id
WHERE recommendation.site_id = sqlc.arg('site_id')
  AND recommendation.supplier_id = sqlc.arg('supplier_id')
  AND recommendation.model = sqlc.arg('model')
  AND recommendation.strategy_kind = sqlc.arg('strategy_kind')
  AND recommendation.created_at < sqlc.arg('created_at')
  AND snapshot.policy_version = sqlc.arg('policy_version')
ORDER BY recommendation.created_at DESC, recommendation.id DESC
LIMIT 1;

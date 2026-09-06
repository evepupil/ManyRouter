package postgres

import (
	"context"
	"errors"
	"time"

	domaincatalog "github.com/evepupil/ManyRouter/internal/domain/catalog"
	operationsdomain "github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/evepupil/ManyRouter/internal/domain/routing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) LoadCatalogSource(ctx context.Context, siteID uuid.UUID) (domaincatalog.BuildInput, error) {
	var siteName string
	var routePlanID uuid.UUID
	var snapshotJSON []byte
	err := store.pool.QueryRow(ctx, `
		SELECT site.name,plan.id,plan.snapshot
		FROM sites site
		JOIN LATERAL (
			SELECT id,snapshot
			FROM route_plan_versions
			WHERE site_id=site.id AND status='confirmed'
			ORDER BY version DESC
			LIMIT 1
		) plan ON true
		WHERE site.id=$1 AND site.status='enabled'
	`, siteID).Scan(&siteName, &routePlanID, &snapshotJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return domaincatalog.BuildInput{}, operationsdomain.ErrNotFound
	}
	if err != nil {
		return domaincatalog.BuildInput{}, err
	}
	plan, err := routing.DecodeSnapshot(snapshotJSON)
	if err != nil {
		return domaincatalog.BuildInput{}, err
	}
	input := domaincatalog.BuildInput{
		SiteID: siteID, SiteName: siteName, RoutePlanID: routePlanID, Plan: plan,
		StrategyKinds: make(map[string]string), Prices: make(map[string]domaincatalog.PriceEvidence),
		Metrics:   make(map[domaincatalog.MetricKey]domaincatalog.MetricEvidence),
		Qualities: make(map[domaincatalog.QualityKey]domaincatalog.QualityEvidence),
	}
	strategyRows, err := store.pool.Query(ctx, `SELECT group_key,kind FROM site_strategies WHERE site_id=$1`, siteID)
	if err != nil {
		return domaincatalog.BuildInput{}, err
	}
	for strategyRows.Next() {
		var groupKey, kind string
		if err := strategyRows.Scan(&groupKey, &kind); err != nil {
			strategyRows.Close()
			return domaincatalog.BuildInput{}, err
		}
		input.StrategyKinds[groupKey] = kind
	}
	if err := strategyRows.Err(); err != nil {
		strategyRows.Close()
		return domaincatalog.BuildInput{}, err
	}
	strategyRows.Close()
	var scoreRunID uuid.UUID
	err = store.pool.QueryRow(ctx, `
		SELECT id FROM score_runs
		WHERE site_id=$1 AND status='succeeded'
		ORDER BY window_end DESC,id DESC LIMIT 1
	`, siteID).Scan(&scoreRunID)
	if err == nil {
		input.ScoreRunID = &scoreRunID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domaincatalog.BuildInput{}, err
	}
	if len(plan.PriceVersionIDs) > 0 {
		priceRows, err := store.pool.Query(ctx, `
			SELECT id,group_key,applied_at
			FROM price_versions
			WHERE site_id=$1 AND id=ANY($2::uuid[]) AND status='applied' AND applied_at IS NOT NULL
		`, siteID, plan.PriceVersionIDs)
		if err != nil {
			return domaincatalog.BuildInput{}, err
		}
		for priceRows.Next() {
			var id uuid.UUID
			var groupKey string
			var confirmedAt time.Time
			if err := priceRows.Scan(&id, &groupKey, &confirmedAt); err != nil {
				priceRows.Close()
				return domaincatalog.BuildInput{}, err
			}
			input.Prices[groupKey] = domaincatalog.PriceEvidence{VersionID: id, ConfirmedAt: confirmedAt.UTC()}
		}
		if err := priceRows.Err(); err != nil {
			priceRows.Close()
			return domaincatalog.BuildInput{}, err
		}
		priceRows.Close()
	}
	metricRows, err := store.pool.Query(ctx, `
		SELECT request_group,model,
		       count(*)::bigint,
		       count(*) FILTER (WHERE outcome='succeeded')::bigint,
		       percentile_disc(0.5) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL)::bigint,
		       percentile_disc(0.95) WITHIN GROUP (ORDER BY ttft_ms) FILTER (WHERE ttft_ms IS NOT NULL)::bigint,
		       max(observed_at)
		FROM measurement_requests
		WHERE site_id=$1 AND source='real_traffic' AND is_current
		  AND observed_at>=now()-INTERVAL '24 hours'
		GROUP BY request_group,model
	`, siteID)
	if err != nil {
		return domaincatalog.BuildInput{}, err
	}
	for metricRows.Next() {
		var groupKey, model string
		var metric domaincatalog.MetricEvidence
		var p50, p95 pgtype.Int8
		if err := metricRows.Scan(
			&groupKey, &model, &metric.RequestCount, &metric.SuccessCount,
			&p50, &p95, &metric.FactsThrough,
		); err != nil {
			metricRows.Close()
			return domaincatalog.BuildInput{}, err
		}
		metric.FactsThrough = metric.FactsThrough.UTC()
		if p50.Valid {
			value := p50.Int64
			metric.TTFTP50Millis = &value
		}
		if p95.Valid {
			value := p95.Int64
			metric.TTFTP95Millis = &value
		}
		input.Metrics[domaincatalog.MetricKey{Group: groupKey, Model: model}] = metric
	}
	if err := metricRows.Err(); err != nil {
		metricRows.Close()
		return domaincatalog.BuildInput{}, err
	}
	metricRows.Close()
	if input.ScoreRunID != nil {
		qualityRows, err := store.pool.Query(ctx, `
			SELECT relation.id,snapshot.model,snapshot.quality_score,snapshot.confidence,
			       COALESCE(authenticity.verdict,'insufficient')
			FROM score_snapshots snapshot
			JOIN site_suppliers relation ON relation.site_id=snapshot.site_id AND relation.supplier_id=snapshot.supplier_id
			LEFT JOIN authenticity_assessments authenticity ON authenticity.id=snapshot.authenticity_assessment_id
			WHERE snapshot.score_run_id=$1
		`, *input.ScoreRunID)
		if err != nil {
			return domaincatalog.BuildInput{}, err
		}
		for qualityRows.Next() {
			var relationID uuid.UUID
			var model, confidence, authenticity string
			var score pgtype.Numeric
			if err := qualityRows.Scan(&relationID, &model, &score, &confidence, &authenticity); err != nil {
				qualityRows.Close()
				return domaincatalog.BuildInput{}, err
			}
			value, valid, err := numericFloat(score)
			if err != nil {
				qualityRows.Close()
				return domaincatalog.BuildInput{}, err
			}
			var pointer *float64
			if valid {
				pointer = &value
			}
			input.Qualities[domaincatalog.QualityKey{RelationID: relationID, Model: model}] = domaincatalog.QualityEvidence{
				Score: pointer, Confidence: confidence, Authenticity: authenticity,
			}
		}
		if err := qualityRows.Err(); err != nil {
			qualityRows.Close()
			return domaincatalog.BuildInput{}, err
		}
		qualityRows.Close()
	}
	return input, nil
}

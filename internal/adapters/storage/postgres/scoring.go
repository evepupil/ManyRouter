package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/storage/postgres/sqlc"
	evaluationapp "github.com/evepupil/ManyRouter/internal/application/evaluation"
	scoringapp "github.com/evepupil/ManyRouter/internal/application/scoring"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	"github.com/evepupil/ManyRouter/internal/domain/measurement"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

func (store *Store) RefreshMinuteMetrics(
	ctx context.Context,
	historyStart time.Time,
	recentStart time.Time,
	end time.Time,
	computedAt time.Time,
) error {
	if historyStart.IsZero() || recentStart.IsZero() || end.IsZero() || computedAt.IsZero() ||
		!historyStart.Before(end) || recentStart.Before(historyStart) || !recentStart.Before(end) {
		return errors.New("metric refresh window is invalid")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	state, err := queries.LockScoringAggregationState(ctx)
	if err != nil {
		return err
	}
	refreshStart := recentStart.UTC()
	if !state.InitializedAt.Valid || !state.FactsThrough.Valid {
		refreshStart = historyStart.UTC()
	} else {
		overlapStart := state.FactsThrough.Time.UTC().Add(-10 * time.Minute)
		if overlapStart.Before(refreshStart) {
			refreshStart = overlapStart
		}
		if refreshStart.Before(historyStart) {
			refreshStart = historyStart.UTC()
		}
	}
	if !refreshStart.Before(end) {
		refreshStart = recentStart.UTC()
	}
	if err := refreshMinuteMetricsRange(ctx, queries, refreshStart, end.UTC(), computedAt.UTC()); err != nil {
		return err
	}
	updated, err := queries.UpdateScoringAggregationState(ctx, sqlc.UpdateScoringAggregationStateParams{
		InitializedAt: databaseTime(computedAt), FactsThrough: databaseTime(end), UpdatedAt: databaseTime(computedAt),
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return errors.New("scoring aggregation state is unavailable")
	}
	return tx.Commit(ctx)
}

func refreshMinuteMetricsRange(
	ctx context.Context,
	queries *sqlc.Queries,
	start time.Time,
	end time.Time,
	computedAt time.Time,
) error {
	windowStart, windowEnd := databaseTime(start), databaseTime(end)
	if err := queries.DeleteAttemptHistogramsRange(ctx, sqlc.DeleteAttemptHistogramsRangeParams{BucketStart: windowStart, BucketStart_2: windowEnd}); err != nil {
		return err
	}
	if err := queries.DeleteRequestHistogramsRange(ctx, sqlc.DeleteRequestHistogramsRangeParams{BucketStart: windowStart, BucketStart_2: windowEnd}); err != nil {
		return err
	}
	if err := queries.DeleteAttemptMetricsRange(ctx, sqlc.DeleteAttemptMetricsRangeParams{BucketStart: windowStart, BucketStart_2: windowEnd}); err != nil {
		return err
	}
	if err := queries.DeleteRequestMetricsRange(ctx, sqlc.DeleteRequestMetricsRangeParams{BucketStart: windowStart, BucketStart_2: windowEnd}); err != nil {
		return err
	}
	if err := queries.AggregateRequestMetricsRange(ctx, sqlc.AggregateRequestMetricsRangeParams{
		ObservedAt: windowStart, ObservedAt_2: windowEnd, ComputedAt: databaseTime(computedAt),
	}); err != nil {
		return err
	}
	if err := queries.AggregateAttemptMetricsRange(ctx, sqlc.AggregateAttemptMetricsRangeParams{
		ObservedAt: windowStart, ObservedAt_2: windowEnd, ComputedAt: databaseTime(computedAt),
	}); err != nil {
		return err
	}
	if err := queries.AggregateRequestHistogramsRange(ctx, sqlc.AggregateRequestHistogramsRangeParams{ObservedAt: windowStart, ObservedAt_2: windowEnd}); err != nil {
		return err
	}
	if err := queries.AggregateAttemptHistogramsRange(ctx, sqlc.AggregateAttemptHistogramsRangeParams{ObservedAt: windowStart, ObservedAt_2: windowEnd}); err != nil {
		return err
	}
	return nil
}

func (store *Store) ListScoringTargets(ctx context.Context) ([]scoringapp.Target, error) {
	rows, err := store.queries.ListScoringTargets(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]scoringapp.Target, 0, len(rows))
	for _, row := range rows {
		strategies := make([]domainscoring.AutoKind, 0, len(row.CurrentStrategies))
		for _, strategy := range row.CurrentStrategies {
			strategies = append(strategies, domainscoring.AutoKind(strategy))
		}
		result = append(result, scoringapp.Target{
			SiteID: row.SiteID, RelationID: row.RelationID, SupplierID: row.SupplierID,
			SupplierName: row.SupplierName, Model: row.Model, InputPrice: row.InputPrice,
			OutputPrice: row.OutputPrice, Currency: row.Currency, DesiredStatus: row.DesiredStatus,
			SyncStatus:        row.SyncStatus,
			CurrentStrategies: strategies,
		})
	}
	return result, nil
}

func (store *Store) GetLowestPeerCost(ctx context.Context, siteID uuid.UUID, model, currency string) (decimal.Decimal, error) {
	return store.queries.GetLowestPeerCost(ctx, sqlc.GetLowestPeerCostParams{SiteID: siteID, Model: model, Currency: currency})
}

func (store *Store) GetPriceEvidence(
	ctx context.Context,
	target scoringapp.Target,
	start time.Time,
	end time.Time,
) (scoringapp.PriceEvidence, error) {
	if target.SupplierID == uuid.Nil || target.Model == "" || target.Currency == "" || !start.Before(end) {
		return scoringapp.PriceEvidence{}, errors.New("price evidence window is invalid")
	}
	rows, err := store.queries.ListSupplierModelPriceHistory(ctx, sqlc.ListSupplierModelPriceHistoryParams{
		SupplierID: target.SupplierID, Model: target.Model,
		WindowStart: databaseTime(start), WindowEnd: databaseTime(end),
	})
	if err != nil {
		return scoringapp.PriceEvidence{}, err
	}
	return summarizePriceEvidence(rows, target, start.UTC(), end.UTC())
}

func summarizePriceEvidence(
	rows []sqlc.SupplierModelPriceHistory,
	target scoringapp.Target,
	start time.Time,
	end time.Time,
) (scoringapp.PriceEvidence, error) {
	if len(rows) == 0 {
		return scoringapp.PriceEvidence{}, nil
	}
	cursor := start
	currentMatches := false
	changes := uint64(0)
	maximumMagnitude := decimal.Zero
	var previous *sqlc.SupplierModelPriceHistory
	for index := range rows {
		row := &rows[index]
		if !row.ValidFrom.Valid || row.ValidFrom.InfinityModifier != pgtype.Finite ||
			row.ValidTo.Valid && row.ValidTo.InfinityModifier != pgtype.Finite {
			return scoringapp.PriceEvidence{}, errors.New("stored price history time is invalid")
		}
		validFrom := row.ValidFrom.Time.UTC()
		var validTo *time.Time
		if row.ValidTo.Valid {
			value := row.ValidTo.Time.UTC()
			validTo = &value
		}
		if previous != nil && !validFrom.Before(start) && validFrom.Before(end) && priceVersionChanged(*previous, *row) {
			changes++
			magnitude := priceChangeMagnitude(*previous, *row)
			if magnitude.GreaterThan(maximumMagnitude) {
				maximumMagnitude = magnitude
			}
		}
		if row.Currency != target.Currency {
			return scoringapp.PriceEvidence{}, nil
		}
		segmentStart := validFrom
		if segmentStart.Before(start) {
			segmentStart = start
		}
		segmentEnd := end
		if validTo != nil && validTo.Before(segmentEnd) {
			segmentEnd = *validTo
		}
		if segmentEnd.After(segmentStart) {
			if segmentStart.After(cursor) {
				return scoringapp.PriceEvidence{}, nil
			}
			if segmentEnd.After(cursor) {
				cursor = segmentEnd
			}
		}
		if !validFrom.After(end) && (validTo == nil || validTo.After(end)) {
			currentMatches = row.InputPrice.Equal(target.InputPrice) &&
				row.OutputPrice.Equal(target.OutputPrice) && row.Currency == target.Currency
		}
		previous = row
	}
	if cursor.Before(end) || !currentMatches {
		return scoringapp.PriceEvidence{}, nil
	}
	return scoringapp.PriceEvidence{
		Available: true, ChangesPerDay: changes, ChangeMagnitudeRatio: maximumMagnitude.InexactFloat64(),
	}, nil
}

func priceVersionChanged(left, right sqlc.SupplierModelPriceHistory) bool {
	return !left.InputPrice.Equal(right.InputPrice) || !left.OutputPrice.Equal(right.OutputPrice) || left.Currency != right.Currency
}

func priceChangeMagnitude(left, right sqlc.SupplierModelPriceHistory) decimal.Decimal {
	leftAverage := left.InputPrice.Add(left.OutputPrice).Div(decimal.NewFromInt(2)).Abs()
	rightAverage := right.InputPrice.Add(right.OutputPrice).Div(decimal.NewFromInt(2)).Abs()
	denominator := decimal.Max(leftAverage, rightAverage)
	if denominator.IsZero() {
		return decimal.Zero
	}
	return rightAverage.Sub(leftAverage).Abs().Div(denominator)
}

func (store *Store) GetWindowMetrics(ctx context.Context, target scoringapp.Target, start, end time.Time) (scoringapp.WindowMetrics, error) {
	row, err := store.queries.GetAttemptWindowMetrics(ctx, sqlc.GetAttemptWindowMetricsParams{
		SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model,
		Source: string(measurement.SourceRealTraffic), BucketStart: databaseTime(start), BucketStart_2: databaseTime(end),
	})
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	ttft, err := store.loadAttemptHistogram(ctx, target, "ttft", start, end)
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	successDuration, err := store.loadAttemptHistogram(ctx, target, "duration_success", start, end)
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	failureDuration, err := store.loadAttemptHistogram(ctx, target, "duration_failure", start, end)
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	pending, err := store.queries.CountPendingAttribution(ctx, sqlc.CountPendingAttributionParams{
		SiteID: databaseUUID(target.SiteID), Model: target.Model,
		ObservedAt: databaseTime(start), ObservedAt_2: databaseTime(end),
	})
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	recoveryMillis, err := store.queries.GetWindowRecoveryMillis(ctx, sqlc.GetWindowRecoveryMillisParams{
		SiteID: databaseUUID(target.SiteID), SupplierID: databaseUUID(target.SupplierID), Model: target.Model,
		WindowStart: databaseTime(start), WindowEnd: databaseTime(end),
	})
	if err != nil {
		return scoringapp.WindowMetrics{}, err
	}
	metrics := scoringapp.WindowMetrics{
		AttemptCount: nonnegative(row.AttemptCount), SLAAttemptCount: nonnegative(row.SlaAttemptCount),
		SuccessCount: nonnegative(row.SuccessCount), FailureCount: nonnegative(row.FailureCount),
		SLAFailureCount: nonnegative(row.SlaFailureCount), RateLimitedCount: nonnegative(row.RateLimitedCount),
		AuthenticationCount: nonnegative(row.AuthenticationCount), BalanceCount: nonnegative(row.BalanceCount),
		TimeoutCount: nonnegative(row.TimeoutCount), TransportCount: nonnegative(row.TransportCount),
		UpstreamCount: nonnegative(row.UpstreamCount), StreamCount: nonnegative(row.StreamCount),
		StreamCompletedCount: nonnegative(row.StreamCompletedCount), TTFTCount: nonnegative(row.TtftCount),
		SuccessDurationCount: nonnegative(row.SuccessDurationCount), FailureDurationCount: nonnegative(row.FailureDurationCount),
		CoarseDurationCount: nonnegative(row.CoarseDurationCount),
		TTFT:                ttft, SuccessDuration: successDuration, FailureDuration: failureDuration,
		RecoveryMillis: nonnegative(recoveryMillis), PendingAttribution: pending > 0,
	}
	if row.FactsThrough.Valid && row.FactsThrough.InfinityModifier == pgtype.Finite {
		metrics.FactsThrough = row.FactsThrough.Time.UTC()
	}
	return metrics, nil
}

func (store *Store) loadAttemptHistogram(ctx context.Context, target scoringapp.Target, metric string, start, end time.Time) (domainscoring.LatencyHistogram, error) {
	rows, err := store.queries.GetAttemptLatencyHistogram(ctx, sqlc.GetAttemptLatencyHistogramParams{
		SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model,
		Source: string(measurement.SourceRealTraffic), Metric: metric,
		BucketStart: databaseTime(start), BucketStart_2: databaseTime(end),
	})
	if err != nil {
		return domainscoring.LatencyHistogram{}, err
	}
	buckets := domainscoring.LatencyBuckets()
	counts := make([]uint64, len(buckets))
	for _, row := range rows {
		matched := false
		for index, bucket := range buckets {
			bound := bucket.UpperBoundMillis
			if bucket.Infinite {
				bound = int64(^uint64(0) >> 1)
			}
			if row.UpperBoundMs == bound {
				counts[index] = nonnegative(row.SampleCount)
				matched = true
				break
			}
		}
		if !matched {
			return domainscoring.LatencyHistogram{}, errors.New("stored latency bucket is unsupported")
		}
	}
	return domainscoring.NewLatencyHistogram(counts)
}

func (store *Store) GetCollectionEvidence(ctx context.Context, siteID uuid.UUID) (scoringapp.CollectionEvidence, error) {
	row, err := store.queries.GetCollectionCursor(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return scoringapp.CollectionEvidence{DataGap: true}, nil
	}
	if err != nil {
		return scoringapp.CollectionEvidence{}, err
	}
	return scoringapp.CollectionEvidence{
		LastSuccessAt: timeValue(row.LastSuccessAt), SourceLatest: unixTimeValue(row.SourceLatestCreatedAt), DataGap: row.DataGap,
	}, nil
}

func (store *Store) GetEvaluationEvidence(ctx context.Context, supplierID uuid.UUID, model string, validAt time.Time) (scoringapp.EvaluationEvidence, error) {
	result := scoringapp.EvaluationEvidence{}
	authenticity, err := store.queries.FindLatestAuthenticityAssessment(ctx, sqlc.FindLatestAuthenticityAssessmentParams{
		SupplierID: supplierID, Model: model, ValidAt: databaseTime(validAt),
	})
	if err == nil {
		result.AuthenticityID = &authenticity.ID
		result.Authenticity = domainevaluation.Verdict(authenticity.Verdict)
		result.AuthenticityConfidence = authenticity.Confidence.InexactFloat64()
		result.AuthenticityCheckedAt = timeValue(authenticity.CheckedAt)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return scoringapp.EvaluationEvidence{}, err
	}
	quality, err := store.queries.FindLatestCapabilityAssessmentBySuite(ctx, sqlc.FindLatestCapabilityAssessmentBySuiteParams{
		SupplierID: supplierID, Model: model, SuiteVersion: evaluationapp.CapabilitySuiteVersion, ValidAt: databaseTime(validAt),
	})
	if err == nil {
		result.CapabilityID = &quality.ID
		result.CapabilityScore = quality.Score.InexactFloat64()
		result.CapabilityConfidence = quality.Confidence.InexactFloat64()
		result.CapabilityCheckedAt = timeValue(quality.CheckedAt)
		result.CapabilityChecks = int(quality.CompletedChecks)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return scoringapp.EvaluationEvidence{}, err
	}
	health, err := store.queries.FindLatestCapabilityAssessmentBySuite(ctx, sqlc.FindLatestCapabilityAssessmentBySuiteParams{
		SupplierID: supplierID, Model: model, SuiteVersion: evaluationapp.HealthSuiteVersion, ValidAt: databaseTime(validAt),
	})
	if err == nil {
		result.HealthScore = health.Score.InexactFloat64()
		result.HealthConfidence = health.Confidence.InexactFloat64()
		result.HealthCheckedAt = timeValue(health.CheckedAt)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return scoringapp.EvaluationEvidence{}, err
	}
	return result, nil
}

func (store *Store) GetFailureStreak(ctx context.Context, target scoringapp.Target, before time.Time) (scoringapp.FailureStreak, error) {
	row, err := store.queries.GetFailureStreak(ctx, sqlc.GetFailureStreakParams{
		SiteID: databaseUUID(target.SiteID), SupplierID: databaseUUID(target.SupplierID), Model: target.Model, BeforeTime: databaseTime(before),
	})
	if err != nil {
		return scoringapp.FailureStreak{}, err
	}
	return scoringapp.FailureStreak{
		Total: nonnegative(row.Total), Authentication: nonnegative(row.Authentication), Balance: nonnegative(row.Balance),
	}, nil
}

func (store *Store) FindPreviousRecommendation(
	ctx context.Context,
	target scoringapp.Target,
	kind domainscoring.AutoKind,
	before time.Time,
) (*scoringapp.PreviousRecommendation, error) {
	row, err := store.queries.FindPreviousShadowRecommendation(ctx, sqlc.FindPreviousShadowRecommendationParams{
		SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model,
		StrategyKind: string(kind), CreatedAt: databaseTime(before), PolicyVersion: domainscoring.PolicyVersionM2ShadowV1,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	score, valid, err := numericFloat(row.Score)
	if err != nil {
		return nil, err
	}
	if valid && (score < 0 || score > 100) {
		return nil, errors.New("stored recommendation score is invalid")
	}
	var scorePointer *domainscoring.Score
	if valid {
		value := domainscoring.Score(score)
		scorePointer = &value
	}
	confidence := domainscoring.Confidence(row.Confidence)
	switch confidence {
	case domainscoring.ConfidenceHigh, domainscoring.ConfidenceMedium,
		domainscoring.ConfidenceLow, domainscoring.ConfidenceInsufficient:
	default:
		return nil, errors.New("stored recommendation confidence is invalid")
	}
	if !row.CreatedAt.Valid || row.CreatedAt.InfinityModifier != pgtype.Finite {
		return nil, errors.New("stored recommendation time is invalid")
	}
	return &scoringapp.PreviousRecommendation{
		Score: scorePointer, CreatedAt: row.CreatedAt.Time.UTC(), Confidence: confidence,
	}, nil
}

func (store *Store) SaveScoreSnapshot(ctx context.Context, snapshot scoringapp.Snapshot) error {
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := store.queries.WithTx(tx)
	snapshotID := scoreSnapshotID(snapshot)
	hardReasons, err := json.Marshal(snapshot.HardReasons)
	if err != nil {
		return err
	}
	explanation, err := json.Marshal(snapshot.Explanation)
	if err != nil {
		return err
	}
	var priceScore, latencyScore, slaScore, qualityScore pgtype.Numeric
	if snapshot.Scores != nil {
		priceScore = numericValue(snapshot.Scores.Price.Float64())
		latencyScore = numericValue(snapshot.Scores.Latency.Float64())
		slaScore = numericValue(snapshot.Scores.SLA.Float64())
		qualityScore = numericValue(snapshot.Scores.Quality.Float64())
	}
	if err := queries.CreateScoreSnapshot(ctx, sqlc.CreateScoreSnapshotParams{
		ID: snapshotID, SiteID: snapshot.Target.SiteID, SupplierID: snapshot.Target.SupplierID,
		Model: snapshot.Target.Model, PolicyVersion: domainscoring.PolicyVersionM2ShadowV1,
		WindowStart: databaseTime(snapshot.WindowStart), WindowEnd: databaseTime(snapshot.WindowEnd),
		FactsThrough: optionalDatabaseTime(snapshot.FactsThrough), PassiveSamples: int64(snapshot.PassiveSamples),
		ActiveSamples: int64(snapshot.ActiveSamples), PriceScore: priceScore, LatencyScore: latencyScore,
		SlaScore: slaScore, QualityScore: qualityScore,
		TotalScore: optionalDomainScore(snapshot.BalancedScore), Confidence: string(snapshot.Confidence),
		Eligibility: snapshot.Eligibility, HardReasons: hardReasons, Explanation: explanation,
		AuthenticityAssessmentID: databaseUUIDPointer(snapshot.AuthenticityAssessmentID),
		CapabilityAssessmentID:   databaseUUIDPointer(snapshot.CapabilityAssessmentID),
		CreatedAt:                databaseTime(snapshot.CreatedAt),
	}); err != nil {
		return err
	}
	for _, recommendation := range snapshot.Recommendations {
		reasons, err := json.Marshal(recommendation.Reasons)
		if err != nil {
			return err
		}
		if err := queries.CreateShadowRecommendation(ctx, sqlc.CreateShadowRecommendationParams{
			ID:              uuid.NewSHA1(uuid.NameSpaceOID, []byte("recommendation|"+snapshotID.String()+"|"+string(recommendation.AutoKind))),
			ScoreSnapshotID: snapshotID, SiteID: snapshot.Target.SiteID, SupplierID: snapshot.Target.SupplierID,
			Model: snapshot.Target.Model, StrategyKind: string(recommendation.AutoKind), Action: string(recommendation.Action),
			CurrentMember: recommendation.CurrentMember, Score: optionalDomainScore(recommendation.CompositeScore),
			Confidence: string(recommendation.Confidence), Reasons: reasons, CreatedAt: databaseTime(snapshot.CreatedAt),
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (store *Store) ListInsights(ctx context.Context, filter scoringapp.InsightFilter) (scoringapp.InsightPage, error) {
	rows, err := store.queries.ListLatestScoreSnapshots(ctx, sqlc.ListLatestScoreSnapshotsParams{
		SiteID: databaseUUIDPointer(filter.SiteID), SupplierID: databaseUUIDPointer(filter.SupplierID),
		Model: optionalText(filter.Model), PageLimit: int32(filter.Limit), PageOffset: int32(filter.Offset),
	})
	if err != nil {
		return scoringapp.InsightPage{}, err
	}
	page := scoringapp.InsightPage{Items: make([]scoringapp.Insight, 0, len(rows)), Limit: filter.Limit, Offset: filter.Offset}
	for _, row := range rows {
		if !row.CreatedAt.Valid || !row.WindowStart.Valid || !row.WindowEnd.Valid ||
			!row.WindowStart.Time.Before(row.WindowEnd.Time) {
			return scoringapp.InsightPage{}, errors.New("score snapshot time is invalid")
		}
		var hardReasons []string
		if err := json.Unmarshal(row.HardReasons, &hardReasons); err != nil {
			return scoringapp.InsightPage{}, err
		}
		var explanation map[string]any
		if err := json.Unmarshal(row.Explanation, &explanation); err != nil {
			return scoringapp.InsightPage{}, err
		}
		if explanation == nil {
			return scoringapp.InsightPage{}, errors.New("score snapshot explanation is invalid")
		}
		insight := scoringapp.Insight{
			SnapshotID: row.ID, PolicyVersion: row.PolicyVersion, SiteID: row.SiteID, SupplierID: row.SupplierID,
			SupplierName: row.SupplierName, Model: row.Model, PassiveSamples: row.PassiveSamples,
			ActiveSamples: row.ActiveSamples, PriceScore: numericPointer(row.PriceScore),
			LatencyScore: numericPointer(row.LatencyScore), SLAScore: numericPointer(row.SlaScore),
			QualityScore: numericPointer(row.QualityScore), TotalScore: numericPointer(row.TotalScore),
			Confidence: row.Confidence, Eligibility: row.Eligibility, HardReasons: hardReasons,
			AuthenticityVerdict: row.AuthenticityVerdict, FactsThrough: optionalTime(row.FactsThrough),
			WindowStart: row.WindowStart.Time.UTC(), WindowEnd: row.WindowEnd.Time.UTC(),
			CreatedAt: row.CreatedAt.Time.UTC(), Explanation: explanation,
			Recommendations: make([]scoringapp.InsightRecommendation, 0, 5),
		}
		recommendations, err := store.queries.ListShadowRecommendationsForSnapshot(ctx, row.ID)
		if err != nil {
			return scoringapp.InsightPage{}, err
		}
		for _, recommendation := range recommendations {
			var reasons []string
			if err := json.Unmarshal(recommendation.Reasons, &reasons); err != nil {
				return scoringapp.InsightPage{}, err
			}
			insight.Recommendations = append(insight.Recommendations, scoringapp.InsightRecommendation{
				StrategyKind: recommendation.StrategyKind, Action: recommendation.Action,
				CurrentMember: recommendation.CurrentMember, Score: numericPointer(recommendation.Score),
				Confidence: recommendation.Confidence, Reasons: reasons,
			})
		}
		page.Items = append(page.Items, insight)
		page.Total = row.TotalCount
	}
	return page, nil
}

func scoreSnapshotID(snapshot scoringapp.Snapshot) uuid.UUID {
	value := fmt.Sprintf("score|%s|%s|%s|%s|%s", snapshot.Target.SiteID, snapshot.Target.SupplierID, snapshot.Target.Model, domainscoring.PolicyVersionM2ShadowV1, snapshot.WindowEnd.UTC().Format(time.RFC3339Nano))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(value))
}

func optionalDomainScore(score *domainscoring.Score) pgtype.Numeric {
	if score == nil {
		return pgtype.Numeric{}
	}
	return numericValue(score.Float64())
}

func numericPointer(value pgtype.Numeric) *float64 {
	number, valid, err := numericFloat(value)
	if err != nil || !valid {
		return nil
	}
	return &number
}

func nonnegative(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

var _ scoringapp.Repository = (*Store)(nil)

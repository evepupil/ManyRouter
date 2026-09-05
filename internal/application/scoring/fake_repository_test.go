package scoring_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	applicationscoring "github.com/evepupil/ManyRouter/internal/application/scoring"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeRepository struct {
	now time.Time

	calls          []string
	rebuildStart   time.Time
	recentStart    time.Time
	rebuildEnd     time.Time
	rebuildAt      time.Time
	rebuildErr     error
	targets        []applicationscoring.Target
	targetsErr     error
	lowestCost     decimal.Decimal
	lowestCostErr  error
	priceEvidence  applicationscoring.PriceEvidence
	priceErr       error
	collection     applicationscoring.CollectionEvidence
	collectionErrs map[uuid.UUID]error
	evaluations    map[uuid.UUID]applicationscoring.EvaluationEvidence
	evaluationErrs map[uuid.UUID]error
	metrics        applicationscoring.WindowMetrics
	metricsBySpan  map[time.Duration]applicationscoring.WindowMetrics
	metricsErrs    map[uuid.UUID]error
	failures       map[uuid.UUID]applicationscoring.FailureStreak
	failureErrs    map[uuid.UUID]error
	previous       map[recommendationKey]applicationscoring.PreviousRecommendation
	previousErrs   map[recommendationKey]error
	snapshots      []applicationscoring.Snapshot
	saveErrs       map[uuid.UUID]error

	insightPage applicationscoring.InsightPage
	insightErr  error
	listFilters []applicationscoring.InsightFilter
}

func newFakeRepository(now time.Time, targets ...applicationscoring.Target) *fakeRepository {
	authenticityID := uuid.MustParse("a0000000-0000-0000-0000-000000000001")
	capabilityID := uuid.MustParse("c0000000-0000-0000-0000-000000000001")
	repository := &fakeRepository{
		now:           now,
		targets:       targets,
		lowestCost:    decimal.NewFromInt(1),
		priceEvidence: applicationscoring.PriceEvidence{Available: true},
		collection: applicationscoring.CollectionEvidence{
			LastSuccessAt: now.Add(-time.Minute),
			SourceLatest:  now.Add(-time.Minute),
		},
		collectionErrs: make(map[uuid.UUID]error),
		evaluations:    make(map[uuid.UUID]applicationscoring.EvaluationEvidence),
		evaluationErrs: make(map[uuid.UUID]error),
		metrics:        healthyMetrics(now, 200, 0),
		metricsBySpan:  make(map[time.Duration]applicationscoring.WindowMetrics),
		metricsErrs:    make(map[uuid.UUID]error),
		failures:       make(map[uuid.UUID]applicationscoring.FailureStreak),
		failureErrs:    make(map[uuid.UUID]error),
		previous:       make(map[recommendationKey]applicationscoring.PreviousRecommendation),
		previousErrs:   make(map[recommendationKey]error),
		saveErrs:       make(map[uuid.UUID]error),
	}
	for _, target := range targets {
		repository.evaluations[target.SupplierID] = healthyEvaluation(now, authenticityID, capabilityID)
	}
	return repository
}

func (repository *fakeRepository) RefreshMinuteMetrics(_ context.Context, start, recentStart, end, rebuiltAt time.Time) error {
	repository.calls = append(repository.calls, "rebuild")
	repository.rebuildStart = start
	repository.recentStart = recentStart
	repository.rebuildEnd = end
	repository.rebuildAt = rebuiltAt
	return repository.rebuildErr
}

func (repository *fakeRepository) ListScoringTargets(context.Context) ([]applicationscoring.Target, error) {
	repository.calls = append(repository.calls, "list-targets")
	return append([]applicationscoring.Target(nil), repository.targets...), repository.targetsErr
}

func (repository *fakeRepository) GetLowestPeerCost(context.Context, uuid.UUID, string, string) (decimal.Decimal, error) {
	repository.calls = append(repository.calls, "lowest-cost")
	return repository.lowestCost, repository.lowestCostErr
}

func (repository *fakeRepository) GetPriceEvidence(context.Context, applicationscoring.Target, time.Time, time.Time) (applicationscoring.PriceEvidence, error) {
	repository.calls = append(repository.calls, "price-evidence")
	return repository.priceEvidence, repository.priceErr
}

func (repository *fakeRepository) GetWindowMetrics(_ context.Context, target applicationscoring.Target, start, end time.Time) (applicationscoring.WindowMetrics, error) {
	repository.calls = append(repository.calls, fmt.Sprintf("window:%s:%s", target.Model, end.Sub(start)))
	if err := repository.metricsErrs[target.SupplierID]; err != nil {
		return applicationscoring.WindowMetrics{}, err
	}
	if metrics, ok := repository.metricsBySpan[end.Sub(start)]; ok {
		return metrics, nil
	}
	return repository.metrics, nil
}

func (repository *fakeRepository) GetCollectionEvidence(_ context.Context, siteID uuid.UUID) (applicationscoring.CollectionEvidence, error) {
	repository.calls = append(repository.calls, "collection:"+siteID.String())
	if err := repository.collectionErrs[siteID]; err != nil {
		return applicationscoring.CollectionEvidence{}, err
	}
	return repository.collection, nil
}

func (repository *fakeRepository) GetEvaluationEvidence(_ context.Context, supplierID uuid.UUID, _ string, _ time.Time) (applicationscoring.EvaluationEvidence, error) {
	repository.calls = append(repository.calls, "evaluation:"+supplierID.String())
	if err := repository.evaluationErrs[supplierID]; err != nil {
		return applicationscoring.EvaluationEvidence{}, err
	}
	if evidence, ok := repository.evaluations[supplierID]; ok {
		return evidence, nil
	}
	return healthyEvaluation(repository.now, uuid.New(), uuid.New()), nil
}

func (repository *fakeRepository) GetFailureStreak(_ context.Context, target applicationscoring.Target, _ time.Time) (applicationscoring.FailureStreak, error) {
	repository.calls = append(repository.calls, "failures:"+target.SupplierID.String())
	if err := repository.failureErrs[target.SupplierID]; err != nil {
		return applicationscoring.FailureStreak{}, err
	}
	return repository.failures[target.SupplierID], nil
}

func (repository *fakeRepository) FindPreviousRecommendation(
	_ context.Context,
	target applicationscoring.Target,
	kind domainscoring.AutoKind,
	_ time.Time,
) (*applicationscoring.PreviousRecommendation, error) {
	repository.calls = append(repository.calls, "previous:"+target.SupplierID.String()+":"+string(kind))
	key := recommendationKey{SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model, Kind: kind}
	if err := repository.previousErrs[key]; err != nil {
		return nil, err
	}
	previous, ok := repository.previous[key]
	if !ok {
		return nil, nil
	}
	return &previous, nil
}

func (repository *fakeRepository) SaveScoreSnapshot(_ context.Context, snapshot applicationscoring.Snapshot) error {
	repository.calls = append(repository.calls, "save:"+snapshot.Target.SupplierID.String())
	if err := repository.saveErrs[snapshot.Target.SupplierID]; err != nil {
		return err
	}
	repository.snapshots = append(repository.snapshots, snapshot)
	return nil
}

func (repository *fakeRepository) ListInsights(_ context.Context, filter applicationscoring.InsightFilter) (applicationscoring.InsightPage, error) {
	repository.calls = append(repository.calls, "list-insights")
	repository.listFilters = append(repository.listFilters, filter)
	return repository.insightPage, repository.insightErr
}

func newService(t *testing.T, repository *fakeRepository, now time.Time) *applicationscoring.Service {
	t.Helper()
	service, err := applicationscoring.NewService(repository, func() time.Time { return now }, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func scoringTarget(index int) applicationscoring.Target {
	return applicationscoring.Target{
		SiteID:        uuid.MustParse(fmt.Sprintf("10000000-0000-0000-0000-%012d", index)),
		RelationID:    uuid.MustParse(fmt.Sprintf("20000000-0000-0000-0000-%012d", index)),
		SupplierID:    uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-%012d", index)),
		SupplierName:  fmt.Sprintf("Supplier %d", index),
		Model:         "model-a",
		InputPrice:    decimal.NewFromInt(1),
		OutputPrice:   decimal.NewFromInt(1),
		Currency:      "USD",
		DesiredStatus: "enabled",
		SyncStatus:    "active",
	}
}

func healthyMetrics(now time.Time, samples, failures uint64) applicationscoring.WindowMetrics {
	successes := samples - failures
	return applicationscoring.WindowMetrics{
		AttemptCount:         samples,
		SLAAttemptCount:      samples,
		SuccessCount:         successes,
		FailureCount:         failures,
		SLAFailureCount:      failures,
		StreamCount:          successes,
		StreamCompletedCount: successes,
		TTFTCount:            successes,
		SuccessDurationCount: successes,
		FailureDurationCount: failures,
		TTFT:                 latencyHistogram(0, successes),
		SuccessDuration:      latencyHistogram(0, successes),
		FailureDuration:      latencyHistogram(0, failures),
		FactsThrough:         now.Add(-time.Minute),
	}
}

func slowUnreliableMetrics(now time.Time) applicationscoring.WindowMetrics {
	const samples = uint64(200)
	const failures = uint64(40)
	metrics := healthyMetrics(now, samples, failures)
	metrics.RateLimitedCount = failures
	metrics.StreamCompletedCount = 128
	metrics.TTFT = latencyHistogram(15, metrics.TTFTCount)
	metrics.SuccessDuration = latencyHistogram(15, metrics.SuccessDurationCount)
	return metrics
}

func latencyHistogram(bucket int, count uint64) domainscoring.LatencyHistogram {
	var histogram domainscoring.LatencyHistogram
	histogram.Counts[bucket] = count
	return histogram
}

func healthyEvaluation(now time.Time, authenticityID, capabilityID uuid.UUID) applicationscoring.EvaluationEvidence {
	return applicationscoring.EvaluationEvidence{
		AuthenticityID:         &authenticityID,
		Authenticity:           domainevaluation.VerdictConsistent,
		AuthenticityConfidence: 0.95,
		AuthenticityCheckedAt:  now.Add(-time.Hour),
		CapabilityID:           &capabilityID,
		CapabilityScore:        90,
		CapabilityConfidence:   0.95,
		CapabilityCheckedAt:    now.Add(-time.Hour),
		CapabilityChecks:       10,
		HealthScore:            95,
		HealthConfidence:       0.95,
		HealthCheckedAt:        now.Add(-time.Hour),
	}
}

func adviceFor(t *testing.T, snapshot applicationscoring.Snapshot, kind domainscoring.AutoKind) domainscoring.ShadowAdvice {
	t.Helper()
	for _, advice := range snapshot.Recommendations {
		if advice.AutoKind == kind {
			return advice
		}
	}
	t.Fatalf("missing recommendation for %s", kind)
	return domainscoring.ShadowAdvice{}
}

type recommendationKey struct {
	SiteID     uuid.UUID
	SupplierID uuid.UUID
	Model      string
	Kind       domainscoring.AutoKind
}

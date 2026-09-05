package scoring_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	applicationscoring "github.com/evepupil/ManyRouter/internal/application/scoring"
	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/shopspring/decimal"
)

func TestRefreshRebuildsMinuteFactsBeforeScoringAllThreeWindows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 14, 37, 42, 0, time.UTC)
	target := scoringTarget(1)
	repository := newFakeRepository(now, target)
	service := newService(t, repository, now)

	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.calls) < 2 || repository.calls[0] != "rebuild" || repository.calls[1] != "list-targets" {
		t.Fatalf("refresh order = %#v", repository.calls)
	}
	wantEnd := now.Truncate(time.Minute)
	if !repository.rebuildEnd.Equal(wantEnd) || !repository.rebuildStart.Equal(wantEnd.Add(-24*time.Hour)) || !repository.rebuildAt.Equal(now) {
		t.Fatalf("unexpected rebuild range: %s .. %s at %s", repository.rebuildStart, repository.rebuildEnd, repository.rebuildAt)
	}
	if !repository.recentStart.Equal(wantEnd.Add(-10 * time.Minute)) {
		t.Fatalf("unexpected recent rebuild start: %s", repository.recentStart)
	}
	if len(repository.snapshots) != 1 {
		t.Fatalf("saved %d snapshots, want 1", len(repository.snapshots))
	}
	snapshot := repository.snapshots[0]
	if !snapshot.WindowStart.Equal(wantEnd.Add(-24*time.Hour)) || !snapshot.WindowEnd.Equal(wantEnd) {
		t.Fatalf("unexpected snapshot window: %s .. %s", snapshot.WindowStart, snapshot.WindowEnd)
	}
	if len(snapshot.Recommendations) != 5 {
		t.Fatalf("got %d fixed Auto recommendations", len(snapshot.Recommendations))
	}
	weights := adviceFor(t, snapshot, domainscoring.AutoBalanced).WindowWeights
	assertApplicationWindowWeight(t, weights, domainscoring.Window15Minutes, 0.50)
	assertApplicationWindowWeight(t, weights, domainscoring.Window1Hour, 0.30)
	assertApplicationWindowWeight(t, weights, domainscoring.Window24Hours, 0.20)
}

func TestRefreshTurnsSampleCollectionAndAttributionGapsIntoWatchOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*fakeRepository)
		check  func(*testing.T, applicationscoring.Snapshot)
	}{
		{
			name: "insufficient 15 minute samples",
			change: func(repository *fakeRepository) {
				repository.metricsBySpan[15*time.Minute] = healthyMetrics(now, domainscoring.DefaultMinimumSamples-1, 0)
			},
			check: func(t *testing.T, snapshot applicationscoring.Snapshot) {
				t.Helper()
				if snapshot.Scores == nil {
					t.Fatal("complete fallback windows should keep a displayable score")
				}
				advice := adviceFor(t, snapshot, domainscoring.AutoBalanced)
				assertApplicationWindowWeight(t, advice.WindowWeights, domainscoring.Window1Hour, 0.60)
				assertApplicationWindowWeight(t, advice.WindowWeights, domainscoring.Window24Hours, 0.40)
				if hasApplicationWindowWeight(advice.WindowWeights, domainscoring.Window15Minutes) {
					t.Fatalf("insufficient 15 minute window retained weight: %#v", advice.WindowWeights)
				}
			},
		},
		{
			name: "collection gap",
			change: func(repository *fakeRepository) {
				repository.collection.DataGap = true
			},
		},
		{
			name: "pending attribution",
			change: func(repository *fakeRepository) {
				repository.metrics.PendingAttribution = true
			},
		},
		{
			name: "missing price history",
			change: func(repository *fakeRepository) {
				repository.priceEvidence.Available = false
			},
		},
		{
			name: "coarse duration evidence",
			change: func(repository *fakeRepository) {
				repository.metrics.CoarseDurationCount = repository.metrics.SuccessDurationCount
			},
		},
	}
	for index, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := scoringTarget(index + 10)
			repository := newFakeRepository(now, target)
			test.change(repository)
			if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(repository.snapshots) != 1 {
				t.Fatalf("saved %d snapshots", len(repository.snapshots))
			}
			assertOnlyAction(t, repository.snapshots[0], domainscoring.AdviceWatch)
			if repository.snapshots[0].Eligibility != "insufficient" {
				t.Fatalf("eligibility = %q", repository.snapshots[0].Eligibility)
			}
			if test.check != nil {
				test.check(t, repository.snapshots[0])
			}
		})
	}
}

func TestAuthenticityMismatchCreatesFiveExcludeRecommendations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	target := scoringTarget(20)
	repository := newFakeRepository(now, target)
	evaluation := repository.evaluations[target.SupplierID]
	evaluation.Authenticity = domainevaluation.VerdictInconsistent
	repository.evaluations[target.SupplierID] = evaluation

	if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.snapshots) != 1 {
		t.Fatalf("saved %d snapshots", len(repository.snapshots))
	}
	snapshot := repository.snapshots[0]
	assertOnlyAction(t, snapshot, domainscoring.AdviceExclude)
	if snapshot.Eligibility != "excluded" || len(snapshot.HardReasons) != 1 || snapshot.HardReasons[0] != domainscoring.GateAuthenticityMismatch {
		t.Fatalf("unexpected exclusion snapshot: %#v", snapshot)
	}
}

func TestMissingEvaluationEvidenceCreatesWatchRecommendationsOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 17, 0, 0, 0, time.UTC)
	target := scoringTarget(30)
	repository := newFakeRepository(now, target)
	repository.evaluations[target.SupplierID] = applicationscoring.EvaluationEvidence{}

	if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.snapshots) != 1 {
		t.Fatalf("saved %d snapshots", len(repository.snapshots))
	}
	assertOnlyAction(t, repository.snapshots[0], domainscoring.AdviceWatch)
}

func TestExistingBalancedMemberGetsKeepOrExitAdvice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		change func(*applicationscoring.Target, *fakeRepository)
		want   domainscoring.AdviceAction
	}{
		{
			name: "healthy member stays",
			change: func(target *applicationscoring.Target, _ *fakeRepository) {
				target.CurrentStrategies = []domainscoring.AutoKind{domainscoring.AutoBalanced}
			},
			want: domainscoring.AdviceKeep,
		},
		{
			name: "persistently poor member exits",
			change: func(target *applicationscoring.Target, repository *fakeRepository) {
				target.CurrentStrategies = []domainscoring.AutoKind{domainscoring.AutoBalanced}
				target.InputPrice = decimal.NewFromInt(3)
				target.OutputPrice = decimal.NewFromInt(3)
				repository.metrics = slowUnreliableMetrics(now)
				repository.previous[recommendationKey{
					SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model, Kind: domainscoring.AutoBalanced,
				}] = applicationscoring.PreviousRecommendation{
					Score: scorePointer(20), CreatedAt: now.Add(-5 * time.Minute), Confidence: domainscoring.ConfidenceHigh,
				}
			},
			want: domainscoring.AdviceExit,
		},
	}
	for index, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := scoringTarget(index + 40)
			repository := newFakeRepository(now, target)
			test.change(&target, repository)
			repository.targets = []applicationscoring.Target{target}
			if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			advice := adviceFor(t, repository.snapshots[0], domainscoring.AutoBalanced)
			if advice.Action != test.want {
				t.Fatalf("balanced member advice = %q, want %q; reasons=%v score=%v", advice.Action, test.want, advice.Reasons, advice.CompositeScore)
			}
		})
	}
}

func TestRefreshSkipsManuallyDisabledAndUnsyncedRelations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 18, 30, 0, 0, time.UTC)
	disabled := scoringTarget(47)
	disabled.DesiredStatus = "disabled"
	unsynced := scoringTarget(48)
	unsynced.SyncStatus = "failed"
	repository := newFakeRepository(now, disabled, unsynced)

	if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.snapshots) != 0 {
		t.Fatalf("ineligible relations received score snapshots: %#v", repository.snapshots)
	}
}

func TestRecommendationConfirmationRejectsStaleOrWeakHistory(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 18, 45, 0, 0, time.UTC)
	tests := []struct {
		name       string
		createdAt  time.Time
		confidence domainscoring.Confidence
	}{
		{name: "stale", createdAt: now.Add(-11 * time.Minute), confidence: domainscoring.ConfidenceHigh},
		{name: "weak", createdAt: now.Add(-5 * time.Minute), confidence: domainscoring.ConfidenceLow},
	}
	for index, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			target := scoringTarget(70 + index)
			repository := newFakeRepository(now, target)
			for _, kind := range []domainscoring.AutoKind{
				domainscoring.AutoLowestPrice, domainscoring.AutoLowLatency, domainscoring.AutoHighSLA,
				domainscoring.AutoHighQuality, domainscoring.AutoBalanced,
			} {
				repository.previous[recommendationKey{SiteID: target.SiteID, SupplierID: target.SupplierID, Model: target.Model, Kind: kind}] = applicationscoring.PreviousRecommendation{
					Score: scorePointer(100), CreatedAt: test.createdAt, Confidence: test.confidence,
				}
			}
			if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertOnlyAction(t, repository.snapshots[0], domainscoring.AdviceWatch)
		})
	}
}

func TestZeroPriceCandidateStillProducesScoredSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 19, 0, 0, 0, time.UTC)
	target := scoringTarget(50)
	target.InputPrice = decimal.Zero
	target.OutputPrice = decimal.Zero
	repository := newFakeRepository(now, target)
	repository.lowestCost = decimal.NewFromInt(1)

	if err := newService(t, repository, now).Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.snapshots) != 1 || repository.snapshots[0].Scores == nil {
		t.Fatalf("zero-price target was not scored: %#v", repository.snapshots)
	}
	price := repository.snapshots[0].Scores.Price
	if price != 100 {
		t.Fatalf("zero-price score = %v, want 100", price)
	}
}

func TestRefreshContinuesAfterOneTargetFails(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)
	failed := scoringTarget(60)
	healthy := scoringTarget(61)
	repository := newFakeRepository(now, failed, healthy)
	repository.collectionErrs[failed.SiteID] = errors.New("collection unavailable")

	err := newService(t, repository, now).Refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), failed.SupplierID.String()) || !strings.Contains(err.Error(), "collection unavailable") {
		t.Fatalf("refresh error = %v", err)
	}
	if len(repository.snapshots) != 1 || repository.snapshots[0].Target.SupplierID != healthy.SupplierID {
		t.Fatalf("healthy target was blocked: %#v", repository.snapshots)
	}
}

func assertOnlyAction(t *testing.T, snapshot applicationscoring.Snapshot, action domainscoring.AdviceAction) {
	t.Helper()
	if len(snapshot.Recommendations) != 5 {
		t.Fatalf("got %d recommendations", len(snapshot.Recommendations))
	}
	for _, advice := range snapshot.Recommendations {
		if advice.Action != action {
			t.Fatalf("%s advice = %q, want %q; reasons=%v", advice.AutoKind, advice.Action, action, advice.Reasons)
		}
	}
}

func assertApplicationWindowWeight(t *testing.T, weights []domainscoring.WindowWeight, window domainscoring.Window, want float64) {
	t.Helper()
	for _, weight := range weights {
		if weight.Window == window {
			if math.Abs(weight.Weight-want) > 1e-9 {
				t.Fatalf("%s weight = %.6f, want %.6f", window, weight.Weight, want)
			}
			return
		}
	}
	t.Fatalf("missing window weight %s", window)
}

func hasApplicationWindowWeight(weights []domainscoring.WindowWeight, window domainscoring.Window) bool {
	for _, weight := range weights {
		if weight.Window == window {
			return true
		}
	}
	return false
}

func scorePointer(value domainscoring.Score) *domainscoring.Score {
	return &value
}

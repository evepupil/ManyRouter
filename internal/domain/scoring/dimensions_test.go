package scoring_test

import (
	"errors"
	"math"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/shopspring/decimal"
)

func TestPriceScoreCombinesRelativeCostAndPriceStability(t *testing.T) {
	t.Parallel()
	rules := scoring.PriceRules{
		RelativeCost:    metricRule(1, 2, 0.70),
		ChangesPerDay:   metricRule(0, 10, 0.15),
		ChangeMagnitude: metricRule(0, 1, 0.15),
	}
	tests := []struct {
		name    string
		metrics scoring.PriceMetrics
		want    float64
	}{
		{
			name: "cheapest and stable",
			metrics: scoring.PriceMetrics{
				CurrentCost:         decimal.RequireFromString("1"),
				LowestCandidateCost: decimal.RequireFromString("1"),
			},
			want: 100,
		},
		{
			name: "free candidate",
			metrics: scoring.PriceMetrics{
				CurrentCost:         decimal.Zero,
				LowestCandidateCost: decimal.Zero,
			},
			want: 100,
		},
		{
			name: "free candidate with positive peer baseline",
			metrics: scoring.PriceMetrics{
				CurrentCost:         decimal.Zero,
				LowestCandidateCost: decimal.RequireFromString("1"),
			},
			want: 100,
		},
		{
			name: "midway on every component",
			metrics: scoring.PriceMetrics{
				CurrentCost:          decimal.RequireFromString("1.5"),
				LowestCandidateCost:  decimal.RequireFromString("1"),
				ChangesPerDay:        5,
				ChangeMagnitudeRatio: 0.5,
			},
			want: 50,
		},
		{
			name: "twice the cheapest and unstable",
			metrics: scoring.PriceMetrics{
				CurrentCost:          decimal.RequireFromString("2"),
				LowestCandidateCost:  decimal.RequireFromString("1"),
				ChangesPerDay:        10,
				ChangeMagnitudeRatio: 1,
			},
			want: 0,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := scoring.ScorePrice(test.metrics, rules)
			if err != nil {
				t.Fatal(err)
			}
			assertScore(t, result.Score, test.want)
			if result.Dimension != scoring.DimensionPrice || len(result.Components) != 3 {
				t.Fatalf("price explanation is incomplete: %#v", result)
			}
		})
	}
}

func TestLatencySLAAndQualityScoresStayOnTheSharedScale(t *testing.T) {
	t.Parallel()
	latency, err := scoring.ScoreLatency(scoring.LatencyMetrics{
		TTFTP50Millis: 550, TTFTP95Millis: 1_100,
		DurationP50Millis: 2_200, DurationP95Millis: 4_400,
		VariabilityRatio: 0.5,
	}, scoring.LatencyRules{
		TTFTP50: metricRule(100, 1_000, 0.20), TTFTP95: metricRule(200, 2_000, 0.20),
		DurationP50: metricRule(400, 4_000, 0.20), DurationP95: metricRule(800, 8_000, 0.20),
		Variability: metricRule(0, 1, 0.20),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertScore(t, latency.Score, 50)

	sla, err := scoring.ScoreSLA(scoring.SLAMetrics{
		AttemptSuccessRate: 1, StreamCompletionRate: 1, CapacityStability: 1,
	}, scoring.SLARules{
		AttemptSuccess: metricRule(1, 0, 0.30), RateLimit: metricRule(0, 1, 0.15),
		ConsecutiveFails: metricRule(0, 10, 0.10), StreamCompletion: metricRule(1, 0, 0.20),
		Recovery: metricRule(0, 60_000, 0.10), Capacity: metricRule(1, 0, 0.15),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertScore(t, sla.Score, 100)

	quality, err := scoring.ScoreQuality(scoring.QualityMetrics{
		Authenticity:       scoring.AuthenticitySuspicious,
		CapabilityRate:     1,
		StabilityRate:      1,
		EvidenceConfidence: 1,
	}, scoring.QualityRules{
		Authenticity:       metricRule(1, 0, 0.40),
		AuthenticityValues: scoring.AuthenticityValues{Consistent: 1, Suspicious: 0.5, Inconsistent: 0},
		Capability:         metricRule(1, 0, 0.30),
		Stability:          metricRule(1, 0, 0.20),
		Evidence:           metricRule(1, 0, 0.10),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertScore(t, quality.Score, 80)

	for _, result := range []scoring.DimensionScore{latency, sla, quality} {
		if result.Score < 0 || result.Score > 100 {
			t.Fatalf("score escaped 0..100: %#v", result)
		}
	}
}

func TestDimensionScoresRejectInvalidFactsAndUnversionableRules(t *testing.T) {
	t.Parallel()
	priceRules := scoring.PriceRules{
		RelativeCost: metricRule(1, 2, 0.70), ChangesPerDay: metricRule(0, 10, 0.15), ChangeMagnitude: metricRule(0, 1, 0.15),
	}
	_, err := scoring.ScorePrice(scoring.PriceMetrics{
		CurrentCost: decimal.RequireFromString("0.9"), LowestCandidateCost: decimal.RequireFromString("1"),
	}, priceRules)
	if !errors.Is(err, scoring.ErrInvalidMetric) {
		t.Fatalf("candidate cheaper than the stated minimum returned %v", err)
	}

	invalidWeights := priceRules
	invalidWeights.RelativeCost.Weight = 0.60
	_, err = scoring.ScorePrice(scoring.PriceMetrics{
		CurrentCost: decimal.RequireFromString("1"), LowestCandidateCost: decimal.RequireFromString("1"),
	}, invalidWeights)
	if !errors.Is(err, scoring.ErrInvalidRules) {
		t.Fatalf("weights not summing to one returned %v", err)
	}

	_, err = scoring.ScoreLatency(scoring.LatencyMetrics{TTFTP50Millis: math.NaN()}, scoring.LatencyRules{})
	if !errors.Is(err, scoring.ErrInvalidMetric) {
		t.Fatalf("non-finite latency returned %v", err)
	}
	_, err = scoring.ScoreQuality(
		scoring.QualityMetrics{Authenticity: scoring.AuthenticityPending},
		scoring.QualityRules{AuthenticityValues: scoring.AuthenticityValues{Consistent: 1, Suspicious: 0.5}},
	)
	if !errors.Is(err, scoring.ErrInvalidMetric) {
		t.Fatalf("pending authenticity was scored: %v", err)
	}
}

func TestMetricNormalizationClampsBeyondBestAndWorst(t *testing.T) {
	t.Parallel()
	rules := scoring.LatencyRules{
		TTFTP50: metricRule(100, 1_000, 0.20), TTFTP95: metricRule(100, 1_000, 0.20),
		DurationP50: metricRule(100, 1_000, 0.20), DurationP95: metricRule(100, 1_000, 0.20),
		Variability: metricRule(0.1, 0.9, 0.20),
	}
	best, err := scoring.ScoreLatency(scoring.LatencyMetrics{
		TTFTP50Millis: 0, TTFTP95Millis: 0, DurationP50Millis: 0, DurationP95Millis: 0,
		VariabilityRatio: 0,
	}, rules)
	if err != nil {
		t.Fatal(err)
	}
	assertScore(t, best.Score, 100)

	worst, err := scoring.ScoreLatency(scoring.LatencyMetrics{
		TTFTP50Millis: 2_000, TTFTP95Millis: 2_000, DurationP50Millis: 2_000, DurationP95Millis: 2_000,
		VariabilityRatio: 1,
	}, rules)
	if err != nil {
		t.Fatal(err)
	}
	assertScore(t, worst.Score, 0)
}

func TestMetricRulesRejectCollapsedNonFiniteAndNonPositiveComponents(t *testing.T) {
	t.Parallel()
	metrics := scoring.PriceMetrics{
		CurrentCost: decimal.RequireFromString("1"), LowestCandidateCost: decimal.RequireFromString("1"),
	}
	valid := scoring.PriceRules{
		RelativeCost: metricRule(1, 2, 0.70), ChangesPerDay: metricRule(0, 10, 0.15), ChangeMagnitude: metricRule(0, 1, 0.15),
	}
	tests := []struct {
		name   string
		change func(*scoring.PriceRules)
	}{
		{name: "collapsed endpoints", change: func(rules *scoring.PriceRules) { rules.RelativeCost.Worst = rules.RelativeCost.Best }},
		{name: "non-finite endpoint", change: func(rules *scoring.PriceRules) { rules.RelativeCost.Best = math.Inf(1) }},
		{name: "zero component", change: func(rules *scoring.PriceRules) {
			rules.RelativeCost.Weight = 0
			rules.ChangesPerDay.Weight = 0.50
			rules.ChangeMagnitude.Weight = 0.50
		}},
		{name: "negative component", change: func(rules *scoring.PriceRules) {
			rules.RelativeCost.Weight = -0.10
			rules.ChangesPerDay.Weight = 0.55
			rules.ChangeMagnitude.Weight = 0.55
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rules := valid
			test.change(&rules)
			if _, err := scoring.ScorePrice(metrics, rules); !errors.Is(err, scoring.ErrInvalidRules) {
				t.Fatalf("invalid rule returned %v", err)
			}
		})
	}
}

func metricRule(best, worst, weight float64) scoring.MetricRule {
	return scoring.MetricRule{Best: best, Worst: worst, Weight: weight}
}

func assertScore(t *testing.T, got scoring.Score, want float64) {
	t.Helper()
	if math.Abs(got.Float64()-want) > 1e-9 {
		t.Fatalf("score = %.6f, want %.6f", got.Float64(), want)
	}
}

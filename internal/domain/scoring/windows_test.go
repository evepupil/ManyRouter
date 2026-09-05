package scoring_test

import (
	"errors"
	"math"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/scoring"
)

func TestEvidenceUsesTheWeakestFactorAndBlocksUncertainFacts(t *testing.T) {
	t.Parallel()
	mediumEvidence := strongEvidence()
	mediumEvidence.Factors[4].Level = scoring.ConfidenceMedium
	medium, err := scoring.AssessEvidence(mediumEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if medium.Confidence != scoring.ConfidenceMedium || !medium.UsableForScoring || !medium.DecisionReady {
		t.Fatalf("unexpected weakest-factor result: %#v", medium)
	}

	tests := []struct {
		name   string
		change func(*scoring.EvidenceInput)
		reason scoring.EvidenceReason
	}{
		{
			name: "insufficient samples",
			change: func(input *scoring.EvidenceInput) {
				input.SampleCount = scoring.DefaultMinimumSamples - 1
			},
			reason: scoring.EvidenceInsufficientSamples,
		},
		{
			name: "collection gap",
			change: func(input *scoring.EvidenceInput) {
				input.CollectionGaps = []string{"collector behind source"}
			},
			reason: scoring.EvidenceCollectionGap,
		},
		{
			name: "pending attribution",
			change: func(input *scoring.EvidenceInput) {
				input.AttributionPending = true
			},
			reason: scoring.EvidenceAttributionPending,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := strongEvidence()
			test.change(&input)
			assessment, assessErr := scoring.AssessEvidence(input)
			if assessErr != nil {
				t.Fatal(assessErr)
			}
			if assessment.Confidence != scoring.ConfidenceInsufficient || assessment.UsableForScoring || assessment.DecisionReady {
				t.Fatalf("uncertain evidence became actionable: %#v", assessment)
			}
			if !hasEvidenceReason(assessment.Issues, test.reason) {
				t.Fatalf("missing reason %q in %#v", test.reason, assessment.Issues)
			}
		})
	}
}

func TestEvidenceRequiresEveryNamedIntegrityFactor(t *testing.T) {
	t.Parallel()
	missing := strongEvidence()
	missing.Factors = missing.Factors[:len(missing.Factors)-1]
	assessment, err := scoring.AssessEvidence(missing)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.DecisionReady || !hasEvidenceIssue(assessment.Issues, scoring.EvidenceFactorMissing, string(scoring.FactorAuthenticity)) {
		t.Fatalf("missing authenticity evidence was accepted: %#v", assessment)
	}

	unknown := strongEvidence()
	unknown.Factors = append(unknown.Factors, scoring.ConfidenceFactor{Name: "typo", Level: scoring.ConfidenceHigh})
	if _, err := scoring.AssessEvidence(unknown); !errors.Is(err, scoring.ErrInvalidEvidence) {
		t.Fatalf("unknown confidence factor returned %v", err)
	}
}

func TestWindowScoresUseFixedWeightsWhenAllWindowsAreComplete(t *testing.T) {
	t.Parallel()
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		windowScore(scoring.Window15Minutes, 90),
		windowScore(scoring.Window1Hour, 60),
		windowScore(scoring.Window24Hours, 30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !combined.Available || !combined.DecisionReady || combined.Confidence != scoring.ConfidenceHigh {
		t.Fatalf("complete windows were not decision ready: %#v", combined)
	}
	if combined.PolicyVersion != scoring.PolicyVersionM2ShadowV1 {
		t.Fatalf("window policy version = %q", combined.PolicyVersion)
	}
	assertAllDimensions(t, combined.Scores, 69)
	assertWindowWeight(t, combined.EffectiveWeights, scoring.Window15Minutes, 0.50)
	assertWindowWeight(t, combined.EffectiveWeights, scoring.Window1Hour, 0.30)
	assertWindowWeight(t, combined.EffectiveWeights, scoring.Window24Hours, 0.20)
}

func TestMissingWindowRedistributesOnlyAcrossCompleteWindowsAndLowersConfidence(t *testing.T) {
	t.Parallel()
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		windowScore(scoring.Window1Hour, 80),
		windowScore(scoring.Window24Hours, 20),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertAllDimensions(t, combined.Scores, 56)
	assertWindowWeight(t, combined.EffectiveWeights, scoring.Window1Hour, 0.60)
	assertWindowWeight(t, combined.EffectiveWeights, scoring.Window24Hours, 0.40)
	if combined.Confidence != scoring.ConfidenceMedium || combined.DecisionReady {
		t.Fatalf("one absent window should lower confidence and require observation: %#v", combined)
	}
}

func TestIncompleteEvidenceCanBeDisplayedButCannotProduceADefiniteDecision(t *testing.T) {
	t.Parallel()
	insufficient := windowScore(scoring.Window15Minutes, 100)
	insufficient.Evidence.SampleCount = scoring.DefaultMinimumSamples - 1
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		insufficient,
		windowScore(scoring.Window1Hour, 60),
		windowScore(scoring.Window24Hours, 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !combined.Available {
		t.Fatal("complete fallback windows should still produce a displayable score")
	}
	assertAllDimensions(t, combined.Scores, 52)
	if combined.DecisionReady {
		t.Fatalf("sample gap produced a definite decision: %#v", combined)
	}
	if !hasWindowEvidenceReason(combined.Issues, scoring.Window15Minutes, scoring.EvidenceInsufficientSamples) {
		t.Fatalf("missing sample issue: %#v", combined.Issues)
	}
}

func TestASecondMissingWindowDropsConfidenceBelowTheDecisionFloor(t *testing.T) {
	t.Parallel()
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		windowScore(scoring.Window15Minutes, 75),
	})
	if err != nil {
		t.Fatal(err)
	}
	if combined.Confidence != scoring.ConfidenceLow || combined.DecisionReady {
		t.Fatalf("two missing windows should require observation: %#v", combined)
	}
	assertAllDimensions(t, combined.Scores, 75)
}

func TestWindowCombinationRejectsDuplicateWindows(t *testing.T) {
	t.Parallel()
	input := windowScore(scoring.Window15Minutes, 75)
	_, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{input, input})
	if !errors.Is(err, scoring.ErrInvalidWindow) {
		t.Fatalf("duplicate window returned %v", err)
	}
}

func windowScore(window scoring.Window, value float64) scoring.WindowScoreInput {
	return scoring.WindowScoreInput{
		Window:   window,
		Complete: true,
		Scores:   equalScores(value),
		Evidence: strongEvidence(),
	}
}

func equalScores(value float64) scoring.DimensionScores {
	score := scoring.Score(value)
	return scoring.DimensionScores{Price: score, Latency: score, SLA: score, Quality: score}
}

func strongEvidence() scoring.EvidenceInput {
	return scoring.EvidenceInput{
		SampleCount: scoring.DefaultMinimumSamples,
		Factors: []scoring.ConfidenceFactor{
			{Name: scoring.FactorFreshness, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorCollectionCoverage, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorTimingCoverage, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorAttribution, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorSupplierMapping, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorRealTraffic, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorActiveProbe, Level: scoring.ConfidenceHigh},
			{Name: scoring.FactorAuthenticity, Level: scoring.ConfidenceHigh},
		},
	}
}

func assertAllDimensions(t *testing.T, scores scoring.DimensionScores, want float64) {
	t.Helper()
	for name, got := range map[string]scoring.Score{
		"price": scores.Price, "latency": scores.Latency, "sla": scores.SLA, "quality": scores.Quality,
	} {
		if math.Abs(got.Float64()-want) > 1e-9 {
			t.Fatalf("%s score = %.6f, want %.6f", name, got.Float64(), want)
		}
	}
}

func assertWindowWeight(t *testing.T, weights []scoring.WindowWeight, window scoring.Window, want float64) {
	t.Helper()
	for _, weight := range weights {
		if weight.Window == window {
			if math.Abs(weight.Weight-want) > 1e-9 {
				t.Fatalf("%s weight = %.6f, want %.6f", window, weight.Weight, want)
			}
			return
		}
	}
	t.Fatalf("missing effective weight for %s", window)
}

func hasEvidenceReason(issues []scoring.EvidenceIssue, reason scoring.EvidenceReason) bool {
	for _, issue := range issues {
		if issue.Reason == reason {
			return true
		}
	}
	return false
}

func hasEvidenceIssue(issues []scoring.EvidenceIssue, reason scoring.EvidenceReason, detail string) bool {
	for _, issue := range issues {
		if issue.Reason == reason && issue.Detail == detail {
			return true
		}
	}
	return false
}

func hasWindowEvidenceReason(issues []scoring.WindowIssue, window scoring.Window, reason scoring.EvidenceReason) bool {
	for _, issue := range issues {
		if issue.Window == window && issue.EvidenceReason == reason {
			return true
		}
	}
	return false
}

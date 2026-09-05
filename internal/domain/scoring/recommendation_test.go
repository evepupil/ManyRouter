package scoring_test

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/scoring"
)

func TestFixedAutoWeightsMatchM2ShadowV1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind scoring.AutoKind
		want scoring.AutoWeights
	}{
		{scoring.AutoLowestPrice, scoring.AutoWeights{Price: 0.55, Latency: 0.15, SLA: 0.15, Quality: 0.15}},
		{scoring.AutoLowLatency, scoring.AutoWeights{Price: 0.15, Latency: 0.55, SLA: 0.20, Quality: 0.10}},
		{scoring.AutoHighSLA, scoring.AutoWeights{Price: 0.10, Latency: 0.20, SLA: 0.60, Quality: 0.10}},
		{scoring.AutoHighQuality, scoring.AutoWeights{Price: 0.10, Latency: 0.15, SLA: 0.15, Quality: 0.60}},
		{scoring.AutoBalanced, scoring.AutoWeights{Price: 0.25, Latency: 0.25, SLA: 0.30, Quality: 0.20}},
	}
	for _, test := range tests {
		test := test
		t.Run(string(test.kind), func(t *testing.T) {
			t.Parallel()
			got, err := scoring.FixedAutoWeights(scoring.PolicyVersionM2ShadowV1, test.kind)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("weights = %#v, want %#v", got, test.want)
			}
			if math.Abs(got.Price+got.Latency+got.SLA+got.Quality-1) > 1e-9 {
				t.Fatalf("weights do not sum to one: %#v", got)
			}
		})
	}
	if _, err := scoring.FixedAutoWeights("future-policy", scoring.AutoBalanced); !errors.Is(err, scoring.ErrInvalidRules) {
		t.Fatalf("unknown policy version returned %v", err)
	}
}

func TestConfirmedHardGatesExcludeBeforeScoreAndEvidence(t *testing.T) {
	t.Parallel()
	input := scoring.RecommendationInput{
		AutoKind: scoring.AutoKind("future_strategy"),
		HardGates: scoring.HardGateInput{
			Authenticity:                  scoring.AuthenticityInconsistent,
			CredentialValid:               scoring.CheckFail,
			BalanceAvailable:              scoring.CheckFail,
			MajorRiskAbsent:               scoring.CheckFail,
			AttributedConsecutiveFailures: scoring.DefaultConsecutiveFailureLimit,
		},
	}
	policy := scoring.RecommendationPolicy{
		Version:                    scoring.PolicyVersionM2ShadowV1,
		JoinThreshold:              0,
		ExitThreshold:              100,
		RequiredConsecutiveWindows: 0,
		HardGates:                  scoring.DefaultHardGatePolicy(),
	}
	advice, err := scoring.Recommend(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	if advice.Action != scoring.AdviceExclude || advice.CompositeScore != nil {
		t.Fatalf("hard gate did not bypass scoring: %#v", advice)
	}
	wantReasons := []scoring.GateReason{
		scoring.GateAuthenticityMismatch,
		scoring.GateCredentialInvalid,
		scoring.GateBalanceUnavailable,
		scoring.GateConsecutiveFailures,
		scoring.GateMajorRisk,
	}
	if !reflect.DeepEqual(advice.HardReasons, wantReasons) {
		t.Fatalf("hard reasons = %#v, want %#v", advice.HardReasons, wantReasons)
	}
}

func TestPendingHardGateAndIncompleteEvidenceOnlyProduceWatchAdvice(t *testing.T) {
	t.Parallel()
	policy := recommendationPolicy()
	ready := readyCombined(t, 90)

	pendingGates := passingGates()
	pendingGates.CredentialValid = scoring.CheckPending
	advice, err := scoring.Recommend(scoring.RecommendationInput{
		AutoKind:  scoring.AutoLowestPrice,
		Windows:   ready,
		HardGates: pendingGates,
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if advice.Action != scoring.AdviceWatch || advice.Reasons[0] != scoring.AdviceHardGatePending || advice.CompositeScore != nil {
		t.Fatalf("pending gate became definite: %#v", advice)
	}

	insufficient := windowScore(scoring.Window15Minutes, 90)
	insufficient.Evidence.AttributionPending = true
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		insufficient,
		windowScore(scoring.Window1Hour, 90),
		windowScore(scoring.Window24Hours, 90),
	})
	if err != nil {
		t.Fatal(err)
	}
	advice, err = scoring.Recommend(scoring.RecommendationInput{
		AutoKind:  scoring.AutoLowestPrice,
		Windows:   combined,
		HardGates: passingGates(),
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if advice.Action != scoring.AdviceWatch || advice.Reasons[0] != scoring.AdviceEvidenceNotReady || advice.CompositeScore != nil {
		t.Fatalf("pending attribution became definite: %#v", advice)
	}
}

func TestRecommendationRejectsWindowScoresFromAnotherPolicyVersion(t *testing.T) {
	t.Parallel()
	windows := readyCombined(t, 90)
	windows.PolicyVersion = "future-policy"
	_, err := scoring.Recommend(scoring.RecommendationInput{
		AutoKind:  scoring.AutoBalanced,
		Windows:   windows,
		HardGates: passingGates(),
	}, recommendationPolicy())
	if !errors.Is(err, scoring.ErrInvalidRecommendation) {
		t.Fatalf("mixed policy versions returned %v", err)
	}
}

func TestShadowAdviceUsesSeparateJoinAndExitLinesWithConfirmationWindows(t *testing.T) {
	t.Parallel()
	policy := recommendationPolicy()
	tests := []struct {
		name       string
		member     bool
		score      float64
		joinStreak uint64
		exitStreak uint64
		want       scoring.AdviceAction
		reason     scoring.AdviceReason
	}{
		{name: "join after confirmation", score: 80, joinStreak: 2, want: scoring.AdviceJoin, reason: scoring.AdviceJoinThresholdMet},
		{name: "wait for join confirmation", score: 80, joinStreak: 1, want: scoring.AdviceWatch, reason: scoring.AdviceJoinConfirmationPending},
		{name: "candidate below join line", score: 70, want: scoring.AdviceWatch, reason: scoring.AdviceBelowJoinThreshold},
		{name: "keep member above exit line", member: true, score: 60, want: scoring.AdviceKeep, reason: scoring.AdviceMemberWithinExitLine},
		{name: "exit after confirmation", member: true, score: 50, exitStreak: 2, want: scoring.AdviceExit, reason: scoring.AdviceExitThresholdMet},
		{name: "wait for exit confirmation", member: true, score: 50, exitStreak: 1, want: scoring.AdviceWatch, reason: scoring.AdviceExitConfirmationPending},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			advice, err := scoring.Recommend(scoring.RecommendationInput{
				AutoKind:               scoring.AutoBalanced,
				CurrentMember:          test.member,
				Windows:                readyCombined(t, test.score),
				HardGates:              passingGates(),
				ConsecutiveJoinWindows: test.joinStreak,
				ConsecutiveExitWindows: test.exitStreak,
			}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if advice.Action != test.want || len(advice.Reasons) != 1 || advice.Reasons[0] != test.reason || advice.CompositeScore == nil {
				t.Fatalf("advice = %#v, want action=%q reason=%q", advice, test.want, test.reason)
			}
			if advice.PolicyVersion != scoring.PolicyVersionM2ShadowV1 {
				t.Fatalf("advice policy version = %q", advice.PolicyVersion)
			}
			assertScore(t, *advice.CompositeScore, test.score)
		})
	}
}

func TestShadowAdviceContainsNoExecutionCapability(t *testing.T) {
	t.Parallel()
	typeOfAdvice := reflect.TypeOf(scoring.ShadowAdvice{})
	if typeOfAdvice.NumMethod() != 0 || reflect.PointerTo(typeOfAdvice).NumMethod() != 0 {
		t.Fatal("shadow advice exposes behavior instead of data")
	}
	assertDataOnlyType(t, typeOfAdvice, "ShadowAdvice", make(map[reflect.Type]bool))
}

func assertDataOnlyType(t *testing.T, valueType reflect.Type, path string, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[valueType] {
		return
	}
	seen[valueType] = true
	switch valueType.Kind() {
	case reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		t.Fatalf("%s exposes execution-capable type %s", path, valueType)
	case reflect.Pointer, reflect.Slice, reflect.Array:
		assertDataOnlyType(t, valueType.Elem(), path, seen)
	case reflect.Map:
		assertDataOnlyType(t, valueType.Key(), path, seen)
		assertDataOnlyType(t, valueType.Elem(), path, seen)
	case reflect.Struct:
		for index := 0; index < valueType.NumField(); index++ {
			field := valueType.Field(index)
			assertDataOnlyType(t, field.Type, path+"."+field.Name, seen)
		}
	}
}

func recommendationPolicy() scoring.RecommendationPolicy {
	return scoring.RecommendationPolicy{
		Version:                    scoring.PolicyVersionM2ShadowV1,
		JoinThreshold:              75,
		ExitThreshold:              55,
		RequiredConsecutiveWindows: 2,
		HardGates:                  scoring.DefaultHardGatePolicy(),
	}
}

func passingGates() scoring.HardGateInput {
	return scoring.HardGateInput{
		Authenticity:     scoring.AuthenticityConsistent,
		CredentialValid:  scoring.CheckPass,
		BalanceAvailable: scoring.CheckPass,
		MajorRiskAbsent:  scoring.CheckPass,
	}
}

func readyCombined(t *testing.T, value float64) scoring.CombinedWindowScore {
	t.Helper()
	combined, err := scoring.CombineWindowScores([]scoring.WindowScoreInput{
		windowScore(scoring.Window15Minutes, value),
		windowScore(scoring.Window1Hour, value),
		windowScore(scoring.Window24Hours, value),
	})
	if err != nil {
		t.Fatal(err)
	}
	return combined
}

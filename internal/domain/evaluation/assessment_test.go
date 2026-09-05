package evaluation

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testTargetSubject    = ModelSubject{SupplierID: uuid.MustParse("10000000-0000-0000-0000-000000000001"), Model: "model-a"}
	testReferenceSubject = ModelSubject{SupplierID: uuid.MustParse("20000000-0000-0000-0000-000000000002"), Model: "model-a"}
)

func testReference(fingerprint Fingerprint, now time.Time) *ModelReference {
	return &ModelReference{
		ID:          "reference-1",
		Revision:    1,
		Trust:       ReferenceOfficial,
		Source:      testReferenceSubject,
		ExpiresAt:   now.Add(time.Hour),
		Fingerprint: fingerprint,
	}
}

func TestAssessAuthenticityRequiresCurrentTrustedReference(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	target := testFingerprint("a", 10, now)
	policy := DefaultAuthenticityPolicy()

	assessment, err := AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonNoTrustedReference)

	community := testReference(testFingerprint("a", 10, now), now)
	community.Trust = ReferenceCommunity
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: community, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonNoTrustedReference)

	expired := testReference(testFingerprint("a", 10, now), now)
	expired.ExpiresAt = now
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: expired, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonReferenceExpired)

	operatorTrusted := testReference(testFingerprint("a", 10, now), now)
	operatorTrusted.Trust = ReferenceOperatorTrusted
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: operatorTrusted, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictConsistent, ReasonDistanceMatch)
}

func TestAssessAuthenticityRejectsSelfReferenceAndFutureReference(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	target := testFingerprint("a", 10, now)
	reference := testReference(testFingerprint("a", 10, now), now)
	reference.Source = testTargetSubject
	_, err := AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, DefaultAuthenticityPolicy())
	if !errors.Is(err, ErrInvalidAssessmentInput) {
		t.Fatalf("got %v, want invalid self reference", err)
	}

	reference = testReference(testFingerprint("a", 10, now.Add(time.Second)), now)
	_, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, DefaultAuthenticityPolicy())
	if !errors.Is(err, ErrInvalidAssessmentInput) {
		t.Fatalf("got %v, want invalid future reference", err)
	}
}

func TestAssessAuthenticityRejectsProtocolMismatchAndMissingSamples(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	policy := DefaultAuthenticityPolicy()
	referenceFingerprint := testFingerprint("a", 10, now)
	reference := testReference(referenceFingerprint, now)
	target := testFingerprint("a", 10, now)
	target.ProtocolVersion = "other_protocol"

	assessment, err := AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonProtocolMismatch)

	target = testFingerprint("a", 10, now)
	delete(target.Cells, CellColorZH)
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonInsufficientSamples)
}

func TestAssessAuthenticityRequiresStableSelfChecks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	policy := DefaultAuthenticityPolicy()
	referenceFingerprint := testFingerprint("a", 10, now)
	reference := testReference(referenceFingerprint, now)
	target := testFingerprint("a", 10, now)
	target.Stability.Measured = false

	assessment, err := AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonSelfCheckMissing)

	target = testFingerprint("a", 10, now)
	target.Stability.Distance = math.Nextafter(policy.MaximumSelfDistance, 1)
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonSelfUnstable)

	target = testFingerprint("a", 10, now)
	reference.Fingerprint.Stability.Distance = math.Nextafter(policy.MaximumSelfDistance, 1)
	assessment, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInsufficient, ReasonSelfUnstable)
}

func TestAssessAuthenticityReturnsConsistentForMatchingFingerprint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	referenceFingerprint := testFingerprint("a", 10, now)
	target := testFingerprint("a", 10, now)
	target.RunID = "target-run"

	assessment, err := AssessAuthenticity(AssessmentInput{
		Subject:    testTargetSubject,
		Target:     target,
		Reference:  testReference(referenceFingerprint, now),
		AssessedAt: now,
	}, DefaultAuthenticityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictConsistent, ReasonDistanceMatch)
	if assessment.Confidence != ConfidenceMedium || !assessment.Comparison.Comparable {
		t.Fatalf("unexpected consistent assessment: %#v", assessment)
	}
}

func TestAssessAuthenticityRequiresIndependentDelayedMismatchConfirmation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	policy := DefaultAuthenticityPolicy()
	referenceFingerprint := testFingerprint("a", 10, now.Add(-time.Hour))
	reference := testReference(referenceFingerprint, now)
	target := testFingerprint("b", 10, now)
	target.RunID = "target-run-2"
	target.Seed = 2

	input := AssessmentInput{Subject: testTargetSubject, Target: target, Reference: reference, AssessedAt: now}
	assessment, err := AssessAuthenticity(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictSuspicious, ReasonDistanceMismatchUnconfirmed)

	input.PriorMismatch = &MismatchEvidence{
		Subject:           testTargetSubject,
		TargetRunID:       "target-run-1",
		TargetSeed:        1,
		ObservedAt:        now.Add(-policy.MismatchConfirmationDelay),
		ReferenceID:       reference.ID,
		ReferenceRevision: reference.Revision,
		ProtocolVersion:   ProtocolSingleTokenJSDV1,
		Distance:          1,
		SelfDistance:      0.1,
	}
	assessment, err = AssessAuthenticity(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictInconsistent, ReasonDistanceMismatchConfirmed)
	if assessment.Confidence != ConfidenceHigh {
		t.Fatalf("got confidence %q", assessment.Confidence)
	}

	input.PriorMismatch.ObservedAt = now.Add(-policy.MismatchConfirmationDelay + time.Second)
	assessment, err = AssessAuthenticity(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictSuspicious, ReasonDistanceMismatchUnconfirmed)

	input.PriorMismatch.ObservedAt = now.Add(-policy.MismatchConfirmationDelay)
	input.PriorMismatch.TargetSeed = target.Seed
	assessment, err = AssessAuthenticity(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictSuspicious, ReasonDistanceMismatchUnconfirmed)

	input.PriorMismatch.TargetSeed = 1
	input.PriorMismatch.Subject = ModelSubject{SupplierID: uuid.MustParse("30000000-0000-0000-0000-000000000003"), Model: testTargetSubject.Model}
	assessment, err = AssessAuthenticity(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertAssessment(t, assessment, VerdictSuspicious, ReasonDistanceMismatchUnconfirmed)
}

func TestDistanceClassificationBoundaries(t *testing.T) {
	t.Parallel()
	policy := DefaultAuthenticityPolicy()
	tests := []struct {
		name        string
		distance    float64
		confirmed   bool
		wantVerdict Verdict
		wantReason  Reason
	}{
		{name: "match boundary", distance: 0.25, wantVerdict: VerdictConsistent, wantReason: ReasonDistanceMatch},
		{name: "above match", distance: math.Nextafter(0.25, 1), wantVerdict: VerdictSuspicious, wantReason: ReasonDistanceUncertain},
		{name: "uncertain boundary", distance: 0.35, wantVerdict: VerdictSuspicious, wantReason: ReasonDistanceUncertain},
		{name: "single mismatch", distance: math.Nextafter(0.35, 1), wantVerdict: VerdictSuspicious, wantReason: ReasonDistanceMismatchUnconfirmed},
		{name: "confirmed mismatch", distance: math.Nextafter(0.35, 1), confirmed: true, wantVerdict: VerdictInconsistent, wantReason: ReasonDistanceMismatchConfirmed},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verdict, _, reason := classifyDistance(test.distance, test.confirmed, policy)
			if verdict != test.wantVerdict || reason != test.wantReason {
				t.Fatalf("got verdict=%q reason=%q", verdict, reason)
			}
		})
	}
}

func TestAssessAuthenticityRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	target := testFingerprint("a", 10, now.Add(time.Second))
	_, err := AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, AssessedAt: now}, DefaultAuthenticityPolicy())
	if !errors.Is(err, ErrInvalidAssessmentInput) {
		t.Fatalf("got %v, want invalid input", err)
	}

	policy := DefaultAuthenticityPolicy()
	policy.MatchMaximum = policy.UncertainMaximum
	_, err = AssessAuthenticity(AssessmentInput{Subject: testTargetSubject, Target: target, AssessedAt: now}, policy)
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("got %v, want invalid policy", err)
	}
}

func assertAssessment(t *testing.T, assessment AuthenticityAssessment, verdict Verdict, reason Reason) {
	t.Helper()
	if assessment.Verdict != verdict || assessment.Reason != reason {
		t.Fatalf("got verdict=%q reason=%q, want verdict=%q reason=%q", assessment.Verdict, assessment.Reason, verdict, reason)
	}
}

package evaluation

import (
	"errors"
	"math"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrInvalidPolicy          = errors.New("authenticity policy is invalid")
	ErrInvalidAssessmentInput = errors.New("authenticity assessment input is invalid")
)

func AssessAuthenticity(input AssessmentInput, policy AuthenticityPolicy) (AuthenticityAssessment, error) {
	assessment := AuthenticityAssessment{
		PolicyVersion: policy.Version,
		Verdict:       VerdictInsufficient,
		Confidence:    ConfidenceNone,
		AssessedAt:    input.AssessedAt,
	}
	if err := validatePolicy(policy); err != nil {
		return AuthenticityAssessment{}, err
	}
	if !validSubject(input.Subject) || input.AssessedAt.IsZero() || input.Target.CollectedAt.IsZero() || input.Target.CollectedAt.After(input.AssessedAt) {
		return AuthenticityAssessment{}, ErrInvalidAssessmentInput
	}
	if input.Reference == nil || !input.Reference.CanEstablishAuthenticity() {
		assessment.Reason = ReasonNoTrustedReference
		return assessment, nil
	}
	reference := *input.Reference
	if reference.ID == "" || reference.Revision < 1 || !validSubject(reference.Source) || reference.Source == input.Subject || reference.ExpiresAt.IsZero() || reference.Fingerprint.CollectedAt.IsZero() || reference.Fingerprint.CollectedAt.After(input.AssessedAt) {
		return AuthenticityAssessment{}, ErrInvalidAssessmentInput
	}
	if !input.AssessedAt.Before(reference.ExpiresAt) {
		assessment.Reason = ReasonReferenceExpired
		return assessment, nil
	}

	comparison, err := CompareFingerprints(reference.Fingerprint, input.Target, policy)
	if err != nil {
		return AuthenticityAssessment{}, err
	}
	assessment.Comparison = comparison
	if !comparison.Comparable {
		assessment.Reason = comparison.Reason
		return assessment, nil
	}
	if !input.Target.Stability.Measured || !reference.Fingerprint.Stability.Measured {
		assessment.Reason = ReasonSelfCheckMissing
		return assessment, nil
	}
	if !validDistance(input.Target.Stability.Distance) || !validDistance(reference.Fingerprint.Stability.Distance) {
		return AuthenticityAssessment{}, ErrInvalidFingerprint
	}
	if input.Target.Stability.Distance > policy.MaximumSelfDistance || reference.Fingerprint.Stability.Distance > policy.MaximumSelfDistance {
		assessment.Reason = ReasonSelfUnstable
		return assessment, nil
	}

	assessment.Verdict, assessment.Confidence, assessment.Reason = classifyDistance(
		comparison.MeanDistance,
		confirmedIndependentMismatch(input, reference, policy),
		policy,
	)
	return assessment, nil
}

func classifyDistance(distance float64, confirmed bool, policy AuthenticityPolicy) (Verdict, Confidence, Reason) {
	if distance <= policy.MatchMaximum {
		return VerdictConsistent, ConfidenceMedium, ReasonDistanceMatch
	}
	if distance <= policy.UncertainMaximum {
		return VerdictSuspicious, ConfidenceLow, ReasonDistanceUncertain
	}
	if confirmed {
		return VerdictInconsistent, ConfidenceHigh, ReasonDistanceMismatchConfirmed
	}
	return VerdictSuspicious, ConfidenceLow, ReasonDistanceMismatchUnconfirmed
}

func confirmedIndependentMismatch(input AssessmentInput, reference ModelReference, policy AuthenticityPolicy) bool {
	prior := input.PriorMismatch
	if prior == nil || !validDistance(prior.Distance) || !validDistance(prior.SelfDistance) {
		return false
	}
	if prior.Distance <= policy.UncertainMaximum || prior.SelfDistance > policy.MaximumSelfDistance {
		return false
	}
	if prior.ReferenceID != reference.ID || prior.ReferenceRevision != reference.Revision || prior.ProtocolVersion != input.Target.ProtocolVersion {
		return false
	}
	if prior.Subject != input.Subject {
		return false
	}
	if prior.TargetRunID == "" || input.Target.RunID == "" || prior.TargetRunID == input.Target.RunID || prior.TargetSeed == input.Target.Seed {
		return false
	}
	return !prior.ObservedAt.IsZero() && input.Target.CollectedAt.Sub(prior.ObservedAt) >= policy.MismatchConfirmationDelay
}

func validatePolicy(policy AuthenticityPolicy) error {
	if policy.Version == "" || policy.MinimumSamplesPerCell < 1 || policy.MismatchConfirmationDelay < 0 {
		return ErrInvalidPolicy
	}
	if !validDistance(policy.MatchMaximum) || !validDistance(policy.UncertainMaximum) || !validDistance(policy.MaximumSelfDistance) {
		return ErrInvalidPolicy
	}
	if policy.MatchMaximum >= policy.UncertainMaximum {
		return ErrInvalidPolicy
	}
	return nil
}

func validDistance(distance float64) bool {
	return !math.IsNaN(distance) && !math.IsInf(distance, 0) && distance >= 0 && distance <= 1
}

func validSubject(subject ModelSubject) bool {
	return subject.SupplierID != uuid.Nil && strings.TrimSpace(subject.Model) != ""
}

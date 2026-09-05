package scoring

type CheckState string

const (
	CheckPending CheckState = "pending"
	CheckPass    CheckState = "pass"
	CheckFail    CheckState = "fail"
)

type GateReason string

const (
	GateAuthenticityMismatch GateReason = "authenticity_mismatch"
	GateCredentialInvalid    GateReason = "credential_invalid"
	GateBalanceUnavailable   GateReason = "balance_unavailable"
	GateConsecutiveFailures  GateReason = "consecutive_failures"
	GateMajorRisk            GateReason = "major_risk"
)

type HardGateInput struct {
	Authenticity                  Authenticity
	CredentialValid               CheckState
	BalanceAvailable              CheckState
	MajorRiskAbsent               CheckState
	AttributedConsecutiveFailures uint64
}

type HardGatePolicy struct {
	ConsecutiveFailureLimit uint64
}

func DefaultHardGatePolicy() HardGatePolicy {
	return HardGatePolicy{ConsecutiveFailureLimit: DefaultConsecutiveFailureLimit}
}

type HardGateResult struct {
	Excluded bool
	Reasons  []GateReason
	Pending  []GateReason
}

// EvaluateHardGates preserves every confirmed and pending gate reason in a
// stable business order.
func EvaluateHardGates(input HardGateInput, policy HardGatePolicy) (HardGateResult, error) {
	if policy.ConsecutiveFailureLimit == 0 {
		return HardGateResult{}, ErrInvalidRules
	}
	result := HardGateResult{}

	switch input.Authenticity {
	case "", AuthenticityPending:
		result.Pending = append(result.Pending, GateAuthenticityMismatch)
	case AuthenticityInconsistent:
		result.Reasons = append(result.Reasons, GateAuthenticityMismatch)
	case AuthenticityConsistent, AuthenticitySuspicious:
	default:
		return HardGateResult{}, ErrInvalidMetric
	}

	if err := collectCheck(input.CredentialValid, GateCredentialInvalid, &result); err != nil {
		return HardGateResult{}, err
	}
	if err := collectCheck(input.BalanceAvailable, GateBalanceUnavailable, &result); err != nil {
		return HardGateResult{}, err
	}
	if input.AttributedConsecutiveFailures >= policy.ConsecutiveFailureLimit {
		result.Reasons = append(result.Reasons, GateConsecutiveFailures)
	}
	if err := collectCheck(input.MajorRiskAbsent, GateMajorRisk, &result); err != nil {
		return HardGateResult{}, err
	}

	result.Excluded = len(result.Reasons) > 0
	return result, nil
}

func collectCheck(state CheckState, reason GateReason, result *HardGateResult) error {
	switch state {
	case "", CheckPending:
		result.Pending = append(result.Pending, reason)
	case CheckPass:
	case CheckFail:
		result.Reasons = append(result.Reasons, reason)
	default:
		return ErrInvalidMetric
	}
	return nil
}

package scoring

type RecommendationPolicy struct {
	Version                    string
	JoinThreshold              Score
	ExitThreshold              Score
	RequiredConsecutiveWindows uint64
	HardGates                  HardGatePolicy
}

type RecommendationInput struct {
	AutoKind               AutoKind
	CurrentMember          bool
	Windows                CombinedWindowScore
	HardGates              HardGateInput
	ConsecutiveJoinWindows uint64
	ConsecutiveExitWindows uint64
}

type AdviceReason string

const (
	AdviceHardGateFailed          AdviceReason = "hard_gate_failed"
	AdviceHardGatePending         AdviceReason = "hard_gate_pending"
	AdviceEvidenceNotReady        AdviceReason = "evidence_not_ready"
	AdviceJoinThresholdMet        AdviceReason = "join_threshold_met"
	AdviceJoinConfirmationPending AdviceReason = "join_confirmation_pending"
	AdviceMemberWithinExitLine    AdviceReason = "member_within_exit_line"
	AdviceExitThresholdMet        AdviceReason = "exit_threshold_met"
	AdviceExitConfirmationPending AdviceReason = "exit_confirmation_pending"
	AdviceBelowJoinThreshold      AdviceReason = "below_join_threshold"
)

type ShadowAdvice struct {
	Action           AdviceAction    `json:"action"`
	PolicyVersion    string          `json:"policy_version"`
	AutoKind         AutoKind        `json:"strategy_kind"`
	CurrentMember    bool            `json:"current_member"`
	CompositeScore   *Score          `json:"score,omitempty"`
	DimensionScores  DimensionScores `json:"dimension_scores"`
	StrategyWeights  AutoWeights     `json:"strategy_weights"`
	WindowWeights    []WindowWeight  `json:"window_weights"`
	Confidence       Confidence      `json:"confidence"`
	Reasons          []AdviceReason  `json:"reasons"`
	HardReasons      []GateReason    `json:"hard_reasons"`
	PendingHardGates []GateReason    `json:"pending_hard_gates"`
	WindowIssues     []WindowIssue   `json:"window_issues"`
}

// Recommend returns an explainable shadow action. It evaluates hard gates
// before any score, and its output contains no way to mutate a real route.
func Recommend(input RecommendationInput, policy RecommendationPolicy) (ShadowAdvice, error) {
	gates, err := EvaluateHardGates(input.HardGates, policy.HardGates)
	if err != nil {
		return ShadowAdvice{}, err
	}
	advice := ShadowAdvice{
		AutoKind:         input.AutoKind,
		PolicyVersion:    policy.Version,
		CurrentMember:    input.CurrentMember,
		DimensionScores:  input.Windows.Scores,
		WindowWeights:    append([]WindowWeight(nil), input.Windows.EffectiveWeights...),
		Confidence:       input.Windows.Confidence,
		HardReasons:      append([]GateReason(nil), gates.Reasons...),
		PendingHardGates: append([]GateReason(nil), gates.Pending...),
		WindowIssues:     append([]WindowIssue(nil), input.Windows.Issues...),
	}

	// Confirmed hard failures intentionally bypass score and evidence validation.
	if gates.Excluded {
		advice.Action = AdviceExclude
		advice.Confidence = ConfidenceHigh
		advice.Reasons = []AdviceReason{AdviceHardGateFailed}
		return advice, nil
	}
	if input.Windows.PolicyVersion != policy.Version {
		return ShadowAdvice{}, ErrInvalidRecommendation
	}
	weights, err := FixedAutoWeights(policy.Version, input.AutoKind)
	if err != nil {
		return ShadowAdvice{}, err
	}
	advice.StrategyWeights = weights
	if len(gates.Pending) > 0 {
		advice.Action = AdviceWatch
		advice.Confidence = ConfidenceInsufficient
		advice.Reasons = []AdviceReason{AdviceHardGatePending}
		return advice, nil
	}
	if err := validateRecommendationPolicy(policy); err != nil {
		return ShadowAdvice{}, err
	}
	if !input.Windows.Available || !input.Windows.DecisionReady {
		advice.Action = AdviceWatch
		advice.Reasons = []AdviceReason{AdviceEvidenceNotReady}
		return advice, nil
	}

	composite, err := CompositeScore(input.Windows.Scores, weights)
	if err != nil {
		return ShadowAdvice{}, err
	}
	advice.CompositeScore = &composite
	if input.CurrentMember {
		return adviseCurrentMember(advice, input, policy, composite), nil
	}
	return adviseCandidate(advice, input, policy, composite), nil
}

func validateRecommendationPolicy(policy RecommendationPolicy) error {
	if !validScore(policy.JoinThreshold) || !validScore(policy.ExitThreshold) ||
		policy.JoinThreshold <= policy.ExitThreshold || policy.RequiredConsecutiveWindows == 0 {
		return ErrInvalidRecommendation
	}
	return nil
}

func adviseCurrentMember(advice ShadowAdvice, input RecommendationInput, policy RecommendationPolicy, composite Score) ShadowAdvice {
	if composite >= policy.ExitThreshold {
		advice.Action = AdviceKeep
		advice.Reasons = []AdviceReason{AdviceMemberWithinExitLine}
		return advice
	}
	if input.ConsecutiveExitWindows >= policy.RequiredConsecutiveWindows {
		advice.Action = AdviceExit
		advice.Reasons = []AdviceReason{AdviceExitThresholdMet}
		return advice
	}
	advice.Action = AdviceWatch
	advice.Reasons = []AdviceReason{AdviceExitConfirmationPending}
	return advice
}

func adviseCandidate(advice ShadowAdvice, input RecommendationInput, policy RecommendationPolicy, composite Score) ShadowAdvice {
	if composite < policy.JoinThreshold {
		advice.Action = AdviceWatch
		advice.Reasons = []AdviceReason{AdviceBelowJoinThreshold}
		return advice
	}
	if input.ConsecutiveJoinWindows >= policy.RequiredConsecutiveWindows {
		advice.Action = AdviceJoin
		advice.Reasons = []AdviceReason{AdviceJoinThresholdMet}
		return advice
	}
	advice.Action = AdviceWatch
	advice.Reasons = []AdviceReason{AdviceJoinConfirmationPending}
	return advice
}

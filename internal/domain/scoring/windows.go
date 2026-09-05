package scoring

type WindowIssueReason string

const (
	WindowMissing    WindowIssueReason = "window_missing"
	WindowIncomplete WindowIssueReason = "window_incomplete"
)

type WindowScoreInput struct {
	Window   Window
	Complete bool
	Scores   DimensionScores
	Evidence EvidenceInput
}

type WindowIssue struct {
	Window         Window            `json:"window"`
	WindowReason   WindowIssueReason `json:"window_reason,omitempty"`
	EvidenceReason EvidenceReason    `json:"evidence_reason,omitempty"`
	Detail         string            `json:"detail,omitempty"`
}

type CombinedWindowScore struct {
	PolicyVersion    string
	Available        bool
	Scores           DimensionScores
	EffectiveWeights []WindowWeight
	Confidence       Confidence
	DecisionReady    bool
	Issues           []WindowIssue
}

// CombineWindowScores uses the fixed 15m/1h/24h weights. Missing or unusable
// windows are omitted, and their original weights are redistributed only among
// the remaining complete windows.
func CombineWindowScores(inputs []WindowScoreInput) (CombinedWindowScore, error) {
	configured := DefaultWindowWeights()
	byWindow := make(map[Window]WindowScoreInput, len(inputs))
	for _, input := range inputs {
		if _, exists := byWindow[input.Window]; exists || !knownWindow(input.Window) {
			return CombinedWindowScore{}, ErrInvalidWindow
		}
		byWindow[input.Window] = input
	}

	type usableWindow struct {
		input  WindowScoreInput
		weight float64
	}
	usable := make([]usableWindow, 0, len(configured))
	missingCount := 0
	decisionBlocked := false
	lowestConfidenceRank := 3
	result := CombinedWindowScore{
		PolicyVersion: PolicyVersionM2ShadowV1,
		Confidence:    ConfidenceInsufficient,
	}

	for _, configuredWindow := range configured {
		input, exists := byWindow[configuredWindow.Window]
		if !exists {
			missingCount++
			decisionBlocked = true
			result.Issues = append(result.Issues, WindowIssue{
				Window:       configuredWindow.Window,
				WindowReason: WindowMissing,
			})
			continue
		}
		assessment, err := AssessEvidence(input.Evidence)
		if err != nil {
			return CombinedWindowScore{}, err
		}
		for _, issue := range assessment.Issues {
			result.Issues = append(result.Issues, WindowIssue{
				Window:         input.Window,
				EvidenceReason: issue.Reason,
				Detail:         issue.Detail,
			})
		}
		if !input.Complete || !assessment.UsableForScoring {
			missingCount++
			decisionBlocked = true
			if !input.Complete {
				result.Issues = append(result.Issues, WindowIssue{
					Window:       input.Window,
					WindowReason: WindowIncomplete,
				})
			}
			continue
		}
		if err := validateDimensionScores(input.Scores); err != nil {
			return CombinedWindowScore{}, err
		}
		rank, _ := confidenceRank(assessment.Confidence)
		if rank < lowestConfidenceRank {
			lowestConfidenceRank = rank
		}
		if !assessment.DecisionReady {
			decisionBlocked = true
		}
		usable = append(usable, usableWindow{
			input:  input,
			weight: configuredWindow.Weight,
		})
	}

	if len(usable) == 0 {
		return result, nil
	}
	weightTotal := 0.0
	for _, window := range usable {
		weightTotal += window.weight
	}
	for _, window := range usable {
		weight := window.weight / weightTotal
		result.EffectiveWeights = append(result.EffectiveWeights, WindowWeight{
			Window: window.input.Window,
			Weight: weight,
		})
		result.Scores.Price += Score(window.input.Scores.Price.Float64() * weight)
		result.Scores.Latency += Score(window.input.Scores.Latency.Float64() * weight)
		result.Scores.SLA += Score(window.input.Scores.SLA.Float64() * weight)
		result.Scores.Quality += Score(window.input.Scores.Quality.Float64() * weight)
	}
	result.Scores.Price = clampScore(result.Scores.Price.Float64())
	result.Scores.Latency = clampScore(result.Scores.Latency.Float64())
	result.Scores.SLA = clampScore(result.Scores.SLA.Float64())
	result.Scores.Quality = clampScore(result.Scores.Quality.Float64())

	result.Available = true
	result.Confidence = lowerConfidence(confidenceFromRank(lowestConfidenceRank), missingCount)
	confidenceRank, _ := confidenceRank(result.Confidence)
	result.DecisionReady = !decisionBlocked && confidenceRank >= 2
	return result, nil
}

func knownWindow(window Window) bool {
	switch window {
	case Window15Minutes, Window1Hour, Window24Hours:
		return true
	default:
		return false
	}
}

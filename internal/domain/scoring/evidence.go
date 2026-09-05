package scoring

import "strings"

type EvidenceReason string

const (
	EvidenceInsufficientSamples EvidenceReason = "insufficient_samples"
	EvidenceCollectionGap       EvidenceReason = "collection_gap"
	EvidenceAttributionPending  EvidenceReason = "attribution_pending"
	EvidenceFactorMissing       EvidenceReason = "confidence_factor_missing"
	EvidenceFactorInsufficient  EvidenceReason = "confidence_factor_insufficient"
	EvidenceLowConfidence       EvidenceReason = "low_confidence"
)

type ConfidenceFactorName string

const (
	FactorFreshness          ConfidenceFactorName = "freshness"
	FactorCollectionCoverage ConfidenceFactorName = "collection_coverage"
	FactorTimingCoverage     ConfidenceFactorName = "timing_coverage"
	FactorAttribution        ConfidenceFactorName = "attribution"
	FactorSupplierMapping    ConfidenceFactorName = "supplier_mapping"
	FactorRealTraffic        ConfidenceFactorName = "real_traffic"
	FactorActiveProbe        ConfidenceFactorName = "active_probe"
	FactorAuthenticity       ConfidenceFactorName = "authenticity"
)

var requiredConfidenceFactors = [...]ConfidenceFactorName{
	FactorFreshness,
	FactorCollectionCoverage,
	FactorTimingCoverage,
	FactorAttribution,
	FactorSupplierMapping,
	FactorRealTraffic,
	FactorActiveProbe,
	FactorAuthenticity,
}

type ConfidenceFactor struct {
	Name  ConfidenceFactorName `json:"name"`
	Level Confidence           `json:"level"`
}

// EvidenceInput carries the already-classified freshness and coverage factors
// for one scored window. Their minimum determines confidence.
type EvidenceInput struct {
	SampleCount        uint64
	MinimumSamples     uint64
	Factors            []ConfidenceFactor
	CollectionGaps     []string
	AttributionPending bool
}

type EvidenceIssue struct {
	Reason EvidenceReason
	Detail string
}

type EvidenceAssessment struct {
	Confidence       Confidence
	UsableForScoring bool
	DecisionReady    bool
	SampleCount      uint64
	MinimumSamples   uint64
	Issues           []EvidenceIssue
}

// AssessEvidence leaves low-confidence statistics displayable while keeping
// samples, collection gaps, and pending attribution out of definite advice.
func AssessEvidence(input EvidenceInput) (EvidenceAssessment, error) {
	minimumSamples := input.MinimumSamples
	if minimumSamples == 0 {
		minimumSamples = DefaultMinimumSamples
	}
	assessment := EvidenceAssessment{
		Confidence:     ConfidenceHigh,
		SampleCount:    input.SampleCount,
		MinimumSamples: minimumSamples,
	}
	blocked := false

	if input.SampleCount < minimumSamples {
		blocked = true
		assessment.Issues = append(assessment.Issues, EvidenceIssue{Reason: EvidenceInsufficientSamples})
	}
	for _, gap := range input.CollectionGaps {
		gap = strings.TrimSpace(gap)
		if gap == "" {
			return EvidenceAssessment{}, ErrInvalidEvidence
		}
		blocked = true
		assessment.Issues = append(assessment.Issues, EvidenceIssue{Reason: EvidenceCollectionGap, Detail: gap})
	}
	if input.AttributionPending {
		blocked = true
		assessment.Issues = append(assessment.Issues, EvidenceIssue{Reason: EvidenceAttributionPending})
	}

	seen := make(map[ConfidenceFactorName]bool, len(input.Factors))
	lowestRank := 3
	for _, factor := range input.Factors {
		name := ConfidenceFactorName(strings.TrimSpace(string(factor.Name)))
		rank, ok := confidenceRank(factor.Level)
		if !knownConfidenceFactor(name) || !ok || seen[name] {
			return EvidenceAssessment{}, ErrInvalidEvidence
		}
		seen[name] = true
		if rank < lowestRank {
			lowestRank = rank
		}
		if factor.Level == ConfidenceInsufficient {
			blocked = true
			assessment.Issues = append(assessment.Issues, EvidenceIssue{
				Reason: EvidenceFactorInsufficient,
				Detail: string(name),
			})
		}
	}
	for _, required := range requiredConfidenceFactors {
		if seen[required] {
			continue
		}
		blocked = true
		assessment.Issues = append(assessment.Issues, EvidenceIssue{
			Reason: EvidenceFactorMissing,
			Detail: string(required),
		})
	}
	assessment.Confidence = confidenceFromRank(lowestRank)
	if assessment.Confidence == ConfidenceLow {
		assessment.Issues = append(assessment.Issues, EvidenceIssue{Reason: EvidenceLowConfidence})
	}

	if blocked {
		assessment.Confidence = ConfidenceInsufficient
		return assessment, nil
	}
	assessment.UsableForScoring = true
	rank, _ := confidenceRank(assessment.Confidence)
	assessment.DecisionReady = rank >= 2
	return assessment, nil
}

func knownConfidenceFactor(name ConfidenceFactorName) bool {
	for _, required := range requiredConfidenceFactors {
		if name == required {
			return true
		}
	}
	return false
}

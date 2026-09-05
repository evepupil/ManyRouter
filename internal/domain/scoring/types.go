package scoring

import (
	"errors"
	"math"
)

const (
	PolicyVersionM2ShadowV1               = "m2-shadow-v1"
	DefaultMinimumSamples          uint64 = 50
	DefaultConsecutiveFailureLimit uint64 = 3
)

var (
	ErrInvalidHistogram      = errors.New("latency histogram is invalid")
	ErrNoSamples             = errors.New("latency histogram has no samples")
	ErrInvalidMetric         = errors.New("scoring metric is invalid")
	ErrInvalidRules          = errors.New("scoring rules are invalid")
	ErrInvalidEvidence       = errors.New("scoring evidence is invalid")
	ErrInvalidWindow         = errors.New("scoring window is invalid")
	ErrUnknownAutoKind       = errors.New("auto kind is unknown")
	ErrInvalidRecommendation = errors.New("recommendation input is invalid")
)

type Score float64

func (s Score) Float64() float64 {
	return float64(s)
}

func validScore(score Score) bool {
	value := score.Float64()
	return finite(value) && value >= 0 && value <= 100
}

type Dimension string

const (
	DimensionPrice   Dimension = "price"
	DimensionLatency Dimension = "latency"
	DimensionSLA     Dimension = "sla"
	DimensionQuality Dimension = "quality"
)

type DimensionScores struct {
	Price   Score `json:"price"`
	Latency Score `json:"latency"`
	SLA     Score `json:"sla"`
	Quality Score `json:"quality"`
}

func validateDimensionScores(scores DimensionScores) error {
	if !validScore(scores.Price) || !validScore(scores.Latency) || !validScore(scores.SLA) || !validScore(scores.Quality) {
		return ErrInvalidMetric
	}
	return nil
}

type Confidence string

const (
	ConfidenceInsufficient Confidence = "insufficient"
	ConfidenceLow          Confidence = "low"
	ConfidenceMedium       Confidence = "medium"
	ConfidenceHigh         Confidence = "high"
)

func confidenceRank(confidence Confidence) (int, bool) {
	switch confidence {
	case ConfidenceInsufficient:
		return 0, true
	case ConfidenceLow:
		return 1, true
	case ConfidenceMedium:
		return 2, true
	case ConfidenceHigh:
		return 3, true
	default:
		return 0, false
	}
}

func confidenceFromRank(rank int) Confidence {
	switch {
	case rank >= 3:
		return ConfidenceHigh
	case rank == 2:
		return ConfidenceMedium
	case rank == 1:
		return ConfidenceLow
	default:
		return ConfidenceInsufficient
	}
}

func lowerConfidence(confidence Confidence, steps int) Confidence {
	rank, ok := confidenceRank(confidence)
	if !ok {
		return ConfidenceInsufficient
	}
	return confidenceFromRank(rank - steps)
}

type Window string

const (
	Window15Minutes Window = "15m"
	Window1Hour     Window = "1h"
	Window24Hours   Window = "24h"
)

type WindowWeight struct {
	Window Window  `json:"window"`
	Weight float64 `json:"weight"`
}

// DefaultWindowWeights returns the immutable m2-shadow-v1 time emphasis.
func DefaultWindowWeights() []WindowWeight {
	return []WindowWeight{
		{Window: Window15Minutes, Weight: 0.50},
		{Window: Window1Hour, Weight: 0.30},
		{Window: Window24Hours, Weight: 0.20},
	}
}

type AutoKind string

const (
	AutoLowestPrice AutoKind = "lowest_price"
	AutoLowLatency  AutoKind = "low_latency"
	AutoHighSLA     AutoKind = "high_sla"
	AutoHighQuality AutoKind = "high_quality"
	AutoBalanced    AutoKind = "balanced"
)

type AutoWeights struct {
	Price   float64 `json:"price"`
	Latency float64 `json:"latency"`
	SLA     float64 `json:"sla"`
	Quality float64 `json:"quality"`
}

func FixedAutoWeights(policyVersion string, kind AutoKind) (AutoWeights, error) {
	if policyVersion != PolicyVersionM2ShadowV1 {
		return AutoWeights{}, ErrInvalidRules
	}
	switch kind {
	case AutoLowestPrice:
		return AutoWeights{Price: 0.55, Latency: 0.15, SLA: 0.15, Quality: 0.15}, nil
	case AutoLowLatency:
		return AutoWeights{Price: 0.15, Latency: 0.55, SLA: 0.20, Quality: 0.10}, nil
	case AutoHighSLA:
		return AutoWeights{Price: 0.10, Latency: 0.20, SLA: 0.60, Quality: 0.10}, nil
	case AutoHighQuality:
		return AutoWeights{Price: 0.10, Latency: 0.15, SLA: 0.15, Quality: 0.60}, nil
	case AutoBalanced:
		return AutoWeights{Price: 0.25, Latency: 0.25, SLA: 0.30, Quality: 0.20}, nil
	default:
		return AutoWeights{}, ErrUnknownAutoKind
	}
}

func CompositeScore(scores DimensionScores, weights AutoWeights) (Score, error) {
	if err := validateDimensionScores(scores); err != nil {
		return 0, err
	}
	if err := validateWeights(weights.Price, weights.Latency, weights.SLA, weights.Quality); err != nil {
		return 0, err
	}
	return clampScore(
		scores.Price.Float64()*weights.Price +
			scores.Latency.Float64()*weights.Latency +
			scores.SLA.Float64()*weights.SLA +
			scores.Quality.Float64()*weights.Quality,
	), nil
}

type AdviceAction string

const (
	AdviceJoin    AdviceAction = "join"
	AdviceKeep    AdviceAction = "keep"
	AdviceExit    AdviceAction = "exit"
	AdviceWatch   AdviceAction = "watch"
	AdviceExclude AdviceAction = "exclude"
)

func validateWeights(weights ...float64) error {
	total := 0.0
	for _, weight := range weights {
		if !finite(weight) || weight <= 0 || weight > 1 {
			return ErrInvalidRules
		}
		total += weight
	}
	if math.Abs(total-1) > 1e-9 {
		return ErrInvalidRules
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func clampScore(value float64) Score {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return Score(value)
}

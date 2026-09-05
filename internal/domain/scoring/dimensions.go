package scoring

import "github.com/shopspring/decimal"

// MetricRule maps one raw value onto 0..100 and assigns its dimension weight.
type MetricRule struct {
	Best   float64 `json:"best"`
	Worst  float64 `json:"worst"`
	Weight float64 `json:"weight"`
}

// ComponentScore records one normalized input used by a dimension.
type ComponentScore struct {
	Metric     string  `json:"metric"`
	RawValue   float64 `json:"raw_value"`
	Normalized Score   `json:"normalized"`
	Weight     float64 `json:"weight"`
}

// DimensionScore keeps the result and its component-level explanation.
type DimensionScore struct {
	Dimension  Dimension        `json:"dimension"`
	Score      Score            `json:"score"`
	Components []ComponentScore `json:"components"`
}

type PriceMetrics struct {
	CurrentCost          decimal.Decimal
	LowestCandidateCost  decimal.Decimal
	ChangesPerDay        uint64
	ChangeMagnitudeRatio float64
}

type PriceRules struct {
	RelativeCost    MetricRule `json:"relative_cost"`
	ChangesPerDay   MetricRule `json:"changes_per_day"`
	ChangeMagnitude MetricRule `json:"change_magnitude"`
}

func ScorePrice(metrics PriceMetrics, rules PriceRules) (DimensionScore, error) {
	if metrics.CurrentCost.IsNegative() || metrics.LowestCandidateCost.IsNegative() || (!metrics.CurrentCost.IsZero() && metrics.CurrentCost.LessThan(metrics.LowestCandidateCost)) {
		return DimensionScore{}, ErrInvalidMetric
	}
	relativeCost := rules.RelativeCost.Worst
	if !metrics.LowestCandidateCost.IsZero() {
		relativeCost = metrics.CurrentCost.Div(metrics.LowestCandidateCost).InexactFloat64()
	} else if metrics.CurrentCost.IsZero() {
		relativeCost = rules.RelativeCost.Best
	}
	if !finite(metrics.ChangeMagnitudeRatio) || metrics.ChangeMagnitudeRatio < 0 {
		return DimensionScore{}, ErrInvalidMetric
	}
	return scoreDimension(DimensionPrice, []metricInput{
		{name: "relative_cost", value: relativeCost, rule: rules.RelativeCost},
		{name: "changes_per_day", value: float64(metrics.ChangesPerDay), rule: rules.ChangesPerDay},
		{name: "change_magnitude_ratio", value: metrics.ChangeMagnitudeRatio, rule: rules.ChangeMagnitude},
	})
}

type LatencyMetrics struct {
	TTFTP50Millis     float64
	TTFTP95Millis     float64
	DurationP50Millis float64
	DurationP95Millis float64
	VariabilityRatio  float64
}

type LatencyRules struct {
	TTFTP50     MetricRule `json:"ttft_p50"`
	TTFTP95     MetricRule `json:"ttft_p95"`
	DurationP50 MetricRule `json:"duration_p50"`
	DurationP95 MetricRule `json:"duration_p95"`
	Variability MetricRule `json:"variability"`
}

func ScoreLatency(metrics LatencyMetrics, rules LatencyRules) (DimensionScore, error) {
	values := []float64{
		metrics.TTFTP50Millis,
		metrics.TTFTP95Millis,
		metrics.DurationP50Millis,
		metrics.DurationP95Millis,
		metrics.VariabilityRatio,
	}
	for _, value := range values {
		if !finite(value) || value < 0 {
			return DimensionScore{}, ErrInvalidMetric
		}
	}
	return scoreDimension(DimensionLatency, []metricInput{
		{name: "ttft_p50_ms", value: metrics.TTFTP50Millis, rule: rules.TTFTP50},
		{name: "ttft_p95_ms", value: metrics.TTFTP95Millis, rule: rules.TTFTP95},
		{name: "duration_p50_ms", value: metrics.DurationP50Millis, rule: rules.DurationP50},
		{name: "duration_p95_ms", value: metrics.DurationP95Millis, rule: rules.DurationP95},
		{name: "variability_ratio", value: metrics.VariabilityRatio, rule: rules.Variability},
	})
}

type SLAMetrics struct {
	AttemptSuccessRate   float64
	RateLimitRate        float64
	ConsecutiveFailures  uint64
	StreamCompletionRate float64
	RecoveryMillis       float64
	CapacityStability    float64
}

type SLARules struct {
	AttemptSuccess   MetricRule `json:"attempt_success"`
	RateLimit        MetricRule `json:"rate_limit"`
	ConsecutiveFails MetricRule `json:"consecutive_failures"`
	StreamCompletion MetricRule `json:"stream_completion"`
	Recovery         MetricRule `json:"recovery"`
	Capacity         MetricRule `json:"capacity"`
}

func ScoreSLA(metrics SLAMetrics, rules SLARules) (DimensionScore, error) {
	for _, ratio := range []float64{
		metrics.AttemptSuccessRate,
		metrics.RateLimitRate,
		metrics.StreamCompletionRate,
		metrics.CapacityStability,
	} {
		if !validRatio(ratio) {
			return DimensionScore{}, ErrInvalidMetric
		}
	}
	if !finite(metrics.RecoveryMillis) || metrics.RecoveryMillis < 0 {
		return DimensionScore{}, ErrInvalidMetric
	}
	return scoreDimension(DimensionSLA, []metricInput{
		{name: "attempt_success_rate", value: metrics.AttemptSuccessRate, rule: rules.AttemptSuccess},
		{name: "rate_limit_rate", value: metrics.RateLimitRate, rule: rules.RateLimit},
		{name: "consecutive_failures", value: float64(metrics.ConsecutiveFailures), rule: rules.ConsecutiveFails},
		{name: "stream_completion_rate", value: metrics.StreamCompletionRate, rule: rules.StreamCompletion},
		{name: "recovery_ms", value: metrics.RecoveryMillis, rule: rules.Recovery},
		{name: "capacity_stability", value: metrics.CapacityStability, rule: rules.Capacity},
	})
}

type Authenticity string

const (
	AuthenticityPending      Authenticity = "pending"
	AuthenticityConsistent   Authenticity = "consistent"
	AuthenticitySuspicious   Authenticity = "suspicious"
	AuthenticityInconsistent Authenticity = "inconsistent"
)

type QualityMetrics struct {
	Authenticity       Authenticity
	CapabilityRate     float64
	StabilityRate      float64
	EvidenceConfidence float64
}

type QualityRules struct {
	Authenticity       MetricRule         `json:"authenticity"`
	AuthenticityValues AuthenticityValues `json:"authenticity_values"`
	Capability         MetricRule         `json:"capability"`
	Stability          MetricRule         `json:"stability"`
	Evidence           MetricRule         `json:"evidence"`
}

type AuthenticityValues struct {
	Consistent   float64 `json:"consistent"`
	Suspicious   float64 `json:"suspicious"`
	Inconsistent float64 `json:"inconsistent"`
}

func ScoreQuality(metrics QualityMetrics, rules QualityRules) (DimensionScore, error) {
	authenticity, err := normalizedAuthenticity(metrics.Authenticity, rules.AuthenticityValues)
	if err != nil {
		return DimensionScore{}, err
	}
	for _, ratio := range []float64{metrics.CapabilityRate, metrics.StabilityRate, metrics.EvidenceConfidence} {
		if !validRatio(ratio) {
			return DimensionScore{}, ErrInvalidMetric
		}
	}
	return scoreDimension(DimensionQuality, []metricInput{
		{name: "authenticity", value: authenticity, rule: rules.Authenticity},
		{name: "capability_rate", value: metrics.CapabilityRate, rule: rules.Capability},
		{name: "stability_rate", value: metrics.StabilityRate, rule: rules.Stability},
		{name: "evidence_confidence", value: metrics.EvidenceConfidence, rule: rules.Evidence},
	})
}

func normalizedAuthenticity(authenticity Authenticity, values AuthenticityValues) (float64, error) {
	for _, value := range []float64{values.Consistent, values.Suspicious, values.Inconsistent} {
		if !validRatio(value) {
			return 0, ErrInvalidRules
		}
	}
	switch authenticity {
	case AuthenticityConsistent:
		return values.Consistent, nil
	case AuthenticitySuspicious:
		return values.Suspicious, nil
	case AuthenticityInconsistent:
		return values.Inconsistent, nil
	default:
		return 0, ErrInvalidMetric
	}
}

type metricInput struct {
	name  string
	value float64
	rule  MetricRule
}

func scoreDimension(dimension Dimension, metrics []metricInput) (DimensionScore, error) {
	weights := make([]float64, 0, len(metrics))
	for _, metric := range metrics {
		weights = append(weights, metric.rule.Weight)
	}
	if err := validateWeights(weights...); err != nil {
		return DimensionScore{}, err
	}

	result := DimensionScore{
		Dimension:  dimension,
		Components: make([]ComponentScore, 0, len(metrics)),
	}
	for _, metric := range metrics {
		normalized, err := normalizeMetric(metric.value, metric.rule)
		if err != nil {
			return DimensionScore{}, err
		}
		result.Score += Score(normalized.Float64() * metric.rule.Weight)
		result.Components = append(result.Components, ComponentScore{
			Metric:     metric.name,
			RawValue:   metric.value,
			Normalized: normalized,
			Weight:     metric.rule.Weight,
		})
	}
	result.Score = clampScore(result.Score.Float64())
	return result, nil
}

func normalizeMetric(value float64, rule MetricRule) (Score, error) {
	if !finite(value) {
		return 0, ErrInvalidMetric
	}
	if !finite(rule.Best) || !finite(rule.Worst) || rule.Best == rule.Worst {
		return 0, ErrInvalidRules
	}
	progress := (value - rule.Best) / (rule.Worst - rule.Best)
	return clampScore(100 * (1 - progress)), nil
}

func validRatio(value float64) bool {
	return finite(value) && value >= 0 && value <= 1
}

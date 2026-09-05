package scoring

import (
	"time"

	domainmeasurement "github.com/evepupil/ManyRouter/internal/domain/measurement"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
)

func defaultPriceRules() domainscoring.PriceRules {
	return domainscoring.PriceRules{
		RelativeCost:    domainscoring.MetricRule{Best: 1, Worst: 3, Weight: 0.70},
		ChangesPerDay:   domainscoring.MetricRule{Best: 0, Worst: 4, Weight: 0.15},
		ChangeMagnitude: domainscoring.MetricRule{Best: 0, Worst: 0.5, Weight: 0.15},
	}
}

func defaultLatencyRules() domainscoring.LatencyRules {
	return domainscoring.LatencyRules{
		TTFTP50:     domainscoring.MetricRule{Best: 500, Worst: 10_000, Weight: 0.35},
		TTFTP95:     domainscoring.MetricRule{Best: 1_000, Worst: 20_000, Weight: 0.25},
		DurationP50: domainscoring.MetricRule{Best: 1_000, Worst: 60_000, Weight: 0.15},
		DurationP95: domainscoring.MetricRule{Best: 3_000, Worst: 120_000, Weight: 0.15},
		Variability: domainscoring.MetricRule{Best: 1, Worst: 5, Weight: 0.10},
	}
}

func defaultSLARules() domainscoring.SLARules {
	return domainscoring.SLARules{
		AttemptSuccess:   domainscoring.MetricRule{Best: 1, Worst: 0.8, Weight: 0.35},
		RateLimit:        domainscoring.MetricRule{Best: 0, Worst: 0.2, Weight: 0.20},
		ConsecutiveFails: domainscoring.MetricRule{Best: 0, Worst: 3, Weight: 0.15},
		StreamCompletion: domainscoring.MetricRule{Best: 1, Worst: 0.8, Weight: 0.10},
		Recovery:         domainscoring.MetricRule{Best: 0, Worst: 3_600_000, Weight: 0.10},
		Capacity:         domainscoring.MetricRule{Best: 1, Worst: 0.8, Weight: 0.10},
	}
}

func defaultQualityRules() domainscoring.QualityRules {
	return domainscoring.QualityRules{
		Authenticity:       domainscoring.MetricRule{Best: 1, Worst: 0, Weight: 0.40},
		AuthenticityValues: domainscoring.AuthenticityValues{Consistent: 1, Suspicious: 0.5, Inconsistent: 0},
		Capability:         domainscoring.MetricRule{Best: 1, Worst: 0, Weight: 0.35},
		Stability:          domainscoring.MetricRule{Best: 1, Worst: 0, Weight: 0.10},
		Evidence:           domainscoring.MetricRule{Best: 1, Worst: 0, Weight: 0.15},
	}
}

func scoringPolicyExplanation(policy domainscoring.RecommendationPolicy) map[string]any {
	strategyWeights := make(map[string]domainscoring.AutoWeights, 5)
	for _, kind := range []domainscoring.AutoKind{
		domainscoring.AutoLowestPrice, domainscoring.AutoLowLatency, domainscoring.AutoHighSLA,
		domainscoring.AutoHighQuality, domainscoring.AutoBalanced,
	} {
		weights, _ := domainscoring.FixedAutoWeights(policy.Version, kind)
		strategyWeights[string(kind)] = weights
	}
	return map[string]any{
		"minimum_passive_samples":        domainscoring.DefaultMinimumSamples,
		"window_weights":                 domainscoring.DefaultWindowWeights(),
		"strategy_weights":               strategyWeights,
		"join_threshold":                 policy.JoinThreshold,
		"exit_threshold":                 policy.ExitThreshold,
		"required_consecutive_windows":   policy.RequiredConsecutiveWindows,
		"recommendation_max_gap_seconds": int64(maximumRecommendationGap.Seconds()),
		"collection_fresh_seconds":       int64((15 * time.Minute).Seconds()),
		"collection_stale_seconds":       int64(time.Hour.Seconds()),
		"authenticity_valid_days":        7,
		"capability_valid_days":          7,
		"health_valid_hours":             24,
		"measurement_rule_version":       domainmeasurement.MeasurementRuleVersion,
		"error_classification_version":   domainmeasurement.ErrorClassificationRuleVersion,
		"aggregation_version":            "minute-metrics-v1",
		"latency_buckets_ms":             latencyBucketExplanation(),
		"price_rules":                    defaultPriceRules(),
		"latency_rules":                  defaultLatencyRules(),
		"sla_rules":                      defaultSLARules(),
		"quality_rules":                  defaultQualityRules(),
	}
}

func latencyBucketExplanation() []any {
	buckets := domainscoring.LatencyBuckets()
	result := make([]any, 0, len(buckets))
	for _, bucket := range buckets {
		if bucket.Infinite {
			result = append(result, "+Inf")
		} else {
			result = append(result, bucket.UpperBoundMillis)
		}
	}
	return result
}

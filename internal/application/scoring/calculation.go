package scoring

import (
	"math"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
	"github.com/shopspring/decimal"
)

func scorePrice(target Target, lowestCost decimal.Decimal, evidence PriceEvidence) (domainscoring.DimensionScore, error) {
	return domainscoring.ScorePrice(domainscoring.PriceMetrics{CurrentCost: target.InputPrice.Add(target.OutputPrice).Div(decimal.NewFromInt(2)), LowestCandidateCost: lowestCost, ChangesPerDay: evidence.ChangesPerDay, ChangeMagnitudeRatio: evidence.ChangeMagnitudeRatio}, defaultPriceRules())
}

func scoreQuality(evidence EvaluationEvidence, now time.Time) (domainscoring.DimensionScore, bool) {
	authenticity := scoringAuthenticity(evidence.Authenticity)
	if authenticity == domainscoring.AuthenticityPending || evidence.CapabilityID == nil || evidence.CapabilityCheckedAt.IsZero() || now.Sub(evidence.CapabilityCheckedAt) > 7*24*time.Hour {
		return domainscoring.DimensionScore{}, false
	}
	result, err := domainscoring.ScoreQuality(domainscoring.QualityMetrics{Authenticity: authenticity, CapabilityRate: clampRatio(evidence.CapabilityScore / 100), StabilityRate: clampRatio(evidence.CapabilityConfidence), EvidenceConfidence: clampRatio(evidence.AuthenticityConfidence)}, defaultQualityRules())
	return result, err == nil
}

func buildWindowScore(window domainscoring.Window, metrics WindowMetrics, collection CollectionEvidence, evaluation EvaluationEvidence, price domainscoring.DimensionScore, priceAvailable bool, quality domainscoring.DimensionScore, qualityAvailable bool, consecutiveFailures uint64, now time.Time) (domainscoring.WindowScoreInput, map[string]any) {
	latency, latencyAvailable := scoreLatency(metrics)
	sla, slaAvailable := scoreSLA(metrics, consecutiveFailures)
	scores := domainscoring.DimensionScores{}
	if priceAvailable {
		scores.Price = price.Score
	}
	if latencyAvailable {
		scores.Latency = latency.Score
	}
	if slaAvailable {
		scores.SLA = sla.Score
	}
	if qualityAvailable {
		scores.Quality = quality.Score
	}
	factors := evidenceFactors(metrics, collection, evaluation, now)
	input := domainscoring.WindowScoreInput{Window: window, Complete: priceAvailable && latencyAvailable && slaAvailable && qualityAvailable, Scores: scores, Evidence: domainscoring.EvidenceInput{SampleCount: metrics.SLAAttemptCount, MinimumSamples: domainscoring.DefaultMinimumSamples, Factors: factors, AttributionPending: metrics.PendingAttribution}}
	if collection.DataGap {
		input.Evidence.CollectionGaps = []string{"site_collection_gap"}
	}
	explanation := map[string]any{"window": window, "attempts": metrics.AttemptCount, "sla_attempts": metrics.SLAAttemptCount, "successes": metrics.SuccessCount, "failures": metrics.FailureCount, "rate_limited": metrics.RateLimitedCount, "facts_through": metrics.FactsThrough, "recovery_ms": metrics.RecoveryMillis, "coarse_duration_samples": metrics.CoarseDurationCount, "complete": input.Complete, "evidence_factors": factors, "price": map[string]any{"available": priceAvailable, "score": price}, "latency": map[string]any{"available": latencyAvailable, "score": latency}, "sla": map[string]any{"available": slaAvailable, "score": sla}, "quality": map[string]any{"available": qualityAvailable, "score": quality}}
	return input, explanation
}

func scoreLatency(metrics WindowMetrics) (domainscoring.DimensionScore, bool) {
	ttft, err := domainscoring.SummarizeLatency(metrics.TTFT)
	if err != nil {
		return domainscoring.DimensionScore{}, false
	}
	duration, err := domainscoring.SummarizeLatency(metrics.SuccessDuration)
	if err != nil {
		return domainscoring.DimensionScore{}, false
	}
	p50, p95, durationP50, durationP95 := percentileValue(ttft.P50), percentileValue(ttft.P95), percentileValue(duration.P50), percentileValue(duration.P95)
	result, err := domainscoring.ScoreLatency(domainscoring.LatencyMetrics{TTFTP50Millis: p50, TTFTP95Millis: p95, DurationP50Millis: durationP50, DurationP95Millis: durationP95, VariabilityRatio: p95 / math.Max(p50, 1)}, defaultLatencyRules())
	return result, err == nil
}

func scoreSLA(metrics WindowMetrics, consecutiveFailures uint64) (domainscoring.DimensionScore, bool) {
	if metrics.SLAAttemptCount == 0 {
		return domainscoring.DimensionScore{}, false
	}
	successRate := float64(metrics.SLAAttemptCount-metrics.SLAFailureCount) / float64(metrics.SLAAttemptCount)
	rateLimitRate := float64(metrics.RateLimitedCount) / float64(metrics.SLAAttemptCount)
	streamCompletion := 1.0
	if metrics.StreamCount > 0 {
		streamCompletion = float64(metrics.StreamCompletedCount) / float64(metrics.StreamCount)
	}
	result, err := domainscoring.ScoreSLA(domainscoring.SLAMetrics{AttemptSuccessRate: successRate, RateLimitRate: rateLimitRate, ConsecutiveFailures: consecutiveFailures, StreamCompletionRate: streamCompletion, RecoveryMillis: float64(metrics.RecoveryMillis), CapacityStability: clampRatio(1 - rateLimitRate)}, defaultSLARules())
	return result, err == nil
}

func hardGateInput(evaluation EvaluationEvidence, failures FailureStreak, now time.Time) domainscoring.HardGateInput {
	credential := domainscoring.CheckPending
	if failures.Authentication >= 3 {
		credential = domainscoring.CheckFail
	} else if activeProbeConfidence(evaluation, now) != domainscoring.ConfidenceInsufficient {
		credential = domainscoring.CheckPass
	}
	balance := domainscoring.CheckPending
	if failures.Balance >= 3 {
		balance = domainscoring.CheckFail
	} else if credential == domainscoring.CheckPass {
		balance = domainscoring.CheckPass
	}
	return domainscoring.HardGateInput{Authenticity: scoringAuthenticity(evaluation.Authenticity), CredentialValid: credential, BalanceAvailable: balance, MajorRiskAbsent: domainscoring.CheckPass, AttributedConsecutiveFailures: failures.Total}
}

func scoringAuthenticity(verdict domainevaluation.Verdict) domainscoring.Authenticity {
	switch verdict {
	case domainevaluation.VerdictConsistent:
		return domainscoring.AuthenticityConsistent
	case domainevaluation.VerdictSuspicious:
		return domainscoring.AuthenticitySuspicious
	case domainevaluation.VerdictInconsistent:
		return domainscoring.AuthenticityInconsistent
	default:
		return domainscoring.AuthenticityPending
	}
}

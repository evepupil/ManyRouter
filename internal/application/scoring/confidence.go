package scoring

import (
	"math"
	"time"

	domainevaluation "github.com/evepupil/ManyRouter/internal/domain/evaluation"
	domainscoring "github.com/evepupil/ManyRouter/internal/domain/scoring"
)

func evidenceFactors(metrics WindowMetrics, collection CollectionEvidence, evaluation EvaluationEvidence, now time.Time) []domainscoring.ConfidenceFactor {
	return []domainscoring.ConfidenceFactor{{Name: domainscoring.FactorFreshness, Level: freshnessConfidence(metrics.FactsThrough, now)}, {Name: domainscoring.FactorCollectionCoverage, Level: collectionConfidence(collection, now)}, {Name: domainscoring.FactorTimingCoverage, Level: timingConfidence(metrics)}, {Name: domainscoring.FactorAttribution, Level: booleanConfidence(!metrics.PendingAttribution)}, {Name: domainscoring.FactorSupplierMapping, Level: domainscoring.ConfidenceHigh}, {Name: domainscoring.FactorRealTraffic, Level: sampleConfidence(metrics.SLAAttemptCount)}, {Name: domainscoring.FactorActiveProbe, Level: activeProbeConfidence(evaluation, now)}, {Name: domainscoring.FactorAuthenticity, Level: authenticityConfidence(evaluation, now)}}
}
func freshnessConfidence(at, now time.Time) domainscoring.Confidence {
	if at.IsZero() || now.Sub(at) > time.Hour {
		return domainscoring.ConfidenceInsufficient
	}
	if now.Sub(at) <= 15*time.Minute {
		return domainscoring.ConfidenceHigh
	}
	return domainscoring.ConfidenceMedium
}
func collectionConfidence(e CollectionEvidence, now time.Time) domainscoring.Confidence {
	if e.DataGap || e.LastSuccessAt.IsZero() || now.Sub(e.LastSuccessAt) > time.Hour {
		return domainscoring.ConfidenceInsufficient
	}
	if now.Sub(e.LastSuccessAt) <= 15*time.Minute {
		return domainscoring.ConfidenceHigh
	}
	return domainscoring.ConfidenceMedium
}
func timingConfidence(m WindowMetrics) domainscoring.Confidence {
	if m.SLAAttemptCount == 0 || m.SuccessDurationCount*2 < m.SuccessCount {
		return domainscoring.ConfidenceInsufficient
	}
	if m.CoarseDurationCount > 0 || (m.StreamCount > 0 && m.TTFTCount*2 < m.StreamCount) {
		return domainscoring.ConfidenceLow
	}
	return domainscoring.ConfidenceHigh
}
func sampleConfidence(s uint64) domainscoring.Confidence {
	if s >= 200 {
		return domainscoring.ConfidenceHigh
	}
	if s >= domainscoring.DefaultMinimumSamples {
		return domainscoring.ConfidenceMedium
	}
	return domainscoring.ConfidenceInsufficient
}
func activeProbeConfidence(e EvaluationEvidence, now time.Time) domainscoring.Confidence {
	if e.HealthCheckedAt.IsZero() || now.Sub(e.HealthCheckedAt) > 24*time.Hour || e.HealthConfidence < .5 {
		return domainscoring.ConfidenceInsufficient
	}
	if e.HealthConfidence >= .9 {
		return domainscoring.ConfidenceHigh
	}
	return domainscoring.ConfidenceMedium
}
func authenticityConfidence(e EvaluationEvidence, now time.Time) domainscoring.Confidence {
	if e.AuthenticityID == nil || e.Authenticity == domainevaluation.VerdictInsufficient || e.AuthenticityCheckedAt.IsZero() || now.Sub(e.AuthenticityCheckedAt) > 7*24*time.Hour {
		return domainscoring.ConfidenceInsufficient
	}
	if e.AuthenticityConfidence >= .9 {
		return domainscoring.ConfidenceHigh
	}
	if e.AuthenticityConfidence >= .6 {
		return domainscoring.ConfidenceMedium
	}
	return domainscoring.ConfidenceLow
}
func booleanConfidence(ok bool) domainscoring.Confidence {
	if ok {
		return domainscoring.ConfidenceHigh
	}
	return domainscoring.ConfidenceInsufficient
}
func percentileValue(v domainscoring.PercentileRange) float64 {
	if v.UpperBoundInfinite {
		return 600000
	}
	return float64(v.UpperBoundMillis)
}
func clampRatio(v float64) float64 { return math.Max(0, math.Min(1, v)) }

package evaluation

import "errors"

var ErrInvalidFingerprint = errors.New("model fingerprint is invalid")

func CompareFingerprints(reference, target Fingerprint, policy AuthenticityPolicy) (FingerprintComparison, error) {
	comparison := FingerprintComparison{CellDistances: make(map[CellID]float64)}
	if err := validatePolicy(policy); err != nil {
		return comparison, err
	}
	if reference.ProtocolVersion == "" || target.ProtocolVersion == "" {
		return comparison, ErrInvalidFingerprint
	}
	if reference.ProtocolVersion != target.ProtocolVersion {
		comparison.Reason = ReasonProtocolMismatch
		return comparison, nil
	}
	if reference.ProtocolVersion != ProtocolSingleTokenJSDV1 {
		return comparison, ErrInvalidFingerprint
	}

	var totalDistance float64
	for _, cell := range protocolV1Cells {
		referenceDistribution, referenceOK := reference.Cells[cell]
		targetDistribution, targetOK := target.Cells[cell]
		if !referenceOK || !targetOK || referenceDistribution.ValidSamples() < policy.MinimumSamplesPerCell || targetDistribution.ValidSamples() < policy.MinimumSamplesPerCell {
			comparison.Reason = ReasonInsufficientSamples
			return comparison, nil
		}
		distance, err := JensenShannon(referenceDistribution, targetDistribution)
		if err != nil {
			return FingerprintComparison{}, ErrInvalidFingerprint
		}
		comparison.CellDistances[cell] = distance
		comparison.ComparableCells++
		totalDistance += distance
	}
	comparison.Comparable = true
	comparison.MeanDistance = totalDistance / float64(len(protocolV1Cells))
	return comparison, nil
}

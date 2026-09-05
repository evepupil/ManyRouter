package evaluation

import (
	"errors"
	"math"
)

var ErrInvalidDistribution = errors.New("fingerprint distribution is invalid")

func JensenShannon(left, right Distribution) (float64, error) {
	leftTotal, err := distributionTotal(left)
	if err != nil {
		return 0, err
	}
	rightTotal, err := distributionTotal(right)
	if err != nil {
		return 0, err
	}

	keys := make(map[string]struct{}, len(left.Counts)+len(right.Counts))
	for key := range left.Counts {
		keys[key] = struct{}{}
	}
	for key := range right.Counts {
		keys[key] = struct{}{}
	}

	var divergence float64
	for key := range keys {
		leftProbability := float64(left.Counts[key]) / leftTotal
		rightProbability := float64(right.Counts[key]) / rightTotal
		middle := (leftProbability + rightProbability) / 2
		if leftProbability > 0 {
			divergence += 0.5 * leftProbability * math.Log2(leftProbability/middle)
		}
		if rightProbability > 0 {
			divergence += 0.5 * rightProbability * math.Log2(rightProbability/middle)
		}
	}
	if divergence < 0 && divergence > -1e-12 {
		return 0, nil
	}
	if divergence > 1 && divergence < 1+1e-12 {
		return 1, nil
	}
	if math.IsNaN(divergence) || math.IsInf(divergence, 0) || divergence < 0 || divergence > 1 {
		return 0, ErrInvalidDistribution
	}
	return divergence, nil
}

func distributionTotal(distribution Distribution) (float64, error) {
	var total float64
	for answer, count := range distribution.Counts {
		if answer == "" {
			return 0, ErrInvalidDistribution
		}
		total += float64(count)
	}
	if total <= 0 || math.IsInf(total, 0) {
		return 0, ErrInvalidDistribution
	}
	return total, nil
}

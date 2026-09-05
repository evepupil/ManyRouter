package evaluation

import (
	"errors"
	"math"
	"testing"
)

func TestJensenShannonIdentityAndDisjointDistributions(t *testing.T) {
	t.Parallel()
	identical, err := JensenShannon(
		Distribution{Counts: map[string]uint64{"a": 7, "b": 3}},
		Distribution{Counts: map[string]uint64{"a": 7, "b": 3}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(identical) > 1e-12 {
		t.Fatalf("identical distributions have distance %v", identical)
	}

	disjoint, err := JensenShannon(
		Distribution{Counts: map[string]uint64{"a": 10}},
		Distribution{Counts: map[string]uint64{"b": 10}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(disjoint-1) > 1e-12 {
		t.Fatalf("disjoint distributions have distance %v", disjoint)
	}
}

func TestJensenShannonIsSymmetric(t *testing.T) {
	t.Parallel()
	left := Distribution{Counts: map[string]uint64{"a": 8, "b": 2}}
	right := Distribution{Counts: map[string]uint64{"a": 1, "b": 4, "c": 5}}
	forward, err := JensenShannon(left, right)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := JensenShannon(right, left)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(forward-reverse) > 1e-12 || forward <= 0 || forward >= 1 {
		t.Fatalf("unexpected distances: forward=%v reverse=%v", forward, reverse)
	}
}

func TestJensenShannonRejectsInvalidDistributions(t *testing.T) {
	t.Parallel()
	tests := []Distribution{
		{},
		{Counts: map[string]uint64{}},
		{Counts: map[string]uint64{"": 10}},
		{Counts: map[string]uint64{"answer": 0}},
	}
	for _, distribution := range tests {
		if _, err := JensenShannon(distribution, Distribution{Counts: map[string]uint64{"valid": 10}}); !errors.Is(err, ErrInvalidDistribution) {
			t.Fatalf("got %v, want invalid distribution", err)
		}
	}
}

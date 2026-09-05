package evaluation

import (
	"errors"
	"testing"
	"time"
)

func testFingerprint(answer string, count uint64, collectedAt time.Time) Fingerprint {
	cells := make(map[CellID]Distribution, len(protocolV1Cells))
	for _, cell := range protocolV1Cells {
		cells[cell] = Distribution{Counts: map[string]uint64{answer: count}}
	}
	return Fingerprint{
		RunID:           "run-" + answer,
		Seed:            count,
		ProtocolVersion: ProtocolSingleTokenJSDV1,
		CollectedAt:     collectedAt,
		Cells:           cells,
		Stability:       Stability{Measured: true, Distance: 0.1},
	}
}

func TestCompareFingerprintsRequiresAllEightCellsAndTenSamples(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	reference := testFingerprint("a", 10, now)
	target := testFingerprint("a", 10, now)
	comparison, err := CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !comparison.Comparable || comparison.ComparableCells != 8 || comparison.MeanDistance != 0 {
		t.Fatalf("unexpected comparison: %#v", comparison)
	}

	delete(target.Cells, CellCoinZH)
	comparison, err = CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Comparable || comparison.Reason != ReasonInsufficientSamples {
		t.Fatalf("missing cell was accepted: %#v", comparison)
	}

	target = testFingerprint("a", 9, now)
	comparison, err = CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Comparable || comparison.Reason != ReasonInsufficientSamples {
		t.Fatalf("undersized cells were accepted: %#v", comparison)
	}
}

func TestCompareFingerprintsDetectsProtocolMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	reference := testFingerprint("a", 10, now)
	target := testFingerprint("a", 10, now)
	target.ProtocolVersion = "other_protocol"
	comparison, err := CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Comparable || comparison.Reason != ReasonProtocolMismatch {
		t.Fatalf("protocol mismatch was accepted: %#v", comparison)
	}
}

func TestCompareFingerprintsRejectsUnknownProtocolEvenWhenItMatches(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	reference := testFingerprint("a", 10, now)
	target := testFingerprint("a", 10, now)
	reference.ProtocolVersion = "unknown_protocol"
	target.ProtocolVersion = "unknown_protocol"
	_, err := CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if !errors.Is(err, ErrInvalidFingerprint) {
		t.Fatalf("got %v, want invalid fingerprint", err)
	}
}

func TestCompareFingerprintsRejectsMalformedData(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	reference := testFingerprint("a", 10, now)
	target := testFingerprint("b", 10, now)
	target.Cells[CellCoinZH] = Distribution{Counts: map[string]uint64{"": 10}}
	_, err := CompareFingerprints(reference, target, DefaultAuthenticityPolicy())
	if !errors.Is(err, ErrInvalidFingerprint) {
		t.Fatalf("got %v, want invalid fingerprint", err)
	}
}

func TestRequiredCellsReturnsACopy(t *testing.T) {
	t.Parallel()
	first := RequiredCells()
	if len(first) != 8 {
		t.Fatalf("got %d required cells", len(first))
	}
	first[0] = CellID("changed")
	second := RequiredCells()
	if second[0] == first[0] {
		t.Fatal("required cells exposed mutable package state")
	}
}

package scoring_test

import (
	"errors"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/scoring"
)

func TestLatencyBucketsMatchTheSharedMillisecondContract(t *testing.T) {
	t.Parallel()
	buckets := scoring.LatencyBuckets()
	want := []int64{50, 100, 250, 500, 1_000, 2_000, 3_000, 5_000, 8_000, 10_000, 15_000, 30_000, 60_000, 120_000, 300_000, 600_000}
	if len(buckets) != scoring.LatencyBucketCount {
		t.Fatalf("got %d buckets, want %d", len(buckets), scoring.LatencyBucketCount)
	}
	for index, upperBound := range want {
		if buckets[index].UpperBoundMillis != upperBound || buckets[index].Infinite {
			t.Fatalf("bucket %d = %#v, want finite upper bound %d", index, buckets[index], upperBound)
		}
	}
	if last := buckets[len(buckets)-1]; !last.Infinite || last.UpperBoundMillis != 0 {
		t.Fatalf("last bucket must represent +Inf: %#v", last)
	}
}

func TestLatencySummaryRecomputesPercentilesAfterMergingBuckets(t *testing.T) {
	t.Parallel()
	fast := histogram(t, map[int]uint64{0: 8})
	slow := histogram(t, map[int]uint64{2: 2})

	summary, err := scoring.SummarizeLatency(fast, slow)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Samples != 10 {
		t.Fatalf("samples = %d, want 10", summary.Samples)
	}
	assertPercentile(t, summary.P50, 50, 5, 0, true, 50, false)
	assertPercentile(t, summary.P90, 90, 9, 100, false, 250, false)
	assertPercentile(t, summary.P95, 95, 10, 100, false, 250, false)

	fastP50, err := fast.Percentile(50)
	if err != nil {
		t.Fatal(err)
	}
	slowP50, err := slow.Percentile(50)
	if err != nil {
		t.Fatal(err)
	}
	averagedSmallWindowP50 := (fastP50.UpperBoundMillis + slowP50.UpperBoundMillis) / 2
	if summary.P50.UpperBoundMillis == averagedSmallWindowP50 {
		t.Fatal("merged P50 was derived by averaging small-window percentiles")
	}
}

func TestLatencyPercentileReportsAnOpenEndedInterval(t *testing.T) {
	t.Parallel()
	histogram := histogram(t, map[int]uint64{scoring.LatencyBucketCount - 1: 3})
	percentile, err := histogram.Percentile(95)
	if err != nil {
		t.Fatal(err)
	}
	if !percentile.UpperBoundInfinite || percentile.LowerBoundMillis != 600_000 {
		t.Fatalf("unexpected +Inf interval: %#v", percentile)
	}
}

func TestLatencyHistogramRejectsInvalidShapeEmptyDataAndOverflow(t *testing.T) {
	t.Parallel()
	if _, err := scoring.NewLatencyHistogram(make([]uint64, scoring.LatencyBucketCount-1)); !errors.Is(err, scoring.ErrInvalidHistogram) {
		t.Fatalf("invalid bucket count returned %v", err)
	}
	empty := histogram(t, nil)
	if _, err := empty.Percentile(50); !errors.Is(err, scoring.ErrNoSamples) {
		t.Fatalf("empty percentile returned %v", err)
	}

	var overflow scoring.LatencyHistogram
	overflow.Counts[0] = ^uint64(0)
	overflow.Counts[1] = 1
	if _, err := overflow.SampleCount(); !errors.Is(err, scoring.ErrInvalidHistogram) {
		t.Fatalf("overflowing sample count returned %v", err)
	}
	left := histogram(t, map[int]uint64{0: ^uint64(0)})
	right := histogram(t, map[int]uint64{1: 1})
	if _, err := scoring.MergeLatencyHistograms(left, right); !errors.Is(err, scoring.ErrInvalidHistogram) {
		t.Fatalf("merged cross-bucket overflow returned %v", err)
	}
}

func histogram(t *testing.T, countsByBucket map[int]uint64) scoring.LatencyHistogram {
	t.Helper()
	counts := make([]uint64, scoring.LatencyBucketCount)
	for index, count := range countsByBucket {
		counts[index] = count
	}
	histogram, err := scoring.NewLatencyHistogram(counts)
	if err != nil {
		t.Fatal(err)
	}
	return histogram
}

func assertPercentile(
	t *testing.T,
	got scoring.PercentileRange,
	percentile uint8,
	rank uint64,
	lower int64,
	lowerInclusive bool,
	upper int64,
	upperInfinite bool,
) {
	t.Helper()
	if got.Percentile != percentile || got.Rank != rank || got.LowerBoundMillis != lower ||
		got.LowerBoundInclusive != lowerInclusive || got.UpperBoundMillis != upper ||
		got.UpperBoundInfinite != upperInfinite {
		t.Fatalf("unexpected percentile range: %#v", got)
	}
}

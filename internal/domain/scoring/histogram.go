package scoring

const LatencyBucketCount = 17

// LatencyBucket describes a cumulative upper-bound convention. The finite
// ranges are [0, first] and (previous, current]; the last range is +Inf.
type LatencyBucket struct {
	UpperBoundMillis int64
	Infinite         bool
}

var latencyBuckets = [...]LatencyBucket{
	{UpperBoundMillis: 50},
	{UpperBoundMillis: 100},
	{UpperBoundMillis: 250},
	{UpperBoundMillis: 500},
	{UpperBoundMillis: 1_000},
	{UpperBoundMillis: 2_000},
	{UpperBoundMillis: 3_000},
	{UpperBoundMillis: 5_000},
	{UpperBoundMillis: 8_000},
	{UpperBoundMillis: 10_000},
	{UpperBoundMillis: 15_000},
	{UpperBoundMillis: 30_000},
	{UpperBoundMillis: 60_000},
	{UpperBoundMillis: 120_000},
	{UpperBoundMillis: 300_000},
	{UpperBoundMillis: 600_000},
	{Infinite: true},
}

func LatencyBuckets() []LatencyBucket {
	buckets := make([]LatencyBucket, len(latencyBuckets))
	copy(buckets, latencyBuckets[:])
	return buckets
}

// LatencyHistogram stores non-cumulative counts in the shared bucket order.
type LatencyHistogram struct {
	Counts [LatencyBucketCount]uint64
}

func NewLatencyHistogram(counts []uint64) (LatencyHistogram, error) {
	if len(counts) != LatencyBucketCount {
		return LatencyHistogram{}, ErrInvalidHistogram
	}
	var histogram LatencyHistogram
	copy(histogram.Counts[:], counts)
	return histogram, nil
}

func (h LatencyHistogram) SampleCount() (uint64, error) {
	var total uint64
	for _, count := range h.Counts {
		if ^uint64(0)-total < count {
			return 0, ErrInvalidHistogram
		}
		total += count
	}
	return total, nil
}

func MergeLatencyHistograms(histograms ...LatencyHistogram) (LatencyHistogram, error) {
	if len(histograms) == 0 {
		return LatencyHistogram{}, ErrInvalidHistogram
	}
	var merged LatencyHistogram
	for _, histogram := range histograms {
		for index, count := range histogram.Counts {
			if ^uint64(0)-merged.Counts[index] < count {
				return LatencyHistogram{}, ErrInvalidHistogram
			}
			merged.Counts[index] += count
		}
	}
	if _, err := merged.SampleCount(); err != nil {
		return LatencyHistogram{}, err
	}
	return merged, nil
}

type PercentileRange struct {
	Percentile          uint8
	Rank                uint64
	SampleCount         uint64
	LowerBoundMillis    int64
	LowerBoundInclusive bool
	UpperBoundMillis    int64
	UpperBoundInfinite  bool
}

// Percentile uses the nearest-rank method and returns the containing bucket so
// callers retain the histogram's interval uncertainty.
func (h LatencyHistogram) Percentile(percentile uint8) (PercentileRange, error) {
	if percentile == 0 || percentile > 100 {
		return PercentileRange{}, ErrInvalidMetric
	}
	samples, err := h.SampleCount()
	if err != nil {
		return PercentileRange{}, err
	}
	if samples == 0 {
		return PercentileRange{}, ErrNoSamples
	}

	rank := percentileRank(samples, uint64(percentile))
	var cumulative uint64
	for index, count := range h.Counts {
		cumulative += count
		if cumulative < rank {
			continue
		}
		result := PercentileRange{
			Percentile:          percentile,
			Rank:                rank,
			SampleCount:         samples,
			LowerBoundInclusive: index == 0,
		}
		if index > 0 {
			result.LowerBoundMillis = latencyBuckets[index-1].UpperBoundMillis
		}
		bucket := latencyBuckets[index]
		result.UpperBoundMillis = bucket.UpperBoundMillis
		result.UpperBoundInfinite = bucket.Infinite
		return result, nil
	}
	return PercentileRange{}, ErrInvalidHistogram
}

func percentileRank(samples, percentile uint64) uint64 {
	whole := (samples / 100) * percentile
	remainder := (samples % 100) * percentile
	return whole + (remainder+99)/100
}

type LatencySummary struct {
	Histogram LatencyHistogram
	Samples   uint64
	P50       PercentileRange
	P90       PercentileRange
	P95       PercentileRange
}

// SummarizeLatency merges bucket counts before deriving P50, P90, and P95.
func SummarizeLatency(histograms ...LatencyHistogram) (LatencySummary, error) {
	merged, err := MergeLatencyHistograms(histograms...)
	if err != nil {
		return LatencySummary{}, err
	}
	p50, err := merged.Percentile(50)
	if err != nil {
		return LatencySummary{}, err
	}
	p90, err := merged.Percentile(90)
	if err != nil {
		return LatencySummary{}, err
	}
	p95, err := merged.Percentile(95)
	if err != nil {
		return LatencySummary{}, err
	}
	return LatencySummary{
		Histogram: merged,
		Samples:   p50.SampleCount,
		P50:       p50,
		P90:       p90,
		P95:       p95,
	}, nil
}

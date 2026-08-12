package perflane

import (
	"fmt"
	"math"
	"slices"
)

// Distribution retains raw sample count and explains any percentile that is
// statistically inapplicable under the contract's minimums.
type Distribution struct {
	Samples int               `json:"samples"`
	P50     Evidence[float64] `json:"p50"`
	P95     Evidence[float64] `json:"p95"`
	P99     Evidence[float64] `json:"p99"`
	Max     Evidence[float64] `json:"max"`
}

func Summarize(samples []float64, minimums PercentileMinimums) (Distribution, error) {
	if minimums.P95 < 1 || minimums.P99 < minimums.P95 {
		return Distribution{}, fmt.Errorf("invalid percentile minimums: p95=%d p99=%d", minimums.P95, minimums.P99)
	}
	ordered := slices.Clone(samples)
	for index, value := range ordered {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Distribution{}, fmt.Errorf("sample %d is not finite", index)
		}
	}
	slices.Sort(ordered)
	result := Distribution{Samples: len(ordered)}
	result.P50 = percentileEvidence(ordered, 0.50, 1, "p50")
	result.P95 = percentileEvidence(ordered, 0.95, minimums.P95, "p95")
	result.P99 = percentileEvidence(ordered, 0.99, minimums.P99, "p99")
	if len(ordered) == 0 {
		result.Max = Unavailable[float64]("max requires at least 1 sample; got 0")
	} else {
		result.Max = Available(ordered[len(ordered)-1])
	}
	return result, nil
}

func percentileEvidence(samples []float64, percentile float64, minimum int, name string) Evidence[float64] {
	if len(samples) < minimum {
		return Unavailable[float64](fmt.Sprintf("%s requires at least %d samples; got %d", name, minimum, len(samples)))
	}
	// Nearest-rank percentiles are deterministic and always select an observed
	// sample. The rank is one-based.
	rank := int(math.Ceil(percentile * float64(len(samples))))
	return Available(samples[rank-1])
}

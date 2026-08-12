package perflane

import (
	"math"
	"strings"
	"testing"
)

func TestSummarizePercentiles(t *testing.T) {
	samples := make([]float64, 100)
	for index := range samples {
		samples[index] = float64(100 - index)
	}
	summary, err := Summarize(samples, PercentileMinimums{P95: 20, P99: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertEvidenceValue(t, "p50", summary.P50, 50)
	assertEvidenceValue(t, "p95", summary.P95, 95)
	assertEvidenceValue(t, "p99", summary.P99, 99)
	assertEvidenceValue(t, "max", summary.Max, 100)
	if samples[0] != 100 {
		t.Fatal("Summarize mutated input")
	}
}

func TestSummarizeExplainsInapplicablePercentiles(t *testing.T) {
	summary, err := Summarize([]float64{3, 1, 2}, PercentileMinimums{P95: 20, P99: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertEvidenceValue(t, "p50", summary.P50, 2)
	assertEvidenceValue(t, "max", summary.Max, 3)
	if summary.P95.Value != nil || !strings.Contains(summary.P95.UnavailableReason, "requires at least 20") {
		t.Fatalf("p95 = %#v", summary.P95)
	}
	if summary.P99.Value != nil || !strings.Contains(summary.P99.UnavailableReason, "requires at least 100") {
		t.Fatalf("p99 = %#v", summary.P99)
	}
}

func TestSummarizeEmptyAndNonFinite(t *testing.T) {
	summary, err := Summarize(nil, PercentileMinimums{P95: 20, P99: 100})
	if err != nil {
		t.Fatal(err)
	}
	for name, evidence := range map[string]Evidence[float64]{"p50": summary.P50, "p95": summary.P95, "p99": summary.P99, "max": summary.Max} {
		if evidence.Value != nil || evidence.UnavailableReason == "" {
			t.Fatalf("%s = %#v", name, evidence)
		}
	}
	if _, err := Summarize([]float64{1, math.NaN()}, PercentileMinimums{P95: 20, P99: 100}); err == nil {
		t.Fatal("Summarize accepted NaN")
	}
}

func assertEvidenceValue(t *testing.T, name string, evidence Evidence[float64], want float64) {
	t.Helper()
	if evidence.Value == nil || *evidence.Value != want || evidence.UnavailableReason != "" {
		t.Fatalf("%s = %#v, want %v", name, evidence, want)
	}
}

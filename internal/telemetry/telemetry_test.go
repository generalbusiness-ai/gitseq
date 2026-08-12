package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDisabledRuntimeReturnsHandlerUnchanged(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	(&Runtime{}).Handler(handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("disabled handler did not run")
	}
}

func TestInMemoryExporterGetsTemplateOnlyHTTPMetrics(t *testing.T) {
	runtime, err := NewInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Shutdown(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v0/actors/{fingerprint}", func(http.ResponseWriter, *http.Request) {})
	handler := runtime.Handler(observe.HTTPHandler(runtime.Observer(), mux))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v0/actors/private-fingerprint", nil))
	metrics, err := runtime.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !hasMetric(metrics, "gitseq.operation.duration") || !hasMetric(metrics, "go.goroutine.count") {
		t.Fatalf("expected operation and Go runtime metrics, got %+v", metrics)
	}
	if len(runtime.Spans()) != 1 || runtime.Spans()[0].Name != "gitseq.http" {
		t.Fatalf("HTTP spans = %+v", runtime.Spans())
	}
	// Look where the attributes actually go. observer.Record attaches them with
	// AddEvent, so they land in span.Events[].Attributes rather than on the span,
	// and they are recorded on every metric datapoint. An earlier version scanned
	// only span.Attributes, which no operation attribute ever reaches: routing the
	// raw URL path into the observer left it passing while internal/observe caught
	// the very same mutation.
	var scanned int
	assertBounded := func(where string, attributes []attribute.KeyValue) {
		for _, value := range attributes {
			scanned++
			for _, secret := range []string{"private-fingerprint", "/v0/actors/private-fingerprint"} {
				if strings.Contains(value.Value.Emit(), secret) {
					t.Fatalf("raw request path reached %s as %s=%q", where, value.Key, value.Value.Emit())
				}
			}
		}
	}
	for _, span := range runtime.Spans() {
		assertBounded("a span attribute", span.Attributes)
		for _, recorded := range span.Events {
			assertBounded("a span event attribute", recorded.Attributes)
		}
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					assertBounded(metric.Name+" datapoint", point.Attributes.ToSlice())
				}
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					assertBounded(metric.Name+" datapoint", point.Attributes.ToSlice())
				}
			}
		}
	}

	// A privacy walk that inspected nothing would pass exactly as quietly as one
	// that inspected everything, so prove it reached the attributes carrying the
	// route instead of trusting that it did.
	if scanned == 0 {
		t.Fatal("the attribute walk found nothing to inspect, so it proves nothing")
	}
	if !routeRecorded(runtime, metrics) {
		t.Fatal("no bounded http.route attribute was recorded, so the leak this test guards could not have been seen")
	}
}

// routeRecorded reports whether the bounded route template reached both places
// the exporter carries it, so the guard above cannot pass vacuously.
func routeRecorded(runtime *Runtime, metrics metricdata.ResourceMetrics) bool {
	const want = "GET /v0/actors/{fingerprint}"
	var inSpan, inMetric bool
	for _, span := range runtime.Spans() {
		for _, recorded := range span.Events {
			for _, value := range recorded.Attributes {
				if value.Key == "http.route" && value.Value.Emit() == want {
					inSpan = true
				}
			}
		}
	}
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			data, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok {
				continue
			}
			for _, point := range data.DataPoints {
				for _, value := range point.Attributes.ToSlice() {
					if value.Key == "http.route" && value.Value.Emit() == want {
						inMetric = true
					}
				}
			}
		}
	}
	return inSpan && inMetric
}

func hasMetric(metrics metricdata.ResourceMetrics, name string) bool {
	for _, scope := range metrics.ScopeMetrics {
		for _, candidate := range scope.Metrics {
			if candidate.Name == name {
				return true
			}
		}
	}
	return false
}

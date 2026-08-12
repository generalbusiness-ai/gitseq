package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
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
	for _, span := range runtime.Spans() {
		for _, value := range span.Attributes {
			if value.Value.AsString() == "private-fingerprint" || value.Value.AsString() == "/v0/actors/private-fingerprint" {
				t.Fatalf("private raw path entered span attributes: %+v", span.Attributes)
			}
		}
	}
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

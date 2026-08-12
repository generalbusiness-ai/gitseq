// Package telemetry adapts Gitseq's neutral observations to OpenTelemetry at
// process composition boundaries.
package telemetry

import (
	"context"
	"errors"
	"net/http"

	"github.com/generalbusiness-ai/gitseq/internal/observe"
	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/generalbusiness-ai/gitseq"

// Runtime owns the optional SDK/exporter lifetime. A zero Runtime is disabled.
type Runtime struct {
	observer observe.Observer
	meter    metric.MeterProvider
	tracer   trace.TracerProvider
	shutdown func(context.Context) error
	manual   *sdkmetric.ManualReader
	spans    *tracetest.InMemoryExporter
}

// NewOTLP enables traces, metrics, and Go runtime metrics over OTLP/HTTP. The
// endpoint is explicit; Gitseq never silently discovers or selects a backend.
func NewOTLP(ctx context.Context, endpoint string) (*Runtime, error) {
	if endpoint == "" {
		return &Runtime{}, nil
	}
	resource := sdkresource.NewSchemaless(attribute.String("service.name", "gitseq-resident"))
	traceExporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	metricExporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
	if err != nil {
		return nil, err
	}
	tracer := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExporter), sdktrace.WithResource(resource))
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)), sdkmetric.WithResource(resource))
	runtime, err := newRuntime(meter, tracer)
	if err != nil {
		_ = tracer.Shutdown(ctx)
		_ = meter.Shutdown(ctx)
		return nil, err
	}
	runtime.shutdown = func(shutdownCtx context.Context) error {
		return errors.Join(tracer.Shutdown(shutdownCtx), meter.Shutdown(shutdownCtx))
	}
	return runtime, nil
}

// NewInMemory is the local/test exporter. Collection is explicit and cannot
// send data off-host.
func NewInMemory() (*Runtime, error) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spans := tracetest.NewInMemoryExporter()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	runtime, err := newRuntime(meter, tracer)
	if err != nil {
		return nil, err
	}
	runtime.manual, runtime.spans = reader, spans
	runtime.shutdown = func(ctx context.Context) error {
		return errors.Join(tracer.Shutdown(ctx), meter.Shutdown(ctx))
	}
	return runtime, nil
}

func newRuntime(meter *sdkmetric.MeterProvider, tracer *sdktrace.TracerProvider) (*Runtime, error) {
	observer, err := newObserver(meter.Meter(instrumentationName))
	if err != nil {
		return nil, err
	}
	if err := otelruntime.Start(otelruntime.WithMeterProvider(meter)); err != nil {
		return nil, err
	}
	return &Runtime{observer: observer, meter: meter, tracer: tracer}, nil
}

func (r *Runtime) Enabled() bool { return r != nil && r.observer != nil }

func (r *Runtime) Observer() observe.Observer {
	if r == nil {
		return nil
	}
	return r.observer
}

// Handler installs standard net/http spans plus Gitseq's template-only metric.
func (r *Runtime) Handler(next http.Handler) http.Handler {
	if !r.Enabled() {
		return next
	}
	tracer := r.tracer.Tracer(instrumentationName)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, span := tracer.Start(request.Context(), "gitseq.http")
		defer span.End()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.shutdown == nil {
		return nil
	}
	return r.shutdown(ctx)
}

// Collect exposes only the in-memory metric exporter used by tests and local
// overhead evidence.
func (r *Runtime) Collect(ctx context.Context) (metricdata.ResourceMetrics, error) {
	var result metricdata.ResourceMetrics
	if r == nil || r.manual == nil {
		return result, errors.New("in-memory telemetry is not enabled")
	}
	err := r.manual.Collect(ctx, &result)
	return result, err
}

func (r *Runtime) Spans() tracetest.SpanStubs {
	if r == nil || r.spans == nil {
		return nil
	}
	return r.spans.GetSpans()
}

type observer struct {
	duration metric.Float64Histogram
	items    metric.Int64Counter
}

func newObserver(meter metric.Meter) (*observer, error) {
	duration, err := meter.Float64Histogram("gitseq.operation.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	items, err := meter.Int64Counter("gitseq.operation.items")
	if err != nil {
		return nil, err
	}
	return &observer{duration: duration, items: items}, nil
}

func (o *observer) Record(ctx context.Context, value observe.Measurement) {
	attributes := []attribute.KeyValue{
		attribute.String("gitseq.operation", string(value.Operation)),
		attribute.String("gitseq.path", string(value.Path)),
		attribute.String("gitseq.outcome", string(value.Outcome)),
	}
	if value.Route != "" {
		attributes = append(attributes,
			attribute.String("http.route", value.Route), attribute.String("http.request.method", value.Method),
			attribute.Int("http.response.status_code_class", value.Status))
	}
	options := metric.WithAttributes(attributes...)
	o.duration.Record(ctx, value.Duration.Seconds(), options)
	o.items.Add(ctx, value.Items, options)
	trace.SpanFromContext(ctx).AddEvent("gitseq.operation", trace.WithAttributes(attributes...))
}

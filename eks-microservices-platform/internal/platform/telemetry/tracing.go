package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ShutdownFunc flushes buffered spans. Call it during graceful shutdown or the
// spans describing the final requests are lost exactly when they matter most.
type ShutdownFunc func(context.Context) error

// TracingConfig configures the OTLP trace pipeline.
type TracingConfig struct {
	ServiceName string
	Version     string
	Environment string
	// Endpoint is the OTLP/HTTP collector address. Empty disables tracing, so
	// local runs and unit tests need no collector.
	Endpoint string
	// SampleRate in [0,1] applies to traces this service starts. Sampling is
	// parent-based, so a sampled trace stays sampled across every hop and never
	// produces a trace with holes in it.
	SampleRate float64
}

// SetupTracing installs a global tracer provider and W3C context propagation.
//
// When Endpoint is empty it installs propagation only and returns a no-op
// shutdown, so the incoming traceparent still flows through to downstream calls
// and to logs even with no collector running.
func SetupTracing(ctx context.Context, cfg TracingConfig) (ShutdownFunc, error) {
	// Propagation is installed unconditionally: it is what stitches the three
	// services into one trace, and it costs nothing without an exporter.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(cfg.Endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.Version),
			attribute.String("deployment.environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	rate := cfg.SampleRate
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxQueueSize(4096),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))),
	)
	otel.SetTracerProvider(provider)

	return provider.Shutdown, nil
}

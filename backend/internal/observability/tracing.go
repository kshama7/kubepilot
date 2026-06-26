// Package observability wires KubePilot's OpenTelemetry tracing. Every analysis
// run is an HTTP request, and every request is a traceable span (enriched with
// cluster/analyzer attributes by the API middleware). Tracing is optional: with
// no OTLP endpoint configured, the global tracer stays a no-op and the process
// runs unchanged.
package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config holds the tracing settings.
type Config struct {
	// Endpoint is the OTLP/gRPC collector address (host:port). Empty disables
	// tracing entirely.
	Endpoint    string
	Insecure    bool
	ServiceName string
	Version     string
}

// InitTracer installs a global tracer provider exporting to the configured OTLP
// endpoint and returns a shutdown function. With an empty endpoint it installs
// nothing and returns a no-op shutdown — the global tracer remains the default
// no-op, so instrumented code is free.
func InitTracer(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }
	if cfg.Endpoint == "" {
		return noop, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return noop, fmt.Errorf("create otlp exporter: %w", err)
	}

	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		return noop, fmt.Errorf("build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

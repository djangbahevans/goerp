// Package telemetry sets up OpenTelemetry tracing, metrics, and propagation
// for the GoERP engine (engine-internals.md §12/§14, erp-design.md §8).
package telemetry

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config holds the configuration needed to stand up OpenTelemetry tracing.
type Config struct {
	Endpoint    string
	ServiceName string
	Environment string
	Insecure    bool
}

// SetupTracing initializes an OpenTelemetry TracerProvider with an OTLP gRPC
// exporter, configures W3C Trace Context and Baggage propagation globally,
// registers the TracerProvider globally, and returns the provider and tracer.
// If Endpoint is empty, it registers a no-op provider and returns a no-op tracer.
func SetupTracing(ctx context.Context, cfg Config) (*sdktrace.TracerProvider, trace.Tracer, error) {
	// Always set up global W3C Trace Context and Baggage propagation so inbound
	// and outbound requests can extract and inject trace context regardless of
	// whether exporting is enabled or in fallback mode.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.ServiceName == "" {
		cfg.ServiceName = "goerp-engine"
	}

	if cfg.Endpoint == "" {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return nil, tp.Tracer(cfg.ServiceName), nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("deployment.environment", cfg.Environment),
		),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithHost(),
	)
	if err != nil {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return nil, tp.Tracer(cfg.ServiceName), fmt.Errorf("create otel resource: %w", err)
	}

	// Merged with resource.Default() (b wins on key conflict) so the
	// standard telemetry.sdk.name/language/version attributes every OTel
	// backend expects are present too — resource.New above only builds
	// this call's own explicit attributes plus process/OS/host, not the
	// SDK identification Default() provides. Our own service.name still
	// wins over Default()'s placeholder value since res is passed second.
	if merged, mergeErr := resource.Merge(resource.Default(), res); mergeErr == nil {
		res = merged
	} else {
		log.Warn().Err(mergeErr).Msg("could not merge otel default resource attributes, continuing without them")
	}

	var grpcOpts []otlptracegrpc.Option
	grpcOpts = append(grpcOpts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
	if cfg.Insecure {
		grpcOpts = append(grpcOpts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, grpcOpts...)
	if err != nil {
		tp := noop.NewTracerProvider()
		otel.SetTracerProvider(tp)
		return nil, tp.Tracer(cfg.ServiceName), fmt.Errorf("create otlp trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	tracer := tp.Tracer(cfg.ServiceName)

	return tp, tracer, nil
}

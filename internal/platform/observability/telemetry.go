package observability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config defines the configuration for the telemetry provider.
type Config struct {
	ServiceName string
	Environment string
	Exporter    string // "noop", "console", "otlp"
	Endpoint    string // OTLP endpoint, e.g. "localhost:4317"
}

// Telemetry manages OpenTelemetry lifecycle.
type Telemetry struct {
	cfg Config
	tp  *sdktrace.TracerProvider
	mp  *sdkmetric.MeterProvider
}

// NewTelemetry creates a new Telemetry component.
func NewTelemetry(cfg Config) *Telemetry {
	return &Telemetry{cfg: cfg}
}

// Name returns the component name.
func (t *Telemetry) Name() string {
	return "telemetry"
}

// Start initializes the OpenTelemetry providers and registers them globally.
func (t *Telemetry) Start(ctx context.Context) error {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			semconv.ServiceNameKey.String(t.cfg.ServiceName),
			semconv.DeploymentEnvironmentKey.String(t.cfg.Environment),
		),
	)
	if err != nil {
		return fmt.Errorf("create resource: %w", err)
	}

	var traceExporter sdktrace.SpanExporter
	var metricExporter sdkmetric.Exporter

	switch t.cfg.Exporter {
	case "console":
		var err error
		traceExporter, err = stdouttrace.New(
			stdouttrace.WithPrettyPrint(),
			stdouttrace.WithWriter(os.Stderr),
		)
		if err != nil {
			return fmt.Errorf("create stdout trace exporter: %w", err)
		}
		metricExporter, err = stdoutmetric.New(
			stdoutmetric.WithWriter(os.Stderr),
		)
		if err != nil {
			return fmt.Errorf("create stdout metric exporter: %w", err)
		}
	case "otlp":
		var err error
		traceExporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(t.cfg.Endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return fmt.Errorf("create otlp trace exporter: %w", err)
		}
		metricExporter, err = otlpmetricgrpc.New(ctx,
			otlpmetricgrpc.WithEndpoint(t.cfg.Endpoint),
			otlpmetricgrpc.WithInsecure(),
		)
		if err != nil {
			return fmt.Errorf("create otlp metric exporter: %w", err)
		}
	case "noop", "":
		// No-op mode
	default:
		return fmt.Errorf("unknown telemetry exporter: %s", t.cfg.Exporter)
	}

	if traceExporter != nil {
		t.tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(t.tp)
	} else {
		otel.SetTracerProvider(sdktrace.NewTracerProvider())
	}

	if metricExporter != nil {
		t.mp = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(5*time.Second))),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(t.mp)
	} else {
		otel.SetMeterProvider(sdkmetric.NewMeterProvider())
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return nil
}

// Stop shuts down the OpenTelemetry providers.
func (t *Telemetry) Stop(ctx context.Context) error {
	var errs []error
	if t.tp != nil {
		if err := t.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown tracer provider: %w", err))
		}
	}
	if t.mp != nil {
		if err := t.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown meter provider: %w", err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

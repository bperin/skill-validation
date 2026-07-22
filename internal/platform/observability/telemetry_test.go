package observability

import (
	"context"
	"testing"
)

func TestNewTelemetry(t *testing.T) {
	cfg := Config{ServiceName: "test", Environment: "test", Exporter: "noop"}
	telemetry := NewTelemetry(cfg)
	if telemetry.Name() != "telemetry" {
		t.Errorf("expected telemetry, got %s", telemetry.Name())
	}
	if err := telemetry.Start(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if err := telemetry.Stop(context.Background()); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

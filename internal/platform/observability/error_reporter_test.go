package observability

import (
	"context"
	"errors"
	"testing"
)

func TestNoopErrorReporter(t *testing.T) {
	reporter := NewNoopErrorReporter()
	reporter.Report(context.Background(), errors.New("test"), nil)
}

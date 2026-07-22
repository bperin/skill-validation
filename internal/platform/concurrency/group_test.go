package concurrency

import (
	"context"
	"testing"
)

func TestGroup(t *testing.T) {
	g, _ := NewGroup(context.Background())
	g.Go(func() error { return nil })
	if err := g.Wait(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

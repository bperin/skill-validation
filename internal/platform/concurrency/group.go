package concurrency

import (
	"context"
	"golang.org/x/sync/errgroup"
)

// Group wraps errgroup.Group for easier usage.
type Group struct {
	g *errgroup.Group
}

// NewGroup creates a new Group.
func NewGroup(ctx context.Context) (*Group, context.Context) {
	g, ctx := errgroup.WithContext(ctx)
	return &Group{g: g}, ctx
}

// Go runs a function in a new goroutine.
func (g *Group) Go(f func() error) {
	g.g.Go(f)
}

// Wait waits for all goroutines to finish.
func (g *Group) Wait() error {
	return g.g.Wait()
}

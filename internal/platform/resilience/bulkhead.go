package resilience

import (
	"context"
	"errors"
	"time"
)

var ErrBulkheadFull = errors.New("bulkhead full")

// Bulkhead caps concurrent calls to a dependency so a slow provider cannot
// exhaust the service's goroutines and connections.
type Bulkhead struct {
	sem  chan struct{}
	wait time.Duration
}

func NewBulkhead(maxConcurrent int, maxWait time.Duration) *Bulkhead {
	return &Bulkhead{sem: make(chan struct{}, maxConcurrent), wait: maxWait}
}

func (b *Bulkhead) Do(ctx context.Context, fn func() error) error {
	t := time.NewTimer(b.wait)
	defer t.Stop()
	select {
	case b.sem <- struct{}{}:
	case <-t.C:
		return ErrBulkheadFull
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-b.sem }()
	return fn()
}

func (b *Bulkhead) InFlight() int { return len(b.sem) }

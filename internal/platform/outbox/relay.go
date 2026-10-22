// Package outbox implements the relay side of the transactional outbox:
// it reads committed, unpublished records and publishes them to the broker.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

// Record is a serialized envelope waiting in the outbox table.
type Record struct {
	ID      string
	Subject string
	Data    []byte
}

// Store is implemented by the Postgres and in-memory outboxes. Drain must call
// publish for up to limit records in order and only mark as published the ones
// that succeeded (stopping at the first failure).
type Store interface {
	Drain(ctx context.Context, limit int, publish func(context.Context, Record) error) (int, error)
}

// Publisher is the broker side.
type Publisher interface {
	Publish(ctx context.Context, subject, msgID string, data []byte) error
}

type Relay struct {
	Store    Store
	Pub      Publisher
	Service  string
	Interval time.Duration
	Batch    int
	Log      *slog.Logger
}

// Run polls until ctx is cancelled. Delivery is at-least-once; consumers
// deduplicate through their inbox and the broker through Nats-Msg-Id.
func (r *Relay) Run(ctx context.Context) {
	if r.Interval == 0 {
		r.Interval = 200 * time.Millisecond
	}
	if r.Batch == 0 {
		r.Batch = 100
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		for {
			n, err := r.Store.Drain(ctx, r.Batch, func(ctx context.Context, rec Record) error {
				pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				return r.Pub.Publish(pctx, rec.Subject, rec.ID, rec.Data)
			})
			if n > 0 {
				observability.OutboxPublished.WithLabelValues(r.Service).Add(float64(n))
			}
			if err != nil {
				if ctx.Err() == nil {
					observability.OutboxErrors.WithLabelValues(r.Service).Inc()
					r.Log.Warn("outbox relay publish failed", "err", err)
				}
				break
			}
			if n < r.Batch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

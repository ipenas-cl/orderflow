// Package provider contains the outbound adapters to payment providers and
// the resilience decorator that wraps any of them.
package provider

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
	"github.com/ipenas-cl/orderflow/internal/platform/resilience"
)

// Resilient decorates a PaymentProvider with, from the outside in:
// retry (exp. backoff + full jitter) -> bulkhead -> circuit breaker -> per-attempt timeout.
// Declines are business outcomes: not retried, not counted as failures.
type Resilient struct {
	Inner          ports.PaymentProvider
	Breaker        *resilience.Breaker
	Bulkhead       *resilience.Bulkhead
	Retry          resilience.RetryPolicy
	AttemptTimeout time.Duration
}

type Options struct {
	MaxAttempts      int
	AttemptTimeout   time.Duration
	MaxConcurrent    int
	FailureThreshold int
	OpenTimeout      time.Duration
}

func NewResilient(inner ports.PaymentProvider, o Options, log *slog.Logger) *Resilient {
	name := inner.Name()
	notDeclined := func(err error) bool { return err != nil && !errors.Is(err, ports.ErrDeclined) }
	return &Resilient{
		Inner: inner,
		Breaker: resilience.NewBreaker(resilience.BreakerConfig{
			FailureThreshold: o.FailureThreshold, OpenTimeout: o.OpenTimeout, IsFailure: notDeclined,
			OnStateChange: func(from, to resilience.State) {
				log.Warn("circuit breaker state change", "provider", name, "from", from.String(), "to", to.String())
				observability.BreakerState.WithLabelValues(name).Set(float64(to))
			},
		}),
		Bulkhead: resilience.NewBulkhead(o.MaxConcurrent, 100*time.Millisecond),
		Retry: resilience.RetryPolicy{
			MaxAttempts: o.MaxAttempts, BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second,
			Retryable: func(err error) bool {
				return !errors.Is(err, ports.ErrDeclined) && !errors.Is(err, resilience.ErrOpen) && !errors.Is(err, resilience.ErrBulkheadFull)
			},
			OnRetry: func(attempt int, d time.Duration, err error) {
				log.Info("retrying provider call", "provider", name, "attempt", attempt, "delay", d, "err", err)
			},
		},
		AttemptTimeout: o.AttemptTimeout,
	}
}

func (r *Resilient) Name() string { return r.Inner.Name() }

func (r *Resilient) call(ctx context.Context, op string, fn func(context.Context) error) error {
	err := resilience.Retry(ctx, r.Retry, func(ctx context.Context) error {
		return r.Bulkhead.Do(ctx, func() error {
			return r.Breaker.Execute(func() error {
				actx, cancel := context.WithTimeout(ctx, r.AttemptTimeout)
				defer cancel()
				return fn(actx)
			})
		})
	})
	result := "ok"
	switch {
	case err == nil:
	case errors.Is(err, ports.ErrDeclined):
		result = "declined"
	case errors.Is(err, resilience.ErrOpen):
		result = "circuit_open"
	case errors.Is(err, resilience.ErrBulkheadFull):
		result = "bulkhead_full"
		observability.BulkheadRejected.WithLabelValues(r.Name()).Inc()
	default:
		result = "error"
	}
	observability.ProviderCalls.WithLabelValues(r.Name(), op, result).Inc()
	return err
}

func (r *Resilient) Authorize(ctx context.Context, req ports.AuthorizeRequest) (ref string, err error) {
	err = r.call(ctx, "authorize", func(ctx context.Context) error { ref, err = r.Inner.Authorize(ctx, req); return err })
	return
}

func (r *Resilient) Refund(ctx context.Context, req ports.RefundRequest) (ref string, err error) {
	err = r.call(ctx, "refund", func(ctx context.Context) error { ref, err = r.Inner.Refund(ctx, req); return err })
	return
}

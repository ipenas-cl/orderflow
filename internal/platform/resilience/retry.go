package resilience

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	// Retryable decides whether an error is transient. Defaults to "not
	// Permanent and not ErrOpen and not ErrBulkheadFull".
	Retryable func(error) bool
	OnRetry   func(attempt int, delay time.Duration, err error)
}

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks err as non-retryable.
func Permanent(err error) error { return permanent{err} }

func IsPermanent(err error) bool { var p permanent; return errors.As(err, &p) }

// Backoff returns a full-jitter delay: random in [0, min(max, base*2^attempt)).
func Backoff(p RetryPolicy, attempt int) time.Duration {
	ceil := p.BaseDelay << attempt
	if ceil <= 0 || ceil > p.MaxDelay {
		ceil = p.MaxDelay
	}
	return time.Duration(rand.Int64N(int64(ceil) + 1))
}

// Retry calls fn until it succeeds, returns a non-retryable error, the
// attempts are exhausted or ctx is done.
func Retry(ctx context.Context, p RetryPolicy, fn func(context.Context) error) error {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay <= 0 {
		p.BaseDelay = 50 * time.Millisecond
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 2 * time.Second
	}
	retryable := p.Retryable
	if retryable == nil {
		retryable = func(err error) bool {
			return !IsPermanent(err) && !errors.Is(err, ErrOpen) && !errors.Is(err, ErrBulkheadFull)
		}
	}
	var err error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		if err = fn(ctx); err == nil || !retryable(err) {
			return err
		}
		if attempt == p.MaxAttempts-1 {
			break
		}
		d := Backoff(p, attempt)
		if p.OnRetry != nil {
			p.OnRetry(attempt+1, d, err)
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(d):
		}
	}
	return err
}

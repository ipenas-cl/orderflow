// Package resilience contains small, dependency-free resilience primitives:
// circuit breaker, retry with exponential backoff and full jitter, bulkhead
// and a per-client rate limiter.
package resilience

import (
	"errors"
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	HalfOpen
	Open
)

func (s State) String() string { return [...]string{"closed", "half-open", "open"}[s] }

var ErrOpen = errors.New("circuit breaker is open")

type BreakerConfig struct {
	FailureThreshold int           // consecutive failures that open the circuit
	OpenTimeout      time.Duration // time before a half-open probe is allowed
	HalfOpenMax      int           // concurrent probes in half-open
	IsFailure        func(error) bool
	OnStateChange    func(from, to State)
}

type Breaker struct {
	mu       sync.Mutex
	cfg      BreakerConfig
	state    State
	failures int
	openedAt time.Time
	probes   int
	Now      func() time.Time
}

func NewBreaker(cfg BreakerConfig) *Breaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 10 * time.Second
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 1
	}
	if cfg.IsFailure == nil {
		cfg.IsFailure = func(err error) bool { return err != nil }
	}
	return &Breaker{cfg: cfg, Now: time.Now}
}

func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) setState(s State) {
	if b.state == s {
		return
	}
	from := b.state
	b.state = s
	if b.cfg.OnStateChange != nil {
		b.cfg.OnStateChange(from, s)
	}
}

// Execute runs fn unless the circuit is open.
func (b *Breaker) Execute(fn func() error) error {
	b.mu.Lock()
	switch b.state {
	case Open:
		if b.Now().Sub(b.openedAt) < b.cfg.OpenTimeout {
			b.mu.Unlock()
			return ErrOpen
		}
		b.setState(HalfOpen)
		b.probes = 0
		fallthrough
	case HalfOpen:
		if b.probes >= b.cfg.HalfOpenMax {
			b.mu.Unlock()
			return ErrOpen
		}
		b.probes++
	}
	b.mu.Unlock()

	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == HalfOpen {
		b.probes--
	}
	if b.cfg.IsFailure(err) {
		b.failures++
		if b.state == HalfOpen || b.failures >= b.cfg.FailureThreshold {
			b.setState(Open)
			b.openedAt = b.Now()
		}
		return err
	}
	b.failures = 0
	b.setState(Closed)
	return err
}

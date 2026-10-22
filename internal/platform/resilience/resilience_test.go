package resilience

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func TestBreakerOpensAndRecovers(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewBreaker(BreakerConfig{FailureThreshold: 3, OpenTimeout: time.Second})
	b.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		_ = b.Execute(func() error { return errBoom })
	}
	if b.State() != Open {
		t.Fatalf("want open, got %v", b.State())
	}
	if err := b.Execute(func() error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("want ErrOpen, got %v", err)
	}
	now = now.Add(2 * time.Second)
	if err := b.Execute(func() error { return errBoom }); !errors.Is(err, errBoom) {
		t.Fatal("half-open probe should run")
	}
	if b.State() != Open {
		t.Fatal("failed probe must re-open")
	}
	now = now.Add(2 * time.Second)
	if err := b.Execute(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if b.State() != Closed {
		t.Fatal("successful probe must close")
	}
}

func TestBreakerIgnoresBusinessErrors(t *testing.T) {
	declined := errors.New("declined")
	b := NewBreaker(BreakerConfig{FailureThreshold: 1, IsFailure: func(err error) bool { return err != nil && !errors.Is(err, declined) }})
	_ = b.Execute(func() error { return declined })
	if b.State() != Closed {
		t.Fatal("business errors must not trip the breaker")
	}
}

func TestRetryEventuallySucceeds(t *testing.T) {
	calls := 0
	err := Retry(context.Background(), RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond},
		func(context.Context) error {
			calls++
			if calls < 3 {
				return errBoom
			}
			return nil
		})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRetryStopsOnPermanentAndOpen(t *testing.T) {
	for _, e := range []error{Permanent(errBoom), ErrOpen, ErrBulkheadFull} {
		calls := 0
		_ = Retry(context.Background(), RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}, func(context.Context) error { calls++; return e })
		if calls != 1 {
			t.Fatalf("%v: calls=%d", e, calls)
		}
	}
}

func TestBackoffIsBoundedAndJittered(t *testing.T) {
	p := RetryPolicy{BaseDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond}
	seen := map[time.Duration]bool{}
	for i := 0; i < 200; i++ {
		d := Backoff(p, 10)
		if d < 0 || d > p.MaxDelay {
			t.Fatalf("out of bounds: %v", d)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Fatal("expected jitter")
	}
}

func TestBulkheadRejectsWhenFull(t *testing.T) {
	b := NewBulkhead(2, 10*time.Millisecond)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = b.Do(context.Background(), func() error { <-release; return nil }) }()
	}
	for b.InFlight() < 2 {
		time.Sleep(time.Millisecond)
	}
	if err := b.Do(context.Background(), func() error { return nil }); !errors.Is(err, ErrBulkheadFull) {
		t.Fatalf("want full, got %v", err)
	}
	close(release)
	wg.Wait()
}

func TestRateLimiterMiddleware(t *testing.T) {
	rl := NewRateLimiter(1, 2)
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	codes := []int{}
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:5"
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
		t.Fatalf("codes=%v", codes)
	}
}

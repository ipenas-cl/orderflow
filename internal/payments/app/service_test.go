package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/provider"
	"github.com/ipenas-cl/orderflow/internal/payments/app"
	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/resilience"
)

// mockProvider is a hand-written mock of the PaymentProvider port.
type mockProvider struct {
	calls   atomic.Int32
	refunds atomic.Int32
	fn      func(n int32) (string, error)
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Authorize(context.Context, ports.AuthorizeRequest) (string, error) {
	return m.fn(m.calls.Add(1))
}
func (m *mockProvider) Refund(context.Context, ports.RefundRequest) (string, error) {
	m.refunds.Add(1)
	return "re_1", nil
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func ok(int32) (string, error) { return "ch_1", nil }

func TestIdempotencyKeyPreventsDoubleCharge(t *testing.T) {
	mp := &mockProvider{fn: ok}
	svc := app.NewService(memory.New(), mp, quiet)
	cmd := app.Authorize{IdempotencyKey: "k1", OrderID: "o1", AmountCents: 1000, Currency: "USD"}
	p1, r1, err := svc.Authorize(context.Background(), cmd)
	if err != nil || r1 {
		t.Fatal(err)
	}
	p2, r2, _ := svc.Authorize(context.Background(), cmd)
	if !r2 || p2.ID != p1.ID || mp.calls.Load() != 1 {
		t.Fatalf("replay=%v calls=%d", r2, mp.calls.Load())
	}
	cmd.AmountCents = 2000
	if _, _, err := svc.Authorize(context.Background(), cmd); !errors.Is(err, app.ErrIdempotencyMismatch) {
		t.Fatal("key reuse with different body accepted")
	}
}

func TestDeclineEmitsPaymentFailed(t *testing.T) {
	st := memory.New()
	svc := app.NewService(st, &mockProvider{fn: func(int32) (string, error) { return "", ports.ErrDeclined }}, quiet)
	env, _ := messaging.New(context.Background(), contracts.SubjPaymentAuthorize, "o1", contracts.AuthorizePayment{OrderID: "o1", AmountCents: 100, Currency: "USD"})
	if err := svc.HandleMessage(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if got := st.Messages.Subjects(); !slices.Equal(got, []string{contracts.SubjPaymentFailed}) {
		t.Fatal(got)
	}
}

func TestResilientProviderRetriesTransientErrors(t *testing.T) {
	mp := &mockProvider{fn: func(n int32) (string, error) {
		if n < 3 {
			return "", errors.New("503")
		}
		return "ch_ok", nil
	}}
	rp := provider.NewResilient(mp, provider.Options{MaxAttempts: 4, AttemptTimeout: time.Second, MaxConcurrent: 4, FailureThreshold: 10, OpenTimeout: time.Second}, quiet)
	rp.Retry.BaseDelay = time.Millisecond
	svc := app.NewService(memory.New(), rp, quiet)
	p, _, err := svc.Authorize(context.Background(), app.Authorize{IdempotencyKey: "k", OrderID: "o", AmountCents: 1, Currency: "USD"})
	if err != nil || p.Status != domain.StatusAuthorized || mp.calls.Load() != 3 {
		t.Fatal(err, mp.calls.Load())
	}
}

func TestBreakerOpensAndSagaGetsDefinitiveFailure(t *testing.T) {
	mp := &mockProvider{fn: func(int32) (string, error) { return "", errors.New("down") }}
	rp := provider.NewResilient(mp, provider.Options{MaxAttempts: 2, AttemptTimeout: time.Second, MaxConcurrent: 4, FailureThreshold: 2, OpenTimeout: time.Minute}, quiet)
	rp.Retry.BaseDelay = time.Millisecond
	st := memory.New()
	svc := app.NewService(st, rp, quiet)
	for i, id := range []string{"o1", "o2"} {
		env, _ := messaging.New(context.Background(), contracts.SubjPaymentAuthorize, id, contracts.AuthorizePayment{OrderID: id, AmountCents: 100, Currency: "USD"})
		if err := svc.HandleMessage(context.Background(), env); err != nil {
			t.Fatal(i, err)
		}
	}
	if rp.Breaker.State() != resilience.Open {
		t.Fatal("breaker should be open")
	}
	if mp.calls.Load() != 2 {
		t.Fatalf("open breaker should fail fast, calls=%d", mp.calls.Load())
	}
	if got := st.Messages.Subjects(); !slices.Equal(got, []string{contracts.SubjPaymentFailed, contracts.SubjPaymentFailed}) {
		t.Fatal(got)
	}
	// Public API does not persist transient failures, so clients may retry.
	_, _, err := svc.Authorize(context.Background(), app.Authorize{IdempotencyKey: "api", OrderID: "o3", AmountCents: 1, Currency: "USD"})
	if !errors.Is(err, ports.ErrProviderUnavailable) {
		t.Fatal(err)
	}
}

func TestRefundAndWebhookAreIdempotent(t *testing.T) {
	mp := &mockProvider{fn: ok}
	st := memory.New()
	svc := app.NewService(st, mp, quiet)
	p, _, _ := svc.Authorize(context.Background(), app.Authorize{IdempotencyKey: "k", OrderID: "o", AmountCents: 1, Currency: "USD"})
	if _, err := svc.Refund(context.Background(), app.Refund{PaymentID: p.ID, Reason: "customer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Refund(context.Background(), app.Refund{PaymentID: p.ID}); err != nil || mp.refunds.Load() != 1 {
		t.Fatal(err, mp.refunds.Load())
	}
	ev := ports.ProviderEvent{ID: "evt_1", Type: "refunded", ProviderRef: "ch_1"}
	_ = svc.HandleProviderEvent(context.Background(), ev)
	_ = svc.HandleProviderEvent(context.Background(), ev)
	n := 0
	for _, s := range st.Messages.Subjects() {
		if s == contracts.SubjPaymentRefunded {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("refunded emitted %d times", n)
	}
}

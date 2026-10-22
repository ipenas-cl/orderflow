// Package ports declares the driven ports of the payments service.
package ports

import (
	"context"
	"errors"

	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate idempotency key")
	// ErrDeclined is a business outcome from the provider: never retried and
	// never counted by the circuit breaker.
	ErrDeclined = errors.New("payment declined")
	// ErrProviderUnavailable means retries were exhausted or the breaker is open.
	ErrProviderUnavailable = errors.New("payment provider unavailable")
)

type PaymentRepository interface {
	Create(ctx context.Context, p *domain.Payment) error // ErrDuplicate on key clash
	Update(ctx context.Context, p *domain.Payment) error
	GetByID(ctx context.Context, id string) (*domain.Payment, error)
	GetByKey(ctx context.Context, key string) (*domain.Payment, error)
	GetByProviderRef(ctx context.Context, ref string) (*domain.Payment, error)
	ListByOrder(ctx context.Context, orderID string) ([]*domain.Payment, error)
}

type AuthorizeRequest struct {
	IdempotencyKey string
	OrderID        string
	AmountCents    int64
	Currency       string
}

type RefundRequest struct {
	IdempotencyKey string
	ProviderRef    string
	AmountCents    int64
	Reason         string
}

// PaymentProvider is the port to a PSP. Adapters: the fake provider (local and
// chaos runs) and Stripe (PaymentIntents, test mode).
type PaymentProvider interface {
	Name() string
	Authorize(ctx context.Context, r AuthorizeRequest) (ref string, err error)
	Refund(ctx context.Context, r RefundRequest) (refundRef string, err error)
}

// ProviderEvent is a normalized webhook event.
type ProviderEvent struct {
	ID          string
	Type        string // payment_failed | refunded
	ProviderRef string
	Reason      string
}

type Tx struct {
	Payments PaymentRepository
	Outbox   messaging.Outbox
	Inbox    messaging.Inbox
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

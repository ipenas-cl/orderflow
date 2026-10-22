// Package ports declares the driven ports of the inventory service.
package ports

import (
	"context"
	"errors"

	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

var ErrNotFound = errors.New("not found")

type ItemRepository interface {
	// GetForUpdate locks the row (pessimistic) for the rest of the transaction.
	GetForUpdate(ctx context.Context, sku string) (*domain.Item, error)
	Save(ctx context.Context, i *domain.Item) error
	List(ctx context.Context) ([]*domain.Item, error)
}

type ReservationRepository interface {
	Get(ctx context.Context, orderID string) (*domain.Reservation, error)
	Save(ctx context.Context, r *domain.Reservation) error
}

type AdjustmentLog interface {
	Append(ctx context.Context, a *domain.Adjustment) error
	List(ctx context.Context, limit int) ([]domain.Adjustment, error)
}

type Tx struct {
	Items        ItemRepository
	Reservations ReservationRepository
	Adjustments  AdjustmentLog
	Outbox       messaging.Outbox
	Inbox        messaging.Inbox
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

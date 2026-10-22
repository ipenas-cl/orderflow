// Package ports declares what the orders application needs from the outside
// world (driven ports). Adapters in ../adapters implement them.
package ports

import (
	"context"
	"errors"

	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("concurrent modification")
)

type ListFilter struct {
	Status domain.Status
	Query  string // matches order id prefix or customer id
	Limit  int
}

type OrderRepository interface {
	Create(ctx context.Context, o *domain.Order) error
	Get(ctx context.Context, id string) (*domain.Order, error)
	// Update uses optimistic concurrency on Version.
	Update(ctx context.Context, o *domain.Order) error
	List(ctx context.Context, f ListFilter) ([]*domain.Order, error)
}

// Tx is the set of repositories bound to one database transaction.
type Tx struct {
	Orders OrderRepository
	Outbox messaging.Outbox
	Inbox  messaging.Inbox
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
	// Read runs fn without a transaction for queries.
	Orders() OrderRepository
}

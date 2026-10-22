// Package ports declares the driven ports of the fulfillment service.
package ports

import (
	"context"
	"errors"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrUndeliverable is a permanent carrier rejection; it fails the saga.
	ErrUndeliverable = errors.New("carrier rejected shipment")
)

type ShipmentRepository interface {
	Create(ctx context.Context, s *domain.Shipment) error
	Get(ctx context.Context, id string) (*domain.Shipment, error)
	GetByOrder(ctx context.Context, orderID string) (*domain.Shipment, error)
	Update(ctx context.Context, s *domain.Shipment) error
	List(ctx context.Context, statuses []domain.Status, limit int) ([]*domain.Shipment, error)
}

type Label struct {
	Carrier        string
	TrackingNumber string
}

// Carrier is the port to a shipping carrier (label booking and tracking).
type Carrier interface {
	Book(ctx context.Context, s *domain.Shipment) (Label, error)
	Track(ctx context.Context, tracking string) (domain.Status, error)
}

type Tx struct {
	Shipments ShipmentRepository
	Outbox    messaging.Outbox
	Inbox     messaging.Inbox
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

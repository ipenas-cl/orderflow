// Package ports declares the driven ports of the backoffice BFF: the
// downstream services it orchestrates and the audit log it owns.
package ports

import (
	"context"
	"encoding/json"
	"time"
)

type Order struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	PaymentID string          `json:"payment_id"`
	Raw       json.RawMessage `json:"-"`
}

// Backend is implemented by an HTTP adapter that fans out to the services.
type Backend interface {
	ListOrders(ctx context.Context, status, q string) (json.RawMessage, error)
	GetOrder(ctx context.Context, id string) (Order, error)
	PaymentsForOrder(ctx context.Context, orderID string) (json.RawMessage, error)
	ShipmentForOrder(ctx context.Context, orderID string) (json.RawMessage, error)
	Refund(ctx context.Context, paymentID, idempotencyKey, reason string) (json.RawMessage, error)
	Stock(ctx context.Context) (json.RawMessage, error)
	Adjustments(ctx context.Context) (json.RawMessage, error)
	AdjustStock(ctx context.Context, sku string, delta int, reason, actor string) (json.RawMessage, error)
	Shipments(ctx context.Context, statuses string) (json.RawMessage, error)
	AdvanceShipment(ctx context.Context, id, actor string) (json.RawMessage, error)
}

type AuditEntry struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Role    string    `json:"role"`
	Action  string    `json:"action"`
	Target  string    `json:"target"`
	Detail  string    `json:"detail"`
	Outcome string    `json:"outcome"`
}

type AuditLog interface {
	Append(ctx context.Context, e AuditEntry) error
	List(ctx context.Context, limit int) ([]AuditEntry, error)
}

// Package domain holds the Order aggregate. It has no dependencies on
// infrastructure: no SQL, no HTTP, no broker.
package domain

import (
	"errors"
	"fmt"
	"time"
)

type Status string

const (
	StatusPending       Status = "PENDING"
	StatusStockReserved Status = "STOCK_RESERVED"
	StatusPaid          Status = "PAID"
	StatusShipped       Status = "SHIPPED"
	StatusDelivered     Status = "DELIVERED"
	StatusCancelled     Status = "CANCELLED"
	StatusRefunded      Status = "REFUNDED"
)

var (
	ErrInvalidOrder      = errors.New("invalid order")
	ErrInvalidTransition = errors.New("invalid status transition")
)

type Line struct {
	SKU            string `json:"sku"`
	Qty            int    `json:"qty"`
	UnitPriceCents int64  `json:"unit_price_cents"`
}

// TimelineEntry records one saga step; the backoffice renders it.
type TimelineEntry struct {
	At     time.Time `json:"at"`
	Step   string    `json:"step"`
	Detail string    `json:"detail,omitempty"`
}

// Order is the aggregate root. All state changes go through its methods so
// invariants (valid lines, legal transitions) hold everywhere.
type Order struct {
	ID             string          `json:"id"`
	CustomerID     string          `json:"customer_id"`
	Currency       string          `json:"currency"`
	Lines          []Line          `json:"lines"`
	Status         Status          `json:"status"`
	Reason         string          `json:"reason,omitempty"`
	PaymentID      string          `json:"payment_id,omitempty"`
	TrackingNumber string          `json:"tracking_number,omitempty"`
	Timeline       []TimelineEntry `json:"timeline"`
	Version        int             `json:"version"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func NewOrder(id, customerID, currency string, lines []Line, now time.Time) (*Order, error) {
	if customerID == "" {
		return nil, fmt.Errorf("%w: customer_id required", ErrInvalidOrder)
	}
	if len(currency) != 3 {
		return nil, fmt.Errorf("%w: currency must be ISO-4217", ErrInvalidOrder)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: at least one line", ErrInvalidOrder)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		if l.SKU == "" || l.Qty <= 0 || l.UnitPriceCents < 0 {
			return nil, fmt.Errorf("%w: bad line %+v", ErrInvalidOrder, l)
		}
		if seen[l.SKU] {
			return nil, fmt.Errorf("%w: duplicate sku %s", ErrInvalidOrder, l.SKU)
		}
		seen[l.SKU] = true
	}
	o := &Order{ID: id, CustomerID: customerID, Currency: currency, Lines: lines, Status: StatusPending, CreatedAt: now, UpdatedAt: now}
	if o.Total() <= 0 {
		return nil, fmt.Errorf("%w: total must be positive", ErrInvalidOrder)
	}
	o.record(now, "order placed", "")
	return o, nil
}

func (o *Order) Total() int64 {
	var t int64
	for _, l := range o.Lines {
		t += int64(l.Qty) * l.UnitPriceCents
	}
	return t
}

func (o *Order) record(now time.Time, step, detail string) {
	o.Timeline = append(o.Timeline, TimelineEntry{At: now, Step: step, Detail: detail})
	o.UpdatedAt = now
}

func (o *Order) transition(now time.Time, to Status, step, detail string, from ...Status) error {
	for _, f := range from {
		if o.Status == f {
			o.Status = to
			o.record(now, step, detail)
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, o.Status, to)
}

func (o *Order) MarkStockReserved(now time.Time) error {
	return o.transition(now, StatusStockReserved, "stock reserved", "", StatusPending)
}

func (o *Order) MarkPaid(paymentID string, now time.Time) error {
	if err := o.transition(now, StatusPaid, "payment authorized", paymentID, StatusStockReserved); err != nil {
		return err
	}
	o.PaymentID = paymentID
	return nil
}

func (o *Order) MarkShipped(carrier, tracking string, now time.Time) error {
	if err := o.transition(now, StatusShipped, "shipped", carrier+" "+tracking, StatusPaid); err != nil {
		return err
	}
	o.TrackingNumber = tracking
	return nil
}

func (o *Order) MarkDelivered(now time.Time) error {
	return o.transition(now, StatusDelivered, "delivered", "", StatusShipped)
}

// Note appends a timeline entry without changing state (e.g. PICKING).
func (o *Order) Note(step, detail string, now time.Time) { o.record(now, step, detail) }

// Compensation tells the saga which completed steps must be undone.
type Compensation struct {
	ReleaseStock  bool
	RefundPayment bool
}

// Cancel moves the order to CANCELLED and returns the compensations that
// the saga must issue for the steps already completed.
func (o *Order) Cancel(reason string, now time.Time) (Compensation, error) {
	var c Compensation
	switch o.Status {
	case StatusPending:
	case StatusStockReserved:
		c.ReleaseStock = true
	case StatusPaid:
		c.ReleaseStock, c.RefundPayment = true, true
	default:
		return c, fmt.Errorf("%w: cannot cancel %s", ErrInvalidTransition, o.Status)
	}
	o.Status, o.Reason = StatusCancelled, reason
	o.record(now, "cancelled", reason)
	return c, nil
}

// CanRefund is the policy used by the backoffice.
func (o *Order) CanRefund() bool {
	return o.PaymentID != "" && (o.Status == StatusPaid || o.Status == StatusShipped || o.Status == StatusDelivered)
}

func (o *Order) MarkRefunded(reason string, now time.Time) error {
	if o.Status == StatusCancelled { // refund was a saga compensation
		o.record(now, "payment refunded", reason)
		return nil
	}
	return o.transition(now, StatusRefunded, "payment refunded", reason, StatusPaid, StatusShipped, StatusDelivered)
}

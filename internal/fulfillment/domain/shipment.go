// Package domain holds the Shipment aggregate (pick -> pack -> ship -> deliver).
package domain

import (
	"errors"
	"fmt"
	"time"
)

type Status string

const (
	Pending   Status = "PENDING"
	Picking   Status = "PICKING"
	Packed    Status = "PACKED"
	Shipped   Status = "SHIPPED"
	InTransit Status = "IN_TRANSIT"
	Delivered Status = "DELIVERED"
	Failed    Status = "FAILED"
)

var ErrInvalidTransition = errors.New("invalid shipment transition")

type Line struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

type Event struct {
	At     time.Time `json:"at"`
	Status Status    `json:"status"`
	Actor  string    `json:"actor"`
}

type Shipment struct {
	ID             string    `json:"id"`
	OrderID        string    `json:"order_id"`
	CustomerID     string    `json:"customer_id"`
	Lines          []Line    `json:"lines"`
	Status         Status    `json:"status"`
	Carrier        string    `json:"carrier,omitempty"`
	TrackingNumber string    `json:"tracking_number,omitempty"`
	FailureReason  string    `json:"failure_reason,omitempty"`
	History        []Event   `json:"history"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func New(id, orderID, customerID string, lines []Line, now time.Time) *Shipment {
	s := &Shipment{ID: id, OrderID: orderID, CustomerID: customerID, Lines: lines, Status: Pending, CreatedAt: now}
	s.add(Pending, "system", now)
	return s
}

func (s *Shipment) add(st Status, actor string, now time.Time) {
	s.Status, s.UpdatedAt = st, now
	s.History = append(s.History, Event{At: now, Status: st, Actor: actor})
}

var next = map[Status]Status{Pending: Picking, Picking: Packed, Shipped: InTransit, InTransit: Delivered}

// Advance moves to the next warehouse/tracking step. PACKED -> SHIPPED goes
// through Ship because it needs a carrier label.
func (s *Shipment) Advance(actor string, now time.Time) error {
	n, ok := next[s.Status]
	if !ok {
		return fmt.Errorf("%w: from %s", ErrInvalidTransition, s.Status)
	}
	s.add(n, actor, now)
	return nil
}

func (s *Shipment) Ship(carrier, tracking, actor string, now time.Time) error {
	if s.Status != Packed {
		return fmt.Errorf("%w: ship from %s", ErrInvalidTransition, s.Status)
	}
	s.Carrier, s.TrackingNumber = carrier, tracking
	s.add(Shipped, actor, now)
	return nil
}

func (s *Shipment) Fail(reason, actor string, now time.Time) error {
	if s.Status == Shipped || s.Status == InTransit || s.Status == Delivered || s.Status == Failed {
		return fmt.Errorf("%w: fail from %s", ErrInvalidTransition, s.Status)
	}
	s.FailureReason = reason
	s.add(Failed, actor, now)
	return nil
}

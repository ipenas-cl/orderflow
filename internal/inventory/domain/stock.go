// Package domain holds the inventory aggregates: Item (stock per SKU) and
// Reservation (stock held for an order).
package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrInvalidQuantity   = errors.New("invalid quantity")
	ErrAlreadyReleased   = errors.New("reservation already released")
)

type Item struct {
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	OnHand   int    `json:"on_hand"`
	Reserved int    `json:"reserved"`
	Version  int    `json:"version"`
}

func (i *Item) Available() int { return i.OnHand - i.Reserved }

func (i *Item) Reserve(qty int) error {
	if qty <= 0 {
		return ErrInvalidQuantity
	}
	if i.Available() < qty {
		return fmt.Errorf("%w: %s has %d, need %d", ErrInsufficientStock, i.SKU, i.Available(), qty)
	}
	i.Reserved += qty
	return nil
}

func (i *Item) Release(qty int) error {
	if qty <= 0 || qty > i.Reserved {
		return ErrInvalidQuantity
	}
	i.Reserved -= qty
	return nil
}

// Commit converts a reservation into a shipment: the units leave the
// warehouse, so both on-hand and reserved go down.
func (i *Item) Commit(qty int) error {
	if qty <= 0 || qty > i.Reserved {
		return ErrInvalidQuantity
	}
	i.Reserved -= qty
	i.OnHand -= qty
	return nil
}

// Adjust changes on-hand stock (receiving, cycle count, damage). It can never
// drop below what is already reserved.
func (i *Item) Adjust(delta int) error {
	if i.OnHand+delta < i.Reserved {
		return fmt.Errorf("%w: on hand would drop below reserved", ErrInvalidQuantity)
	}
	i.OnHand += delta
	return nil
}

type ReservationStatus string

const (
	Reserved  ReservationStatus = "RESERVED"
	Released  ReservationStatus = "RELEASED"
	Committed ReservationStatus = "COMMITTED"
)

type Line struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

type Reservation struct {
	OrderID string            `json:"order_id"`
	Lines   []Line            `json:"lines"`
	Status  ReservationStatus `json:"status"`
}

func (r *Reservation) Release() error {
	if r.Status != Reserved {
		return ErrAlreadyReleased
	}
	r.Status = Released
	return nil
}

// Adjustment is an audit record of a manual stock change.
type Adjustment struct {
	ID     int64     `json:"id"`
	SKU    string    `json:"sku"`
	Delta  int       `json:"delta"`
	Reason string    `json:"reason"`
	Actor  string    `json:"actor"`
	At     time.Time `json:"at"`
}

// Commit marks the stock as shipped. Only a live reservation can be committed.
func (r *Reservation) Commit() error {
	if r.Status != Reserved {
		return ErrAlreadyReleased
	}
	r.Status = Committed
	return nil
}

// Package domain holds the Payment aggregate.
package domain

import (
	"errors"
	"fmt"
	"time"
)

type Status string

const (
	StatusAuthorized Status = "AUTHORIZED"
	StatusDeclined   Status = "DECLINED"
	StatusFailed     Status = "FAILED"
	StatusRefunded   Status = "REFUNDED"
)

var (
	ErrInvalidPayment    = errors.New("invalid payment")
	ErrInvalidTransition = errors.New("invalid payment transition")
)

type Payment struct {
	ID             string    `json:"id"`
	OrderID        string    `json:"order_id"`
	IdempotencyKey string    `json:"-"`
	RequestHash    string    `json:"-"`
	AmountCents    int64     `json:"amount_cents"`
	Currency       string    `json:"currency"`
	Status         Status    `json:"status"`
	Provider       string    `json:"provider"`
	ProviderRef    string    `json:"provider_ref,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func New(id, orderID, key, hash string, amount int64, currency, provider string, now time.Time) (*Payment, error) {
	if orderID == "" || key == "" {
		return nil, fmt.Errorf("%w: order_id and idempotency key required", ErrInvalidPayment)
	}
	if amount <= 0 || len(currency) != 3 {
		return nil, fmt.Errorf("%w: amount must be positive and currency ISO-4217", ErrInvalidPayment)
	}
	return &Payment{ID: id, OrderID: orderID, IdempotencyKey: key, RequestHash: hash, AmountCents: amount,
		Currency: currency, Provider: provider, CreatedAt: now, UpdatedAt: now}, nil
}

func (p *Payment) Authorize(ref string, now time.Time) {
	p.Status, p.ProviderRef, p.UpdatedAt = StatusAuthorized, ref, now
}
func (p *Payment) Decline(reason string, now time.Time) {
	p.Status, p.Reason, p.UpdatedAt = StatusDeclined, reason, now
}
func (p *Payment) Fail(reason string, now time.Time) {
	p.Status, p.Reason, p.UpdatedAt = StatusFailed, reason, now
}

func (p *Payment) Succeeded() bool { return p.Status == StatusAuthorized }

// Refund is only legal from AUTHORIZED. A second refund is reported so the
// caller can treat it as idempotent.
func (p *Payment) Refund(reason string, now time.Time) error {
	if p.Status != StatusAuthorized {
		return fmt.Errorf("%w: cannot refund %s", ErrInvalidTransition, p.Status)
	}
	p.Status, p.Reason, p.UpdatedAt = StatusRefunded, reason, now
	return nil
}

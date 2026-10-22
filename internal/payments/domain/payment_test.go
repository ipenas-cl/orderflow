package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewValidates(t *testing.T) {
	if _, err := New("p", "o", "k", "h", 0, "USD", "fake", time.Now()); !errors.Is(err, ErrInvalidPayment) {
		t.Fatal("zero amount")
	}
	if _, err := New("p", "o", "", "h", 10, "USD", "fake", time.Now()); err == nil {
		t.Fatal("missing key")
	}
}

func TestRefundOnlyFromAuthorized(t *testing.T) {
	p, _ := New("p", "o", "k", "h", 10, "USD", "fake", time.Now())
	p.Decline("insufficient funds", time.Now())
	if err := p.Refund("x", time.Now()); err == nil {
		t.Fatal("refunded a declined payment")
	}
	p.Authorize("ref", time.Now())
	if err := p.Refund("x", time.Now()); err != nil || p.Status != StatusRefunded {
		t.Fatal(err)
	}
	if err := p.Refund("x", time.Now()); err == nil {
		t.Fatal("double refund")
	}
}

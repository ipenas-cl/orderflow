package domain

import (
	"errors"
	"testing"
)

func TestReserveAndRelease(t *testing.T) {
	i := &Item{SKU: "A", OnHand: 5}
	if err := i.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := i.Reserve(3); !errors.Is(err, ErrInsufficientStock) {
		t.Fatal("overbooked")
	}
	if err := i.Release(3); err != nil || i.Available() != 5 {
		t.Fatal(err, i.Available())
	}
	if err := i.Release(1); err == nil {
		t.Fatal("released more than reserved")
	}
}

func TestAdjustCannotGoBelowReserved(t *testing.T) {
	i := &Item{SKU: "A", OnHand: 5, Reserved: 4}
	if err := i.Adjust(-2); err == nil {
		t.Fatal("allowed")
	}
	if err := i.Adjust(10); err != nil || i.OnHand != 15 {
		t.Fatal(err)
	}
}

func TestReservationReleaseOnce(t *testing.T) {
	r := &Reservation{Status: Reserved}
	if err := r.Release(); err != nil {
		t.Fatal(err)
	}
	if err := r.Release(); !errors.Is(err, ErrAlreadyReleased) {
		t.Fatal("double release")
	}
}

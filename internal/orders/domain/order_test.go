package domain

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newOrder(t *testing.T) *Order {
	o, err := NewOrder("o1", "c1", "USD", []Line{{SKU: "A", Qty: 2, UnitPriceCents: 500}, {SKU: "B", Qty: 1, UnitPriceCents: 250}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestNewOrderValidates(t *testing.T) {
	bad := [][]Line{nil, {{SKU: "", Qty: 1, UnitPriceCents: 1}}, {{SKU: "A", Qty: 0, UnitPriceCents: 1}}, {{SKU: "A", Qty: 1, UnitPriceCents: 1}, {SKU: "A", Qty: 1, UnitPriceCents: 1}}, {{SKU: "A", Qty: 1}}}
	for _, l := range bad {
		if _, err := NewOrder("o", "c", "USD", l, now); !errors.Is(err, ErrInvalidOrder) {
			t.Errorf("%v accepted", l)
		}
	}
	if _, err := NewOrder("o", "c", "US", []Line{{SKU: "A", Qty: 1, UnitPriceCents: 1}}, now); err == nil {
		t.Error("bad currency accepted")
	}
}

func TestTotal(t *testing.T) {
	if got := newOrder(t).Total(); got != 1250 {
		t.Fatal(got)
	}
}

func TestHappyPathTransitions(t *testing.T) {
	o := newOrder(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(o.MarkStockReserved(now))
	must(o.MarkPaid("p1", now))
	must(o.MarkShipped("FAKE", "TRK1", now))
	must(o.MarkDelivered(now))
	if o.Status != StatusDelivered || o.PaymentID != "p1" || len(o.Timeline) != 5 {
		t.Fatalf("%+v", o)
	}
}

func TestIllegalTransitions(t *testing.T) {
	o := newOrder(t)
	if err := o.MarkPaid("p", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("paid before stock")
	}
	if err := o.MarkDelivered(now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("delivered before shipped")
	}
}

func TestCancelCompensations(t *testing.T) {
	o := newOrder(t)
	c, _ := o.Cancel("x", now)
	if c.ReleaseStock || c.RefundPayment {
		t.Fatal("pending needs no compensation")
	}
	o = newOrder(t)
	_ = o.MarkStockReserved(now)
	c, _ = o.Cancel("x", now)
	if !c.ReleaseStock || c.RefundPayment {
		t.Fatal("reserved must release stock")
	}
	o = newOrder(t)
	_ = o.MarkStockReserved(now)
	_ = o.MarkPaid("p", now)
	c, _ = o.Cancel("x", now)
	if !c.ReleaseStock || !c.RefundPayment {
		t.Fatal("paid must release and refund")
	}
	if _, err := o.Cancel("again", now); err == nil {
		t.Fatal("double cancel")
	}
}

func TestRefundPolicy(t *testing.T) {
	o := newOrder(t)
	if o.CanRefund() {
		t.Fatal("unpaid refundable")
	}
	_ = o.MarkStockReserved(now)
	_ = o.MarkPaid("p", now)
	if !o.CanRefund() {
		t.Fatal("paid not refundable")
	}
	if err := o.MarkRefunded("customer request", now); err != nil || o.Status != StatusRefunded {
		t.Fatal(err)
	}
}

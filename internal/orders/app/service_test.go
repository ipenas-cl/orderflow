package app_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/orders/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/orders/app"
	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

func setup(t *testing.T) (*app.Service, *memory.Store, *domain.Order) {
	st := memory.New()
	svc := app.NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	o, err := svc.PlaceOrder(context.Background(), app.PlaceOrder{CustomerID: "c1", Currency: "USD",
		Lines: []domain.Line{{SKU: "A", Qty: 1, UnitPriceCents: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, o
}

func deliver(t *testing.T, svc *app.Service, orderID, subject string, payload any) messaging.Envelope {
	t.Helper()
	env, _ := messaging.New(context.Background(), subject, orderID, payload)
	if err := svc.HandleMessage(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	return env
}

func status(t *testing.T, svc *app.Service, id string) domain.Status {
	o, err := svc.GetOrder(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o.Status
}

func TestPlaceOrderWritesOutboxAtomically(t *testing.T) {
	_, st, o := setup(t)
	if o.Status != domain.StatusPending {
		t.Fatal(o.Status)
	}
	if got := st.Messages.Subjects(); !slices.Equal(got, []string{contracts.SubjInventoryReserve}) {
		t.Fatal(got)
	}
}

func TestSagaHappyPath(t *testing.T) {
	svc, st, o := setup(t)
	deliver(t, svc, o.ID, contracts.SubjInventoryReserved, contracts.StockReserved{OrderID: o.ID})
	deliver(t, svc, o.ID, contracts.SubjPaymentAuthorized, contracts.PaymentAuthorized{OrderID: o.ID, PaymentID: "p1"})
	deliver(t, svc, o.ID, contracts.SubjShipmentStatus, contracts.ShipmentStatus{OrderID: o.ID, Status: "SHIPPED", TrackingNumber: "T1"})
	deliver(t, svc, o.ID, contracts.SubjShipmentStatus, contracts.ShipmentStatus{OrderID: o.ID, Status: "DELIVERED"})
	if s := status(t, svc, o.ID); s != domain.StatusDelivered {
		t.Fatal(s)
	}
	want := []string{contracts.SubjInventoryReserve, contracts.SubjPaymentAuthorize, contracts.SubjOrderConfirmed, contracts.SubjFulfillmentCreate}
	if got := st.Messages.Subjects(); !slices.Equal(got, want) {
		t.Fatal(got)
	}
}

func TestPaymentFailureCompensatesStock(t *testing.T) {
	svc, st, o := setup(t)
	deliver(t, svc, o.ID, contracts.SubjInventoryReserved, contracts.StockReserved{OrderID: o.ID})
	deliver(t, svc, o.ID, contracts.SubjPaymentFailed, contracts.PaymentFailed{OrderID: o.ID, Reason: "declined"})
	if s := status(t, svc, o.ID); s != domain.StatusCancelled {
		t.Fatal(s)
	}
	got := st.Messages.Subjects()
	if !slices.Contains(got, contracts.SubjInventoryRelease) || slices.Contains(got, contracts.SubjPaymentRefund) {
		t.Fatal(got)
	}
}

func TestFulfillmentFailureRefundsAndReleases(t *testing.T) {
	svc, st, o := setup(t)
	deliver(t, svc, o.ID, contracts.SubjInventoryReserved, contracts.StockReserved{OrderID: o.ID})
	deliver(t, svc, o.ID, contracts.SubjPaymentAuthorized, contracts.PaymentAuthorized{OrderID: o.ID, PaymentID: "p1"})
	deliver(t, svc, o.ID, contracts.SubjFulfillmentFailed, contracts.FulfillmentFailed{OrderID: o.ID, Reason: "carrier down"})
	got := st.Messages.Subjects()
	if !slices.Contains(got, contracts.SubjInventoryRelease) || !slices.Contains(got, contracts.SubjPaymentRefund) {
		t.Fatal(got)
	}
}

func TestDuplicateMessageIsIdempotent(t *testing.T) {
	svc, st, o := setup(t)
	env := deliver(t, svc, o.ID, contracts.SubjInventoryReserved, contracts.StockReserved{OrderID: o.ID})
	if err := svc.HandleMessage(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range st.Messages.Subjects() {
		if s == contracts.SubjPaymentAuthorize {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("payment authorize emitted %d times", n)
	}
}

func TestOutOfOrderMessageIsAcked(t *testing.T) {
	svc, _, o := setup(t)
	deliver(t, svc, o.ID, contracts.SubjShipmentStatus, contracts.ShipmentStatus{OrderID: o.ID, Status: "DELIVERED"})
	if s := status(t, svc, o.ID); s != domain.StatusPending {
		t.Fatal(s)
	}
}

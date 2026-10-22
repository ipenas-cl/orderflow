package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/app"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

type mockCarrier struct {
	bookErrs []error
	calls    int
}

func (m *mockCarrier) Book(context.Context, *domain.Shipment) (ports.Label, error) {
	m.calls++
	if len(m.bookErrs) > 0 {
		e := m.bookErrs[0]
		m.bookErrs = m.bookErrs[1:]
		return ports.Label{}, e
	}
	return ports.Label{Carrier: "MOCK", TrackingNumber: "T1"}, nil
}
func (m *mockCarrier) Track(context.Context, string) (domain.Status, error) {
	return domain.Delivered, nil
}

func setup(t *testing.T, c ports.Carrier, customer string) (*app.Service, *memory.Store, string) {
	st := memory.New()
	svc := app.NewService(st, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Retry.BaseDelay = time.Millisecond
	env, _ := messaging.New(context.Background(), contracts.SubjFulfillmentCreate, "o1", contracts.CreateFulfillment{OrderID: "o1", CustomerID: customer})
	if err := svc.HandleMessage(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	_ = svc.HandleMessage(context.Background(), env)
	sh, err := svc.GetByOrder(context.Background(), "o1")
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, sh.ID
}

func TestShipmentFlowRetriesCarrier(t *testing.T) {
	mc := &mockCarrier{bookErrs: []error{errors.New("timeout")}}
	svc, _, id := setup(t, mc, "c1")
	var sh *domain.Shipment
	for i := 0; i < 5; i++ {
		var err error
		if sh, err = svc.Advance(context.Background(), id, "op"); err != nil {
			t.Fatal(err)
		}
	}
	if sh.Status != domain.Delivered || mc.calls != 2 {
		t.Fatal(sh.Status, mc.calls)
	}
}

func TestUndeliverableFailsSaga(t *testing.T) {
	mc := &mockCarrier{bookErrs: []error{ports.ErrUndeliverable}}
	svc, st, id := setup(t, mc, "c1")
	_, _ = svc.Advance(context.Background(), id, "op")
	_, _ = svc.Advance(context.Background(), id, "op")
	sh, _ := svc.Advance(context.Background(), id, "op")
	if sh.Status != domain.Failed || mc.calls != 1 {
		t.Fatal(sh.Status, mc.calls)
	}
	if !slices.Contains(st.Messages.Subjects(), contracts.SubjFulfillmentFailed) {
		t.Fatal(st.Messages.Subjects())
	}
}

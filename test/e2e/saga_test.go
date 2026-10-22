// Package e2e runs the whole saga in-process: real NATS JetStream (embedded
// server), the real outbox relays and consumers, in-memory repositories and
// the PSP simulator over HTTP. No Docker required.
package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/carrier"
	fulmem "github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/memory"
	fulapp "github.com/ipenas-cl/orderflow/internal/fulfillment/app"
	invmem "github.com/ipenas-cl/orderflow/internal/inventory/adapters/memory"
	invapp "github.com/ipenas-cl/orderflow/internal/inventory/app"
	invdomain "github.com/ipenas-cl/orderflow/internal/inventory/domain"
	ordmem "github.com/ipenas-cl/orderflow/internal/orders/adapters/memory"
	ordapp "github.com/ipenas-cl/orderflow/internal/orders/app"
	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	paymem "github.com/ipenas-cl/orderflow/internal/payments/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/provider"
	payapp "github.com/ipenas-cl/orderflow/internal/payments/app"
	"github.com/ipenas-cl/orderflow/internal/payprovider"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/outbox"
)

type stack struct {
	orders *ordapp.Service
	inv    *invmem.Store
	pay    *paymem.Store
	ful    *fulapp.Service
	psp    *payprovider.Server
}

func start(t *testing.T, chaos payprovider.Chaos) *stack {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(ns.Shutdown)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bus := func(name string) *messaging.Bus {
		b, err := messaging.Connect(ctx, ns.ClientURL(), name, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return b
	}
	relay := func(name string, store outbox.Store, b *messaging.Bus) {
		r := &outbox.Relay{Store: store, Pub: b, Service: name, Interval: 20 * time.Millisecond, Log: log}
		go r.Run(ctx)
	}

	psp := payprovider.New(chaos)
	pspSrv := httptest.NewServer(psp.Handler())
	t.Cleanup(pspSrv.Close)

	st := &stack{psp: psp}

	ordStore := ordmem.New()
	st.orders = ordapp.NewService(ordStore, log)
	ob := bus("orders")
	relay("orders", ordStore.Messages, ob)
	must(t, ob.Subscribe(ctx, ordapp.SagaConsumer, ordapp.SagaSubjects, st.orders.HandleMessage))

	st.inv = invmem.New(invdomain.Item{SKU: "SKU-A", OnHand: 10}, invdomain.Item{SKU: "SKU-RARE", OnHand: 1})
	ib := bus("inventory")
	relay("inventory", st.inv.Messages, ib)
	must(t, ib.Subscribe(ctx, invapp.Consumer, invapp.Subjects, invapp.NewService(st.inv, log).HandleMessage))

	st.pay = paymem.New()
	rp := provider.NewResilient(provider.NewFakeClient(pspSrv.URL), provider.Options{
		MaxAttempts: 6, AttemptTimeout: 500 * time.Millisecond, MaxConcurrent: 8, FailureThreshold: 50, OpenTimeout: time.Second}, log)
	rp.Retry.BaseDelay = 5 * time.Millisecond
	pb := bus("payments")
	relay("payments", st.pay.Messages, pb)
	must(t, pb.Subscribe(ctx, payapp.Consumer, payapp.Subjects, payapp.NewService(st.pay, rp, log).HandleMessage))

	fulStore := fulmem.New()
	st.ful = fulapp.NewService(fulStore, carrier.NewFake(0), log)
	fb := bus("fulfillment")
	relay("fulfillment", fulStore.Messages, fb)
	must(t, fb.Subscribe(ctx, fulapp.Consumer, fulapp.Subjects, st.ful.HandleMessage))
	go st.ful.Autopilot(ctx, 30*time.Millisecond)
	return st
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (s *stack) place(t *testing.T, customer, sku string, qty int) *domain.Order {
	o, err := s.orders.PlaceOrder(context.Background(), ordapp.PlaceOrder{CustomerID: customer, Currency: "USD",
		Lines: []domain.Line{{SKU: sku, Qty: qty, UnitPriceCents: 1999}}})
	must(t, err)
	return o
}

func (s *stack) waitFor(t *testing.T, id string, want domain.Status) *domain.Order {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var o *domain.Order
	for time.Now().Before(deadline) {
		o, _ = s.orders.GetOrder(context.Background(), id)
		if o != nil && o.Status == want {
			return o
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("order %s: want %s, got %s; timeline=%+v", id, want, o.Status, o.Timeline)
	return nil
}

func (s *stack) waitStock(t *testing.T, sku string, reserved int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.inv.Item(sku).Reserved == reserved {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s reserved=%d want %d", sku, s.inv.Item(sku).Reserved, reserved)
}

func TestHappyPathOrderIsDelivered(t *testing.T) {
	s := start(t, payprovider.Chaos{})
	o := s.place(t, "alice", "SKU-A", 2)
	o = s.waitFor(t, o.ID, domain.StatusDelivered)
	if o.PaymentID == "" || o.TrackingNumber == "" {
		t.Fatalf("%+v", o)
	}
	s.waitStock(t, "SKU-A", 0)
	if s.inv.Item("SKU-A").OnHand != 8 {
		t.Fatal("stock not committed on shipment")
	}
}

func TestDeclinedPaymentReleasesStock(t *testing.T) {
	s := start(t, payprovider.Chaos{DeclineRate: 1})
	o := s.place(t, "bob", "SKU-A", 3)
	o = s.waitFor(t, o.ID, domain.StatusCancelled)
	s.waitStock(t, "SKU-A", 0)
	t.Logf("timeline: %+v", o.Timeline)
}

func TestOutOfStockIsRejected(t *testing.T) {
	s := start(t, payprovider.Chaos{})
	o := s.place(t, "carol", "SKU-RARE", 5)
	s.waitFor(t, o.ID, domain.StatusCancelled)
	if s.pay.Count() != 0 {
		t.Fatal("payment attempted for rejected stock")
	}
}

func TestFlakyProviderStillConvergesThanksToRetries(t *testing.T) {
	s := start(t, payprovider.Chaos{FailureRate: 0.5, LatencyMS: 5, JitterMS: 10})
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, s.place(t, "dave", "SKU-A", 1).ID)
	}
	for _, id := range ids {
		s.waitFor(t, id, domain.StatusDelivered)
	}
}

func TestUndeliverableAddressRefundsAndReleases(t *testing.T) {
	s := start(t, payprovider.Chaos{})
	o := s.place(t, "undeliverable-erin", "SKU-A", 1)
	o = s.waitFor(t, o.ID, domain.StatusCancelled)
	s.waitStock(t, "SKU-A", 0)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o, _ = s.orders.GetOrder(context.Background(), o.ID)
		if o.Timeline[len(o.Timeline)-1].Step == "payment refunded" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("refund compensation missing: %+v", o.Timeline)
}

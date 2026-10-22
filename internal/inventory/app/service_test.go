package app_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/inventory/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/inventory/app"
	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
)

func setup() (*app.Service, *memory.Store) {
	st := memory.New(domain.Item{SKU: "A", OnHand: 5}, domain.Item{SKU: "B", OnHand: 1})
	return app.NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func send(t *testing.T, s *app.Service, subj string, p any) messaging.Envelope {
	env, _ := messaging.New(context.Background(), subj, "o1", p)
	if err := s.HandleMessage(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestReserveAllOrNothing(t *testing.T) {
	s, st := setup()
	send(t, s, contracts.SubjInventoryReserve, contracts.ReserveStock{OrderID: "o1", Lines: []contracts.Line{{SKU: "A", Qty: 2}, {SKU: "B", Qty: 2}}})
	if st.Item("A").Reserved != 0 {
		t.Fatal("partial reservation leaked")
	}
	if got := st.Messages.Subjects(); !slices.Equal(got, []string{contracts.SubjInventoryRejected}) {
		t.Fatal(got)
	}
}

func TestReserveThenReleaseIsIdempotent(t *testing.T) {
	s, st := setup()
	env := send(t, s, contracts.SubjInventoryReserve, contracts.ReserveStock{OrderID: "o1", Lines: []contracts.Line{{SKU: "A", Qty: 2}}})
	_ = s.HandleMessage(context.Background(), env) // redelivery
	if st.Item("A").Reserved != 2 {
		t.Fatalf("reserved=%d", st.Item("A").Reserved)
	}
	send(t, s, contracts.SubjInventoryRelease, contracts.ReleaseStock{OrderID: "o1"})
	send(t, s, contracts.SubjInventoryRelease, contracts.ReleaseStock{OrderID: "o1"}) // new id, same order
	if st.Item("A").Reserved != 0 {
		t.Fatal("release not idempotent")
	}
}

func TestAdjustAudits(t *testing.T) {
	s, _ := setup()
	if _, err := s.Adjust(context.Background(), app.AdjustStock{SKU: "A", Delta: 10, Reason: "receiving", Actor: "ana"}); err != nil {
		t.Fatal(err)
	}
	adj, _ := s.Adjustments(context.Background(), 10)
	if len(adj) != 1 || adj[0].Actor != "ana" {
		t.Fatal(adj)
	}
	if _, err := s.Adjust(context.Background(), app.AdjustStock{SKU: "A", Delta: -100, Reason: "x"}); err == nil {
		t.Fatal("negative stock allowed")
	}
}

func TestShippedCommitsReservation(t *testing.T) {
	s, st := setup()
	send(t, s, contracts.SubjInventoryReserve, contracts.ReserveStock{OrderID: "o1", Lines: []contracts.Line{{SKU: "A", Qty: 2}}})
	send(t, s, contracts.SubjShipmentStatus, contracts.ShipmentStatus{OrderID: "o1", Status: "SHIPPED"})
	send(t, s, contracts.SubjInventoryRelease, contracts.ReleaseStock{OrderID: "o1"}) // too late: no-op
	if it := st.Item("A"); it.OnHand != 3 || it.Reserved != 0 {
		t.Fatalf("%+v", it)
	}
}

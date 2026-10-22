// Package app holds the inventory use cases (saga participant).
package app

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/inventory/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

const Consumer = "inventory"

var Subjects = []string{contracts.SubjInventoryReserve, contracts.SubjInventoryRelease, contracts.SubjShipmentStatus}

type Service struct {
	uow ports.UnitOfWork
	log *slog.Logger
	Now func() time.Time
}

func NewService(uow ports.UnitOfWork, log *slog.Logger) *Service {
	return &Service{uow: uow, log: log, Now: func() time.Time { return time.Now().UTC() }}
}

var errDuplicate = errors.New("duplicate")

func (s *Service) HandleMessage(ctx context.Context, env messaging.Envelope) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		first, err := tx.Inbox.MarkProcessed(ctx, Consumer, env.ID)
		if err != nil {
			return err
		}
		if !first {
			return errDuplicate
		}
		switch env.Subject {
		case contracts.SubjInventoryReserve:
			var m contracts.ReserveStock
			if err := env.Decode(&m); err != nil {
				return err
			}
			return s.reserve(ctx, tx, m)
		case contracts.SubjInventoryRelease:
			var m contracts.ReleaseStock
			if err := env.Decode(&m); err != nil {
				return err
			}
			return s.release(ctx, tx, m.OrderID)
		case contracts.SubjShipmentStatus:
			var m contracts.ShipmentStatus
			if err := env.Decode(&m); err != nil {
				return err
			}
			if m.Status == "SHIPPED" {
				return s.commit(ctx, tx, m.OrderID)
			}
		}
		return nil
	})
	if errors.Is(err, errDuplicate) {
		observability.ConsumerMessages.WithLabelValues(Consumer, "duplicate").Inc()
		return nil
	}
	return err
}

// reserve is all-or-nothing. Rows are locked in SKU order to avoid deadlocks
// between concurrent orders.
func (s *Service) reserve(ctx context.Context, tx ports.Tx, m contracts.ReserveStock) error {
	if r, err := tx.Reservations.Get(ctx, m.OrderID); err == nil && r != nil {
		return nil // already handled under another message id
	}
	lines := append([]contracts.Line(nil), m.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKU < lines[j].SKU })
	var items []*domain.Item
	for _, l := range lines {
		it, err := tx.Items.GetForUpdate(ctx, l.SKU)
		if errors.Is(err, ports.ErrNotFound) {
			return s.reject(ctx, tx, m.OrderID, "unknown sku "+l.SKU)
		}
		if err != nil {
			return err
		}
		if err := it.Reserve(l.Qty); err != nil {
			return s.reject(ctx, tx, m.OrderID, err.Error())
		}
		items = append(items, it)
	}
	for _, it := range items {
		if err := tx.Items.Save(ctx, it); err != nil {
			return err
		}
	}
	res := &domain.Reservation{OrderID: m.OrderID, Status: domain.Reserved}
	for _, l := range lines {
		res.Lines = append(res.Lines, domain.Line{SKU: l.SKU, Qty: l.Qty})
	}
	if err := tx.Reservations.Save(ctx, res); err != nil {
		return err
	}
	return emit(ctx, tx, contracts.SubjInventoryReserved, m.OrderID, contracts.StockReserved{OrderID: m.OrderID})
}

func (s *Service) reject(ctx context.Context, tx ports.Tx, orderID, reason string) error {
	return emit(ctx, tx, contracts.SubjInventoryRejected, orderID, contracts.StockRejected{OrderID: orderID, Reason: reason})
}

// release is the compensation for reserve. Releasing twice or releasing an
// order that never reserved is a no-op (compensations must be idempotent).
func (s *Service) release(ctx context.Context, tx ports.Tx, orderID string) error {
	r, err := tx.Reservations.Get(ctx, orderID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := r.Release(); errors.Is(err, domain.ErrAlreadyReleased) {
		return nil
	}
	lines := append([]domain.Line(nil), r.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKU < lines[j].SKU })
	for _, l := range lines {
		it, err := tx.Items.GetForUpdate(ctx, l.SKU)
		if err != nil {
			return err
		}
		if err := it.Release(l.Qty); err != nil {
			return err
		}
		if err := tx.Items.Save(ctx, it); err != nil {
			return err
		}
	}
	if err := tx.Reservations.Save(ctx, r); err != nil {
		return err
	}
	return emit(ctx, tx, contracts.SubjInventoryReleased, orderID, contracts.StockReleased{OrderID: orderID})
}

// commit decrements on-hand stock once the goods leave the warehouse.
func (s *Service) commit(ctx context.Context, tx ports.Tx, orderID string) error {
	r, err := tx.Reservations.Get(ctx, orderID)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Commit() != nil {
		return nil
	}
	lines := append([]domain.Line(nil), r.Lines...)
	sort.Slice(lines, func(i, j int) bool { return lines[i].SKU < lines[j].SKU })
	for _, l := range lines {
		it, err := tx.Items.GetForUpdate(ctx, l.SKU)
		if err != nil {
			return err
		}
		if err := it.Commit(l.Qty); err != nil {
			return err
		}
		if err := tx.Items.Save(ctx, it); err != nil {
			return err
		}
	}
	return tx.Reservations.Save(ctx, r)
}

type AdjustStock struct {
	SKU    string `json:"sku"`
	Name   string `json:"name,omitempty"`
	Delta  int    `json:"delta"`
	Reason string `json:"reason"`
	Actor  string `json:"actor"`
}

// Adjust changes on-hand stock and appends an audit record. Unknown SKUs are
// created (receiving a new product).
func (s *Service) Adjust(ctx context.Context, cmd AdjustStock) (*domain.Item, error) {
	if cmd.SKU == "" || cmd.Reason == "" || cmd.Delta == 0 {
		return nil, domain.ErrInvalidQuantity
	}
	var out *domain.Item
	err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		it, err := tx.Items.GetForUpdate(ctx, cmd.SKU)
		if errors.Is(err, ports.ErrNotFound) {
			it, err = &domain.Item{SKU: cmd.SKU, Name: cmd.Name}, nil
		}
		if err != nil {
			return err
		}
		if err := it.Adjust(cmd.Delta); err != nil {
			return err
		}
		if err := tx.Items.Save(ctx, it); err != nil {
			return err
		}
		out = it
		return tx.Adjustments.Append(ctx, &domain.Adjustment{SKU: cmd.SKU, Delta: cmd.Delta, Reason: cmd.Reason, Actor: cmd.Actor, At: s.Now()})
	})
	return out, err
}

func (s *Service) List(ctx context.Context) (items []*domain.Item, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error { items, err = tx.Items.List(ctx); return err })
	return
}

func (s *Service) Adjustments(ctx context.Context, limit int) (out []domain.Adjustment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error { out, err = tx.Adjustments.List(ctx, limit); return err })
	return
}

func emit(ctx context.Context, tx ports.Tx, subject, orderID string, payload any) error {
	env, err := messaging.New(ctx, subject, orderID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox.Add(ctx, env)
}

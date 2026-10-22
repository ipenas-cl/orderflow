// Package app holds the fulfillment use cases: create the pick list when the
// order is paid, warehouse steps, carrier booking and tracking.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
	"github.com/ipenas-cl/orderflow/internal/platform/resilience"
)

const Consumer = "fulfillment"

var Subjects = []string{contracts.SubjFulfillmentCreate}

type Service struct {
	uow     ports.UnitOfWork
	carrier ports.Carrier
	log     *slog.Logger
	Now     func() time.Time
	Retry   resilience.RetryPolicy
}

func NewService(uow ports.UnitOfWork, carrier ports.Carrier, log *slog.Logger) *Service {
	return &Service{uow: uow, carrier: carrier, log: log, Now: func() time.Time { return time.Now().UTC() },
		Retry: resilience.RetryPolicy{MaxAttempts: 4, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second}}
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
		var m contracts.CreateFulfillment
		if err := env.Decode(&m); err != nil {
			return err
		}
		if _, err := tx.Shipments.GetByOrder(ctx, m.OrderID); err == nil {
			return nil
		}
		lines := make([]domain.Line, len(m.Lines))
		for i, l := range m.Lines {
			lines[i] = domain.Line{SKU: l.SKU, Qty: l.Qty}
		}
		sh := domain.New(uuid.NewString(), m.OrderID, m.CustomerID, lines, s.Now())
		if err := tx.Shipments.Create(ctx, sh); err != nil {
			return err
		}
		return emitStatus(ctx, tx, sh)
	})
	if errors.Is(err, errDuplicate) {
		observability.ConsumerMessages.WithLabelValues(Consumer, "duplicate").Inc()
		return nil
	}
	return err
}

// Advance performs the next step for a shipment: PENDING->PICKING->PACKED,
// PACKED->SHIPPED (books the carrier) and tracking steps afterwards.
func (s *Service) Advance(ctx context.Context, id, actor string) (*domain.Shipment, error) {
	var sh *domain.Shipment
	if err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) (err error) { sh, err = tx.Shipments.Get(ctx, id); return }); err != nil {
		return nil, err
	}
	if sh.Status == domain.Packed {
		return s.ship(ctx, sh, actor)
	}
	if sh.Status == domain.Shipped || sh.Status == domain.InTransit {
		st, err := s.carrier.Track(ctx, sh.TrackingNumber)
		if err != nil {
			return nil, err
		}
		if st == sh.Status {
			return sh, nil
		}
	}
	return sh, s.save(ctx, sh.ID, func(cur *domain.Shipment) error { return cur.Advance(actor, s.Now()) }, &sh)
}

// ship calls the carrier outside the transaction with retries; a permanent
// rejection fails the shipment and triggers the saga compensation.
func (s *Service) ship(ctx context.Context, sh *domain.Shipment, actor string) (*domain.Shipment, error) {
	var label ports.Label
	err := resilience.Retry(ctx, s.Retry, func(ctx context.Context) error {
		var err error
		label, err = s.carrier.Book(ctx, sh)
		if errors.Is(err, ports.ErrUndeliverable) {
			return resilience.Permanent(err)
		}
		return err
	})
	if errors.Is(err, ports.ErrUndeliverable) {
		ferr := s.save(ctx, sh.ID, func(cur *domain.Shipment) error { return cur.Fail(err.Error(), actor, s.Now()) }, &sh)
		return sh, ferr
	}
	if err != nil {
		return nil, err // transient: operator or autopilot retries later
	}
	return sh, s.save(ctx, sh.ID, func(cur *domain.Shipment) error {
		return cur.Ship(label.Carrier, label.TrackingNumber, actor, s.Now())
	}, &sh)
}

func (s *Service) save(ctx context.Context, id string, mutate func(*domain.Shipment) error, out **domain.Shipment) error {
	return s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		cur, err := tx.Shipments.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := mutate(cur); err != nil {
			return err
		}
		if err := tx.Shipments.Update(ctx, cur); err != nil {
			return err
		}
		*out = cur
		if cur.Status == domain.Failed {
			return emit(ctx, tx, contracts.SubjFulfillmentFailed, cur.OrderID, contracts.FulfillmentFailed{OrderID: cur.OrderID, Reason: cur.FailureReason})
		}
		return emitStatus(ctx, tx, cur)
	})
}

func (s *Service) Get(ctx context.Context, id string) (sh *domain.Shipment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error { sh, err = tx.Shipments.Get(ctx, id); return err })
	return
}

func (s *Service) GetByOrder(ctx context.Context, orderID string) (sh *domain.Shipment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		sh, err = tx.Shipments.GetByOrder(ctx, orderID)
		return err
	})
	return
}

func (s *Service) List(ctx context.Context, statuses []domain.Status, limit int) (out []*domain.Shipment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		out, err = tx.Shipments.List(ctx, statuses, limit)
		return err
	})
	return
}

// Autopilot advances open shipments on an interval (demo / simulated warehouse).
func (s *Service) Autopilot(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		open, err := s.List(ctx, []domain.Status{domain.Pending, domain.Picking, domain.Packed, domain.Shipped, domain.InTransit}, 50)
		if err != nil {
			continue
		}
		for _, sh := range open {
			if _, err := s.Advance(ctx, sh.ID, "autopilot"); err != nil && ctx.Err() == nil {
				s.log.Warn("autopilot advance failed", "shipment", sh.ID, "err", err)
			}
		}
	}
}

func emitStatus(ctx context.Context, tx ports.Tx, sh *domain.Shipment) error {
	return emit(ctx, tx, contracts.SubjShipmentStatus, sh.OrderID, contracts.ShipmentStatus{
		OrderID: sh.OrderID, ShipmentID: sh.ID, Status: string(sh.Status), Carrier: sh.Carrier, TrackingNumber: sh.TrackingNumber})
}

func emit(ctx context.Context, tx ports.Tx, subject, orderID string, payload any) error {
	env, err := messaging.New(ctx, subject, orderID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox.Add(ctx, env)
}

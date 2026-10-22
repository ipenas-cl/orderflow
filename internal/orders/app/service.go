// Package app holds the orders use cases and the saga orchestrator.
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/orders/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

const SagaConsumer = "orders-saga"

// SagaSubjects are the replies the orchestrator listens to.
var SagaSubjects = []string{
	contracts.SubjInventoryReserved, contracts.SubjInventoryRejected,
	contracts.SubjPaymentAuthorized, contracts.SubjPaymentFailed, contracts.SubjPaymentRefunded,
	contracts.SubjShipmentStatus, contracts.SubjFulfillmentFailed,
}

type Service struct {
	uow   ports.UnitOfWork
	log   *slog.Logger
	Now   func() time.Time
	NewID func() string
}

func NewService(uow ports.UnitOfWork, log *slog.Logger) *Service {
	return &Service{uow: uow, log: log, Now: func() time.Time { return time.Now().UTC() }, NewID: uuid.NewString}
}

type PlaceOrder struct {
	CustomerID string        `json:"customer_id"`
	Currency   string        `json:"currency"`
	Lines      []domain.Line `json:"lines"`
}

// PlaceOrder persists the order and the first saga command in one transaction
// (transactional outbox: no dual write).
func (s *Service) PlaceOrder(ctx context.Context, cmd PlaceOrder) (*domain.Order, error) {
	o, err := domain.NewOrder(s.NewID(), cmd.CustomerID, cmd.Currency, cmd.Lines, s.Now())
	if err != nil {
		return nil, err
	}
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		if err := tx.Orders.Create(ctx, o); err != nil {
			return err
		}
		return emit(ctx, tx, contracts.SubjInventoryReserve, o.ID, contracts.ReserveStock{OrderID: o.ID, Lines: contractLines(o)})
	})
	if err != nil {
		return nil, err
	}
	observability.SagaOutcomes.WithLabelValues(string(o.Status)).Inc()
	return o, nil
}

func (s *Service) GetOrder(ctx context.Context, id string) (*domain.Order, error) {
	return s.uow.Orders().Get(ctx, id)
}

func (s *Service) ListOrders(ctx context.Context, f ports.ListFilter) ([]*domain.Order, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	return s.uow.Orders().List(ctx, f)
}

var errDuplicate = errors.New("duplicate message")

// HandleMessage is the saga orchestrator: each reply advances the order state
// machine and emits the next command or the compensations. The inbox insert,
// the aggregate update and the outbox writes share one transaction, so a
// redelivered message is a no-op.
func (s *Service) HandleMessage(ctx context.Context, env messaging.Envelope) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		first, err := tx.Inbox.MarkProcessed(ctx, SagaConsumer, env.ID)
		if err != nil {
			return err
		}
		if !first {
			return errDuplicate
		}
		o, err := tx.Orders.Get(ctx, env.CorrelationID)
		if err != nil {
			return err
		}
		before := o.Status
		if err := s.apply(ctx, tx, o, env); err != nil {
			if errors.Is(err, domain.ErrInvalidTransition) {
				// Stale or out-of-order reply: record it and ack.
				s.log.WarnContext(ctx, "saga: ignoring out-of-order message", "order", o.ID, "subject", env.Subject, "status", o.Status)
				return nil
			}
			return err
		}
		if err := tx.Orders.Update(ctx, o); err != nil {
			return err
		}
		if o.Status != before {
			observability.SagaOutcomes.WithLabelValues(string(o.Status)).Inc()
		}
		return nil
	})
	if errors.Is(err, errDuplicate) {
		observability.ConsumerMessages.WithLabelValues(SagaConsumer, "duplicate").Inc()
		return nil
	}
	return err
}

func (s *Service) apply(ctx context.Context, tx ports.Tx, o *domain.Order, env messaging.Envelope) error {
	now := s.Now()
	switch env.Subject {
	case contracts.SubjInventoryReserved:
		if err := o.MarkStockReserved(now); err != nil {
			return err
		}
		return emit(ctx, tx, contracts.SubjPaymentAuthorize, o.ID, contracts.AuthorizePayment{
			OrderID: o.ID, CustomerID: o.CustomerID, AmountCents: o.Total(), Currency: o.Currency})

	case contracts.SubjInventoryRejected:
		var m contracts.StockRejected
		_ = env.Decode(&m)
		return s.cancel(ctx, tx, o, "stock rejected: "+m.Reason)

	case contracts.SubjPaymentAuthorized:
		var m contracts.PaymentAuthorized
		if err := env.Decode(&m); err != nil {
			return err
		}
		if err := o.MarkPaid(m.PaymentID, now); err != nil {
			return err
		}
		if err := emit(ctx, tx, contracts.SubjOrderConfirmed, o.ID, contracts.OrderConfirmed{OrderID: o.ID}); err != nil {
			return err
		}
		return emit(ctx, tx, contracts.SubjFulfillmentCreate, o.ID, contracts.CreateFulfillment{
			OrderID: o.ID, CustomerID: o.CustomerID, Lines: contractLines(o)})

	case contracts.SubjPaymentFailed:
		var m contracts.PaymentFailed
		_ = env.Decode(&m)
		return s.cancel(ctx, tx, o, "payment failed: "+m.Reason)

	case contracts.SubjFulfillmentFailed:
		var m contracts.FulfillmentFailed
		_ = env.Decode(&m)
		return s.cancel(ctx, tx, o, "fulfillment failed: "+m.Reason)

	case contracts.SubjPaymentRefunded:
		var m contracts.PaymentRefunded
		_ = env.Decode(&m)
		return o.MarkRefunded(m.Reason, now)

	case contracts.SubjShipmentStatus:
		var m contracts.ShipmentStatus
		if err := env.Decode(&m); err != nil {
			return err
		}
		switch m.Status {
		case "SHIPPED":
			return o.MarkShipped(m.Carrier, m.TrackingNumber, now)
		case "DELIVERED":
			return o.MarkDelivered(now)
		default:
			o.Note("fulfillment "+lower(m.Status), m.TrackingNumber, now)
			return nil
		}
	}
	s.log.WarnContext(ctx, "saga: unknown subject", "subject", env.Subject)
	return nil
}

// cancel applies the compensations for every completed step.
func (s *Service) cancel(ctx context.Context, tx ports.Tx, o *domain.Order, reason string) error {
	paymentID := o.PaymentID
	comp, err := o.Cancel(reason, s.Now())
	if err != nil {
		return err
	}
	if comp.ReleaseStock {
		o.Note("compensation: release stock", "", s.Now())
		if err := emit(ctx, tx, contracts.SubjInventoryRelease, o.ID, contracts.ReleaseStock{OrderID: o.ID, Reason: reason}); err != nil {
			return err
		}
	}
	if comp.RefundPayment {
		o.Note("compensation: refund payment", paymentID, s.Now())
		if err := emit(ctx, tx, contracts.SubjPaymentRefund, o.ID, contracts.RefundPayment{OrderID: o.ID, PaymentID: paymentID, Reason: reason}); err != nil {
			return err
		}
	}
	return emit(ctx, tx, contracts.SubjOrderCancelled, o.ID, contracts.OrderCancelled{OrderID: o.ID, Reason: reason})
}

func emit(ctx context.Context, tx ports.Tx, subject, orderID string, payload any) error {
	env, err := messaging.New(ctx, subject, orderID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox.Add(ctx, env)
}

func contractLines(o *domain.Order) []contracts.Line {
	out := make([]contracts.Line, len(o.Lines))
	for i, l := range o.Lines {
		out[i] = contracts.Line{SKU: l.SKU, Qty: l.Qty}
	}
	return out
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

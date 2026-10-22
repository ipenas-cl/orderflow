// Package app holds the payments use cases.
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ipenas-cl/orderflow/internal/contracts"
	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

const (
	Consumer        = "payments"
	WebhookConsumer = "payments-webhooks"
)

var (
	Subjects               = []string{contracts.SubjPaymentAuthorize, contracts.SubjPaymentRefund}
	ErrIdempotencyMismatch = errors.New("idempotency key reused with a different request")
)

type Service struct {
	uow      ports.UnitOfWork
	provider ports.PaymentProvider
	log      *slog.Logger
	Now      func() time.Time
}

func NewService(uow ports.UnitOfWork, provider ports.PaymentProvider, log *slog.Logger) *Service {
	return &Service{uow: uow, provider: provider, log: log, Now: func() time.Time { return time.Now().UTC() }}
}

type Authorize struct {
	IdempotencyKey string `json:"-"`
	OrderID        string `json:"order_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
}

func (a Authorize) hash() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", a.OrderID, a.AmountCents, a.Currency)))
	return hex.EncodeToString(h[:])
}

// Authorize is idempotent on IdempotencyKey: a replay returns the stored
// result (replayed=true) and never charges twice. The key is also forwarded
// to the provider so a crash between charge and commit is safe.
func (s *Service) Authorize(ctx context.Context, cmd Authorize) (p *domain.Payment, replayed bool, err error) {
	return s.authorize(ctx, cmd, false, nil)
}

func (s *Service) authorize(ctx context.Context, cmd Authorize, persistTransient bool, inTx func(context.Context, ports.Tx) error) (*domain.Payment, bool, error) {
	var existing *domain.Payment
	_ = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		existing, _ = tx.Payments.GetByKey(ctx, cmd.IdempotencyKey)
		return nil
	})
	if existing != nil {
		if existing.RequestHash != cmd.hash() {
			return nil, false, ErrIdempotencyMismatch
		}
		return existing, true, nil
	}
	p, err := domain.New(uuid.NewString(), cmd.OrderID, cmd.IdempotencyKey, cmd.hash(), cmd.AmountCents, cmd.Currency, s.provider.Name(), s.Now())
	if err != nil {
		return nil, false, err
	}

	// External call happens outside the DB transaction (never hold a row lock
	// across a network call).
	ref, perr := s.provider.Authorize(ctx, ports.AuthorizeRequest{IdempotencyKey: cmd.IdempotencyKey, OrderID: cmd.OrderID, AmountCents: cmd.AmountCents, Currency: cmd.Currency})
	switch {
	case perr == nil:
		p.Authorize(ref, s.Now())
	case errors.Is(perr, ports.ErrDeclined):
		p.Decline(perr.Error(), s.Now())
	default:
		if !persistTransient {
			return nil, false, fmt.Errorf("%w: %w", ports.ErrProviderUnavailable, perr)
		}
		p.Fail("provider unavailable: "+perr.Error(), s.Now())
	}

	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		if inTx != nil {
			if err := inTx(ctx, tx); err != nil {
				return err
			}
		}
		if err := tx.Payments.Create(ctx, p); err != nil {
			return err
		}
		if p.Succeeded() {
			return emit(ctx, tx, contracts.SubjPaymentAuthorized, p.OrderID, contracts.PaymentAuthorized{OrderID: p.OrderID, PaymentID: p.ID, ProviderRef: p.ProviderRef})
		}
		return emit(ctx, tx, contracts.SubjPaymentFailed, p.OrderID, contracts.PaymentFailed{OrderID: p.OrderID, Reason: p.Reason})
	})
	if errors.Is(err, ports.ErrDuplicate) {
		// Lost a race with a concurrent request for the same key.
		var winner *domain.Payment
		_ = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
			winner, err = tx.Payments.GetByKey(ctx, cmd.IdempotencyKey)
			return err
		})
		return winner, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return p, false, nil
}

type Refund struct {
	IdempotencyKey string `json:"-"`
	PaymentID      string `json:"payment_id"`
	Reason         string `json:"reason"`
}

// Refund refunds an authorized payment through the provider and emits
// PaymentRefunded. Refunding an already refunded payment is a no-op.
func (s *Service) Refund(ctx context.Context, cmd Refund) (*domain.Payment, error) {
	var p *domain.Payment
	if err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) (err error) {
		p, err = tx.Payments.GetByID(ctx, cmd.PaymentID)
		return
	}); err != nil {
		return nil, err
	}
	if p.Status == domain.StatusRefunded {
		return p, nil
	}
	if p.Status != domain.StatusAuthorized {
		return nil, fmt.Errorf("%w: cannot refund %s", domain.ErrInvalidTransition, p.Status)
	}
	key := cmd.IdempotencyKey
	if key == "" {
		key = "refund-" + p.ID
	}
	if _, err := s.provider.Refund(ctx, ports.RefundRequest{IdempotencyKey: key, ProviderRef: p.ProviderRef, AmountCents: p.AmountCents, Reason: cmd.Reason}); err != nil {
		return nil, err
	}
	err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		cur, err := tx.Payments.GetByID(ctx, p.ID)
		if err != nil {
			return err
		}
		if err := cur.Refund(cmd.Reason, s.Now()); err != nil {
			return nil // concurrent refund (e.g. webhook) already applied it
		}
		p = cur
		if err := tx.Payments.Update(ctx, cur); err != nil {
			return err
		}
		return emit(ctx, tx, contracts.SubjPaymentRefunded, cur.OrderID, contracts.PaymentRefunded{OrderID: cur.OrderID, PaymentID: cur.ID, Reason: cmd.Reason})
	})
	return p, err
}

var errDuplicate = errors.New("duplicate")

// HandleMessage serves the saga commands.
func (s *Service) HandleMessage(ctx context.Context, env messaging.Envelope) error {
	markInbox := func(ctx context.Context, tx ports.Tx) error {
		first, err := tx.Inbox.MarkProcessed(ctx, Consumer, env.ID)
		if err != nil {
			return err
		}
		if !first {
			return errDuplicate
		}
		return nil
	}
	var err error
	switch env.Subject {
	case contracts.SubjPaymentAuthorize:
		var m contracts.AuthorizePayment
		if err := env.Decode(&m); err != nil {
			return err
		}
		// Saga key = order id: at most one charge per order whatever happens.
		_, _, err = s.authorize(ctx, Authorize{IdempotencyKey: "order-" + m.OrderID, OrderID: m.OrderID, AmountCents: m.AmountCents, Currency: m.Currency}, true, markInbox)
	case contracts.SubjPaymentRefund:
		var m contracts.RefundPayment
		if err := env.Decode(&m); err != nil {
			return err
		}
		_, err = s.Refund(ctx, Refund{IdempotencyKey: "saga-refund-" + m.OrderID, PaymentID: m.PaymentID, Reason: m.Reason})
		if errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, ports.ErrNotFound) {
			err = nil
		}
	}
	if errors.Is(err, errDuplicate) {
		observability.ConsumerMessages.WithLabelValues(Consumer, "duplicate").Inc()
		return nil
	}
	return err
}

// HandleProviderEvent applies a verified webhook (e.g. a refund issued from
// the Stripe dashboard). Webhooks are retried by the PSP, so the event id goes
// through the inbox.
func (s *Service) HandleProviderEvent(ctx context.Context, ev ports.ProviderEvent) error {
	err := s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		first, err := tx.Inbox.MarkProcessed(ctx, WebhookConsumer, ev.ID)
		if err != nil {
			return err
		}
		if !first {
			return errDuplicate
		}
		p, err := tx.Payments.GetByProviderRef(ctx, ev.ProviderRef)
		if errors.Is(err, ports.ErrNotFound) {
			s.log.WarnContext(ctx, "webhook for unknown payment", "ref", ev.ProviderRef, "type", ev.Type)
			return nil
		}
		if err != nil {
			return err
		}
		switch ev.Type {
		case "refunded":
			if p.Refund(ev.Reason, s.Now()) != nil {
				return nil
			}
			if err := tx.Payments.Update(ctx, p); err != nil {
				return err
			}
			return emit(ctx, tx, contracts.SubjPaymentRefunded, p.OrderID, contracts.PaymentRefunded{OrderID: p.OrderID, PaymentID: p.ID, Reason: "provider: " + ev.Reason})
		case "payment_failed":
			if p.Status == domain.StatusAuthorized {
				return nil // a later success already superseded it
			}
			p.Fail(ev.Reason, s.Now())
			return tx.Payments.Update(ctx, p)
		}
		return nil
	})
	if errors.Is(err, errDuplicate) {
		return nil
	}
	return err
}

func (s *Service) Get(ctx context.Context, id string) (p *domain.Payment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error { p, err = tx.Payments.GetByID(ctx, id); return err })
	return
}

func (s *Service) ListByOrder(ctx context.Context, orderID string) (out []*domain.Payment, err error) {
	err = s.uow.Do(ctx, func(ctx context.Context, tx ports.Tx) error {
		out, err = tx.Payments.ListByOrder(ctx, orderID)
		return err
	})
	return
}

func emit(ctx context.Context, tx ports.Tx, subject, orderID string, payload any) error {
	env, err := messaging.New(ctx, subject, orderID, payload)
	if err != nil {
		return err
	}
	return tx.Outbox.Add(ctx, env)
}

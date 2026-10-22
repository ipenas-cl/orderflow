// Package postgres is the Postgres adapter for the payments ports.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
)

const Schema = `
CREATE TABLE IF NOT EXISTS payments (
  id              TEXT PRIMARY KEY,
  order_id        TEXT NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash    TEXT NOT NULL,
  amount_cents    BIGINT NOT NULL CHECK (amount_cents > 0),
  currency        TEXT NOT NULL,
  status          TEXT NOT NULL,
  provider        TEXT NOT NULL,
  provider_ref    TEXT NOT NULL DEFAULT '',
  reason          TEXT NOT NULL DEFAULT '',
  created_at      TIMESTAMPTZ NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS payments_order ON payments (order_id);
CREATE INDEX IF NOT EXISTS payments_provider_ref ON payments (provider_ref) WHERE provider_ref <> '';`

type UnitOfWork struct{ Pool *pgxpool.Pool }

func (u UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	return pg.WithTx(ctx, u.Pool, func(tx pgx.Tx) error {
		return fn(ctx, ports.Tx{Payments: repo{tx}, Outbox: pg.Outbox{Q: tx}, Inbox: pg.Inbox{Q: tx}})
	})
}

type repo struct{ q pg.DBTX }

const cols = `id, order_id, idempotency_key, request_hash, amount_cents, currency, status, provider, provider_ref, reason, created_at, updated_at`

func (r repo) Create(ctx context.Context, p *domain.Payment) error {
	_, err := r.q.Exec(ctx, `INSERT INTO payments (`+cols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.ID, p.OrderID, p.IdempotencyKey, p.RequestHash, p.AmountCents, p.Currency, p.Status, p.Provider, p.ProviderRef, p.Reason, p.CreatedAt, p.UpdatedAt)
	if pg.IsUniqueViolation(err) {
		return ports.ErrDuplicate
	}
	return err
}

func (r repo) Update(ctx context.Context, p *domain.Payment) error {
	_, err := r.q.Exec(ctx, `UPDATE payments SET status=$2, provider_ref=$3, reason=$4, updated_at=$5 WHERE id=$1`,
		p.ID, p.Status, p.ProviderRef, p.Reason, p.UpdatedAt)
	return err
}

func (r repo) one(ctx context.Context, where string, arg any) (*domain.Payment, error) {
	ps, err := r.many(ctx, where+` FOR UPDATE`, arg)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, ports.ErrNotFound
	}
	return ps[0], nil
}

func (r repo) many(ctx context.Context, where string, arg any) ([]*domain.Payment, error) {
	rows, err := r.q.Query(ctx, `SELECT `+cols+` FROM payments WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Payment
	for rows.Next() {
		var p domain.Payment
		if err := rows.Scan(&p.ID, &p.OrderID, &p.IdempotencyKey, &p.RequestHash, &p.AmountCents, &p.Currency, &p.Status,
			&p.Provider, &p.ProviderRef, &p.Reason, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	if errors.Is(rows.Err(), pgx.ErrNoRows) {
		return nil, nil
	}
	return out, rows.Err()
}

func (r repo) GetByID(ctx context.Context, id string) (*domain.Payment, error) {
	return r.one(ctx, `id=$1`, id)
}
func (r repo) GetByKey(ctx context.Context, k string) (*domain.Payment, error) {
	return r.one(ctx, `idempotency_key=$1`, k)
}
func (r repo) GetByProviderRef(ctx context.Context, ref string) (*domain.Payment, error) {
	return r.one(ctx, `provider_ref=$1 AND provider_ref <> ''`, ref)
}
func (r repo) ListByOrder(ctx context.Context, o string) ([]*domain.Payment, error) {
	return r.many(ctx, `order_id=$1 ORDER BY created_at`, o)
}

// Package postgres is the Postgres adapter for the fulfillment ports.
package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
)

const Schema = `
CREATE TABLE IF NOT EXISTS shipments (
  id              TEXT PRIMARY KEY,
  order_id        TEXT NOT NULL UNIQUE,
  customer_id     TEXT NOT NULL,
  lines           JSONB NOT NULL,
  status          TEXT NOT NULL,
  carrier         TEXT NOT NULL DEFAULT '',
  tracking_number TEXT NOT NULL DEFAULT '',
  failure_reason  TEXT NOT NULL DEFAULT '',
  history         JSONB NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS shipments_status ON shipments (status, created_at);`

type UnitOfWork struct{ Pool *pgxpool.Pool }

func (u UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	return pg.WithTx(ctx, u.Pool, func(tx pgx.Tx) error {
		return fn(ctx, ports.Tx{Shipments: repo{tx}, Outbox: pg.Outbox{Q: tx}, Inbox: pg.Inbox{Q: tx}})
	})
}

type repo struct{ q pg.DBTX }

const cols = `id, order_id, customer_id, lines, status, carrier, tracking_number, failure_reason, history, created_at, updated_at`

func (r repo) Create(ctx context.Context, s *domain.Shipment) error {
	l, _ := json.Marshal(s.Lines)
	h, _ := json.Marshal(s.History)
	_, err := r.q.Exec(ctx, `INSERT INTO shipments (`+cols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		s.ID, s.OrderID, s.CustomerID, l, s.Status, s.Carrier, s.TrackingNumber, s.FailureReason, h, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r repo) Update(ctx context.Context, s *domain.Shipment) error {
	h, _ := json.Marshal(s.History)
	_, err := r.q.Exec(ctx, `UPDATE shipments SET status=$2, carrier=$3, tracking_number=$4, failure_reason=$5, history=$6, updated_at=$7 WHERE id=$1`,
		s.ID, s.Status, s.Carrier, s.TrackingNumber, s.FailureReason, h, s.UpdatedAt)
	return err
}

func (r repo) query(ctx context.Context, sql string, args ...any) ([]*domain.Shipment, error) {
	rows, err := r.q.Query(ctx, `SELECT `+cols+` FROM shipments `+sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Shipment
	for rows.Next() {
		var s domain.Shipment
		var l, h []byte
		if err := rows.Scan(&s.ID, &s.OrderID, &s.CustomerID, &l, &s.Status, &s.Carrier, &s.TrackingNumber, &s.FailureReason, &h, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(l, &s.Lines)
		_ = json.Unmarshal(h, &s.History)
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r repo) first(ctx context.Context, sql string, arg any) (*domain.Shipment, error) {
	out, err := r.query(ctx, sql, arg)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ports.ErrNotFound
	}
	return out[0], nil
}

func (r repo) Get(ctx context.Context, id string) (*domain.Shipment, error) {
	return r.first(ctx, `WHERE id=$1 FOR UPDATE`, id)
}
func (r repo) GetByOrder(ctx context.Context, o string) (*domain.Shipment, error) {
	return r.first(ctx, `WHERE order_id=$1`, o)
}
func (r repo) List(ctx context.Context, st []domain.Status, limit int) ([]*domain.Shipment, error) {
	ss := make([]string, len(st))
	for i, s := range st {
		ss[i] = string(s)
	}
	if limit <= 0 {
		limit = 100
	}
	return r.query(ctx, `WHERE (cardinality($1::text[]) = 0 OR status = ANY($1)) ORDER BY created_at LIMIT $2`, ss, limit)
}

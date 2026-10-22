// Package postgres is the Postgres adapter for the orders ports.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/orders/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
)

const Schema = `
CREATE TABLE IF NOT EXISTS orders (
  id              TEXT PRIMARY KEY,
  customer_id     TEXT NOT NULL,
  currency        TEXT NOT NULL,
  lines           JSONB NOT NULL,
  status          TEXT NOT NULL,
  reason          TEXT NOT NULL DEFAULT '',
  payment_id      TEXT NOT NULL DEFAULT '',
  tracking_number TEXT NOT NULL DEFAULT '',
  timeline        JSONB NOT NULL,
  version         INT  NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL,
  updated_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS orders_status_created ON orders (status, created_at DESC);
CREATE INDEX IF NOT EXISTS orders_customer ON orders (customer_id);`

type UnitOfWork struct{ Pool *pgxpool.Pool }

func (u UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	return pg.WithTx(ctx, u.Pool, func(tx pgx.Tx) error {
		return fn(ctx, ports.Tx{Orders: Repo{tx}, Outbox: pg.Outbox{Q: tx}, Inbox: pg.Inbox{Q: tx}})
	})
}

func (u UnitOfWork) Orders() ports.OrderRepository { return Repo{u.Pool} }

type Repo struct{ Q pg.DBTX }

const cols = `id, customer_id, currency, lines, status, reason, payment_id, tracking_number, timeline, version, created_at, updated_at`

func (r Repo) Create(ctx context.Context, o *domain.Order) error {
	lines, _ := json.Marshal(o.Lines)
	tl, _ := json.Marshal(o.Timeline)
	_, err := r.Q.Exec(ctx, `INSERT INTO orders (`+cols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		o.ID, o.CustomerID, o.Currency, lines, o.Status, o.Reason, o.PaymentID, o.TrackingNumber, tl, o.Version, o.CreatedAt, o.UpdatedAt)
	if pg.IsUniqueViolation(err) {
		return ports.ErrConflict
	}
	return err
}

func scan(row pgx.Row) (*domain.Order, error) {
	var o domain.Order
	var lines, tl []byte
	err := row.Scan(&o.ID, &o.CustomerID, &o.Currency, &lines, &o.Status, &o.Reason, &o.PaymentID, &o.TrackingNumber, &tl, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(lines, &o.Lines)
	_ = json.Unmarshal(tl, &o.Timeline)
	return &o, nil
}

func (r Repo) Get(ctx context.Context, id string) (*domain.Order, error) {
	return scan(r.Q.QueryRow(ctx, `SELECT `+cols+` FROM orders WHERE id=$1`, id))
}

func (r Repo) Update(ctx context.Context, o *domain.Order) error {
	tl, _ := json.Marshal(o.Timeline)
	tag, err := r.Q.Exec(ctx, `UPDATE orders SET status=$2, reason=$3, payment_id=$4, tracking_number=$5, timeline=$6,
		updated_at=$7, version=version+1 WHERE id=$1 AND version=$8`,
		o.ID, o.Status, o.Reason, o.PaymentID, o.TrackingNumber, tl, o.UpdatedAt, o.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ports.ErrConflict
	}
	o.Version++
	return nil
}

func (r Repo) List(ctx context.Context, f ports.ListFilter) ([]*domain.Order, error) {
	rows, err := r.Q.Query(ctx, `SELECT `+cols+` FROM orders
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR id LIKE $2 || '%' OR customer_id ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC LIMIT $3`, string(f.Status), f.Query, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Order
	for rows.Next() {
		o, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

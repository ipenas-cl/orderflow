// Package postgres is the Postgres adapter for the inventory ports.
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/inventory/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
)

const Schema = `
CREATE TABLE IF NOT EXISTS items (
  sku      TEXT PRIMARY KEY,
  name     TEXT NOT NULL DEFAULT '',
  on_hand  INT  NOT NULL CHECK (on_hand >= 0),
  reserved INT  NOT NULL CHECK (reserved >= 0),
  version  INT  NOT NULL DEFAULT 0,
  CHECK (reserved <= on_hand)
);
CREATE TABLE IF NOT EXISTS reservations (
  order_id TEXT PRIMARY KEY,
  lines    JSONB NOT NULL,
  status   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS stock_adjustments (
  id     BIGSERIAL PRIMARY KEY,
  sku    TEXT NOT NULL,
  delta  INT  NOT NULL,
  reason TEXT NOT NULL,
  actor  TEXT NOT NULL,
  at     TIMESTAMPTZ NOT NULL
);
INSERT INTO items (sku, name, on_hand, reserved) VALUES
  ('SKU-KEYBOARD', 'Mechanical keyboard', 100, 0),
  ('SKU-MOUSE', 'Wireless mouse', 250, 0),
  ('SKU-MONITOR', '27" 4K monitor', 25, 0),
  ('SKU-LIMITED', 'Limited edition desk mat', 3, 0)
ON CONFLICT (sku) DO NOTHING;`

type UnitOfWork struct{ Pool *pgxpool.Pool }

func (u UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	return pg.WithTx(ctx, u.Pool, func(tx pgx.Tx) error {
		return fn(ctx, ports.Tx{Items: items{tx}, Reservations: reservations{tx}, Adjustments: adjustments{tx},
			Outbox: pg.Outbox{Q: tx}, Inbox: pg.Inbox{Q: tx}})
	})
}

type items struct{ q pg.DBTX }

func (r items) GetForUpdate(ctx context.Context, sku string) (*domain.Item, error) {
	var i domain.Item
	err := r.q.QueryRow(ctx, `SELECT sku, name, on_hand, reserved, version FROM items WHERE sku=$1 FOR UPDATE`, sku).
		Scan(&i.SKU, &i.Name, &i.OnHand, &i.Reserved, &i.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	return &i, err
}

func (r items) Save(ctx context.Context, i *domain.Item) error {
	_, err := r.q.Exec(ctx, `INSERT INTO items (sku, name, on_hand, reserved, version) VALUES ($1,$2,$3,$4,1)
		ON CONFLICT (sku) DO UPDATE SET on_hand=$3, reserved=$4, version=items.version+1`, i.SKU, i.Name, i.OnHand, i.Reserved)
	i.Version++
	return err
}

func (r items) List(ctx context.Context) ([]*domain.Item, error) {
	rows, err := r.q.Query(ctx, `SELECT sku, name, on_hand, reserved, version FROM items ORDER BY sku`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Item
	for rows.Next() {
		var i domain.Item
		if err := rows.Scan(&i.SKU, &i.Name, &i.OnHand, &i.Reserved, &i.Version); err != nil {
			return nil, err
		}
		out = append(out, &i)
	}
	return out, rows.Err()
}

type reservations struct{ q pg.DBTX }

func (r reservations) Get(ctx context.Context, id string) (*domain.Reservation, error) {
	var v domain.Reservation
	var lines []byte
	err := r.q.QueryRow(ctx, `SELECT order_id, lines, status FROM reservations WHERE order_id=$1 FOR UPDATE`, id).Scan(&v.OrderID, &lines, &v.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(lines, &v.Lines)
	return &v, nil
}

func (r reservations) Save(ctx context.Context, v *domain.Reservation) error {
	lines, _ := json.Marshal(v.Lines)
	_, err := r.q.Exec(ctx, `INSERT INTO reservations (order_id, lines, status) VALUES ($1,$2,$3)
		ON CONFLICT (order_id) DO UPDATE SET status=$3`, v.OrderID, lines, v.Status)
	return err
}

type adjustments struct{ q pg.DBTX }

func (a adjustments) Append(ctx context.Context, v *domain.Adjustment) error {
	return a.q.QueryRow(ctx, `INSERT INTO stock_adjustments (sku, delta, reason, actor, at) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		v.SKU, v.Delta, v.Reason, v.Actor, v.At).Scan(&v.ID)
}

func (a adjustments) List(ctx context.Context, limit int) ([]domain.Adjustment, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := a.q.Query(ctx, `SELECT id, sku, delta, reason, actor, at FROM stock_adjustments ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Adjustment{}
	for rows.Next() {
		var v domain.Adjustment
		if err := rows.Scan(&v.ID, &v.SKU, &v.Delta, &v.Reason, &v.Actor, &v.At); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

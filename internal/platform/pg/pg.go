// Package pg holds Postgres helpers shared by the adapters: connection,
// migrations, transactions and the outbox/inbox tables.
package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/outbox"
)

// DBTX is satisfied by *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const PlatformSchema = `
CREATE TABLE IF NOT EXISTS outbox (
  seq          BIGSERIAL PRIMARY KEY,
  id           TEXT UNIQUE NOT NULL,
  subject      TEXT NOT NULL,
  data         JSONB NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS outbox_unpublished ON outbox (seq) WHERE published_at IS NULL;
CREATE TABLE IF NOT EXISTS inbox (
  consumer     TEXT NOT NULL,
  msg_id       TEXT NOT NULL,
  processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer, msg_id)
);`

// Connect retries until the database answers (useful with compose start order).
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 20
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	var pool *pgxpool.Pool
	for i := 0; i < 30; i++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("connect postgres: %w", err)
}

// Migrate applies idempotent DDL, serialized with an advisory lock so several
// replicas can start at once.
func Migrate(ctx context.Context, pool *pgxpool.Pool, ddl ...string) error {
	return WithTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4242)`); err != nil {
			return err
		}
		for _, d := range ddl {
			if _, err := tx.Exec(ctx, d); err != nil {
				return err
			}
		}
		return nil
	})
}

func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func IsUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// Outbox implements messaging.Outbox inside a transaction.
type Outbox struct{ Q DBTX }

func (o Outbox) Add(ctx context.Context, env messaging.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = o.Q.Exec(ctx, `INSERT INTO outbox (id, subject, data) VALUES ($1,$2,$3)`, env.ID, env.Subject, b)
	return err
}

// Inbox implements messaging.Inbox inside a transaction.
type Inbox struct{ Q DBTX }

func (i Inbox) MarkProcessed(ctx context.Context, consumer, id string) (bool, error) {
	tag, err := i.Q.Exec(ctx, `INSERT INTO inbox (consumer, msg_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, consumer, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// OutboxStore implements outbox.Store. Rows are locked with SKIP LOCKED so
// several relay replicas can run without double-publishing in parallel.
type OutboxStore struct{ Pool *pgxpool.Pool }

func (s OutboxStore) Drain(ctx context.Context, limit int, publish func(context.Context, outbox.Record) error) (int, error) {
	n := 0
	err := WithTx(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT seq, id, subject, data FROM outbox WHERE published_at IS NULL
			ORDER BY seq LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return err
		}
		type row struct {
			seq int64
			rec outbox.Record
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.seq, &r.rec.ID, &r.rec.Subject, &r.rec.Data); err != nil {
				return err
			}
			batch = append(batch, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		var done []int64
		var perr error
		for _, r := range batch {
			if perr = publish(ctx, r.rec); perr != nil {
				break
			}
			done = append(done, r.seq)
		}
		if len(done) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE seq = ANY($1)`, done); err != nil {
				return err
			}
		}
		n = len(done)
		if perr != nil && n == 0 {
			return perr
		}
		return nil
	})
	return n, err
}

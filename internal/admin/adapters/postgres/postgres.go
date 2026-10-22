// Package postgres is the append-only audit log adapter.
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ipenas-cl/orderflow/internal/admin/ports"
)

const Schema = `
CREATE TABLE IF NOT EXISTS audit_log (
  id      BIGSERIAL PRIMARY KEY,
  at      TIMESTAMPTZ NOT NULL,
  actor   TEXT NOT NULL,
  role    TEXT NOT NULL,
  action  TEXT NOT NULL,
  target  TEXT NOT NULL,
  detail  TEXT NOT NULL,
  outcome TEXT NOT NULL
);
-- append-only: the application role should be granted INSERT/SELECT only.`

type Audit struct{ Pool *pgxpool.Pool }

func (a Audit) Append(ctx context.Context, e ports.AuditEntry) error {
	_, err := a.Pool.Exec(ctx, `INSERT INTO audit_log (at, actor, role, action, target, detail, outcome) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.At, e.Actor, e.Role, e.Action, e.Target, e.Detail, e.Outcome)
	return err
}

func (a Audit) List(ctx context.Context, limit int) ([]ports.AuditEntry, error) {
	rows, err := a.Pool.Query(ctx, `SELECT id, at, actor, role, action, target, detail, outcome FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ports.AuditEntry{}
	for rows.Next() {
		var e ports.AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Role, &e.Action, &e.Target, &e.Detail, &e.Outcome); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

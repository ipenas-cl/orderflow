// Package memory is an in-memory audit log (tests).
package memory

import (
	"context"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/admin/ports"
)

type Audit struct {
	mu sync.Mutex
	E  []ports.AuditEntry
}

func (a *Audit) Append(_ context.Context, e ports.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e.ID = int64(len(a.E) + 1)
	a.E = append(a.E, e)
	return nil
}

func (a *Audit) List(_ context.Context, limit int) ([]ports.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []ports.AuditEntry{}
	for i := len(a.E) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, a.E[i])
	}
	return out, nil
}

// Package memstore provides in-memory outbox/inbox used by tests and the
// in-process end-to-end suite.
package memstore

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/outbox"
)

type Messages struct {
	mu        sync.Mutex
	pending   []outbox.Record
	inbox     map[string]struct{}
	Published []messaging.Envelope
}

func NewMessages() *Messages { return &Messages{inbox: map[string]struct{}{}} }

// Tx stages outbox/inbox writes until Commit.
type Tx struct {
	m   *Messages
	out []outbox.Record
	env []messaging.Envelope
	in  map[string]struct{}
}

func (m *Messages) Begin() *Tx { return &Tx{m: m, in: map[string]struct{}{}} }

func (t *Tx) Add(_ context.Context, env messaging.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	t.out = append(t.out, outbox.Record{ID: env.ID, Subject: env.Subject, Data: b})
	t.env = append(t.env, env)
	return nil
}

func (t *Tx) MarkProcessed(_ context.Context, consumer, id string) (bool, error) {
	k := consumer + "/" + id
	if _, ok := t.in[k]; ok {
		return false, nil
	}
	t.m.mu.Lock()
	_, seen := t.m.inbox[k]
	t.m.mu.Unlock()
	if seen {
		return false, nil
	}
	t.in[k] = struct{}{}
	return true, nil
}

func (t *Tx) Commit() {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	t.m.pending = append(t.m.pending, t.out...)
	t.m.Published = append(t.m.Published, t.env...)
	for k := range t.in {
		t.m.inbox[k] = struct{}{}
	}
}

// Drain implements outbox.Store.
func (m *Messages) Drain(ctx context.Context, limit int, publish func(context.Context, outbox.Record) error) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for n < limit && len(m.pending) > 0 {
		if err := publish(ctx, m.pending[0]); err != nil {
			return n, err
		}
		m.pending = m.pending[1:]
		n++
	}
	return n, nil
}

// Subjects returns the subjects emitted so far (test helper).
func (m *Messages) Subjects() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.Published))
	for _, e := range m.Published {
		out = append(out, e.Subject)
	}
	return out
}

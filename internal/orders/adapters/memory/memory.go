// Package memory is an in-memory adapter for the orders ports (tests, e2e).
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/orders/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/memstore"
)

type Store struct {
	mu       sync.Mutex
	orders   map[string]domain.Order
	Messages *memstore.Messages
}

func New() *Store { return &Store{orders: map[string]domain.Order{}, Messages: memstore.NewMessages()} }

type repo struct{ m map[string]domain.Order }

func clone(o domain.Order) *domain.Order {
	o.Lines = append([]domain.Line(nil), o.Lines...)
	o.Timeline = append([]domain.TimelineEntry(nil), o.Timeline...)
	return &o
}

func (r repo) Create(_ context.Context, o *domain.Order) error {
	if _, ok := r.m[o.ID]; ok {
		return ports.ErrConflict
	}
	r.m[o.ID] = *clone(*o)
	return nil
}

func (r repo) Get(_ context.Context, id string) (*domain.Order, error) {
	o, ok := r.m[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return clone(o), nil
}

func (r repo) Update(_ context.Context, o *domain.Order) error {
	cur, ok := r.m[o.ID]
	if !ok {
		return ports.ErrNotFound
	}
	if cur.Version != o.Version {
		return ports.ErrConflict
	}
	o.Version++
	r.m[o.ID] = *clone(*o)
	return nil
}

func (r repo) List(_ context.Context, f ports.ListFilter) ([]*domain.Order, error) {
	var out []*domain.Order
	for _, o := range r.m {
		if f.Status != "" && o.Status != f.Status {
			continue
		}
		if f.Query != "" && !strings.HasPrefix(o.ID, f.Query) && !strings.Contains(o.CustomerID, f.Query) {
			continue
		}
		out = append(out, clone(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// Do snapshots state, runs fn and commits only on success.
func (s *Store) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := make(map[string]domain.Order, len(s.orders))
	for k, v := range s.orders {
		staged[k] = v
	}
	mtx := s.Messages.Begin()
	if err := fn(ctx, ports.Tx{Orders: repo{staged}, Outbox: mtx, Inbox: mtx}); err != nil {
		return err
	}
	s.orders = staged
	mtx.Commit()
	return nil
}

type readRepo struct{ s *Store }

func (r readRepo) Create(ctx context.Context, o *domain.Order) error {
	return r.s.Do(ctx, func(ctx context.Context, tx ports.Tx) error { return tx.Orders.Create(ctx, o) })
}
func (r readRepo) Update(ctx context.Context, o *domain.Order) error {
	return r.s.Do(ctx, func(ctx context.Context, tx ports.Tx) error { return tx.Orders.Update(ctx, o) })
}
func (r readRepo) Get(ctx context.Context, id string) (*domain.Order, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return repo{r.s.orders}.Get(ctx, id)
}
func (r readRepo) List(ctx context.Context, f ports.ListFilter) ([]*domain.Order, error) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return repo{r.s.orders}.List(ctx, f)
}

func (s *Store) Orders() ports.OrderRepository { return readRepo{s} }

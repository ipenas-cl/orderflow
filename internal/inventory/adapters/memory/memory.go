// Package memory is an in-memory adapter for the inventory ports.
package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/inventory/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/memstore"
)

type Store struct {
	mu           sync.Mutex
	items        map[string]domain.Item
	reservations map[string]domain.Reservation
	adjustments  []domain.Adjustment
	Messages     *memstore.Messages
}

func New(seed ...domain.Item) *Store {
	s := &Store{items: map[string]domain.Item{}, reservations: map[string]domain.Reservation{}, Messages: memstore.NewMessages()}
	for _, i := range seed {
		s.items[i.SKU] = i
	}
	return s
}

func (s *Store) Item(sku string) domain.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[sku]
}

type tx struct {
	items        map[string]domain.Item
	reservations map[string]domain.Reservation
	adjustments  *[]domain.Adjustment
}

func (t *tx) GetForUpdate(_ context.Context, sku string) (*domain.Item, error) {
	i, ok := t.items[sku]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return &i, nil
}
func (t *tx) Save(_ context.Context, i *domain.Item) error {
	i.Version++
	t.items[i.SKU] = *i
	return nil
}
func (t *tx) List(context.Context) ([]*domain.Item, error) {
	var out []*domain.Item
	for _, i := range t.items {
		out = append(out, &i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].SKU < out[b].SKU })
	return out, nil
}

type resRepo struct{ t *tx }

func (r resRepo) Get(_ context.Context, id string) (*domain.Reservation, error) {
	v, ok := r.t.reservations[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return &v, nil
}
func (r resRepo) Save(_ context.Context, v *domain.Reservation) error {
	r.t.reservations[v.OrderID] = *v
	return nil
}

type adjRepo struct{ t *tx }

func (a adjRepo) Append(_ context.Context, v *domain.Adjustment) error {
	v.ID = int64(len(*a.t.adjustments) + 1)
	*a.t.adjustments = append(*a.t.adjustments, *v)
	return nil
}
func (a adjRepo) List(_ context.Context, limit int) ([]domain.Adjustment, error) {
	out := append([]domain.Adjustment(nil), *a.t.adjustments...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &tx{items: map[string]domain.Item{}, reservations: map[string]domain.Reservation{}}
	for k, v := range s.items {
		t.items[k] = v
	}
	for k, v := range s.reservations {
		t.reservations[k] = v
	}
	adj := append([]domain.Adjustment(nil), s.adjustments...)
	t.adjustments = &adj
	mtx := s.Messages.Begin()
	if err := fn(ctx, ports.Tx{Items: t, Reservations: resRepo{t}, Adjustments: adjRepo{t}, Outbox: mtx, Inbox: mtx}); err != nil {
		return err
	}
	s.items, s.reservations, s.adjustments = t.items, t.reservations, adj
	mtx.Commit()
	return nil
}

// Package memory is an in-memory adapter for the fulfillment ports.
package memory

import (
	"context"
	"slices"
	"sort"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/memstore"
)

type Store struct {
	mu       sync.Mutex
	m        map[string]domain.Shipment
	Messages *memstore.Messages
}

func New() *Store { return &Store{m: map[string]domain.Shipment{}, Messages: memstore.NewMessages()} }

type repo struct{ m map[string]domain.Shipment }

func cp(s domain.Shipment) *domain.Shipment {
	s.History = append([]domain.Event(nil), s.History...)
	return &s
}
func (r repo) Create(_ context.Context, s *domain.Shipment) error { r.m[s.ID] = *cp(*s); return nil }
func (r repo) Update(_ context.Context, s *domain.Shipment) error { r.m[s.ID] = *cp(*s); return nil }
func (r repo) Get(_ context.Context, id string) (*domain.Shipment, error) {
	if s, ok := r.m[id]; ok {
		return cp(s), nil
	}
	return nil, ports.ErrNotFound
}
func (r repo) GetByOrder(_ context.Context, o string) (*domain.Shipment, error) {
	for _, s := range r.m {
		if s.OrderID == o {
			return cp(s), nil
		}
	}
	return nil, ports.ErrNotFound
}
func (r repo) List(_ context.Context, st []domain.Status, limit int) ([]*domain.Shipment, error) {
	var out []*domain.Shipment
	for _, s := range r.m {
		if len(st) == 0 || slices.Contains(st, s.Status) {
			out = append(out, cp(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := make(map[string]domain.Shipment, len(s.m))
	for k, v := range s.m {
		staged[k] = v
	}
	mtx := s.Messages.Begin()
	if err := fn(ctx, ports.Tx{Shipments: repo{staged}, Outbox: mtx, Inbox: mtx}); err != nil {
		return err
	}
	s.m = staged
	mtx.Commit()
	return nil
}

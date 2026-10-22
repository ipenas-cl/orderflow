// Package memory is an in-memory adapter for the payments ports.
package memory

import (
	"context"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/memstore"
)

type Store struct {
	mu       sync.Mutex
	byID     map[string]domain.Payment
	Messages *memstore.Messages
}

func New() *Store { return &Store{byID: map[string]domain.Payment{}, Messages: memstore.NewMessages()} }

type repo struct{ m map[string]domain.Payment }

func (r repo) find(f func(domain.Payment) bool) (*domain.Payment, error) {
	for _, p := range r.m {
		if f(p) {
			return &p, nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r repo) Create(_ context.Context, p *domain.Payment) error {
	if _, err := r.GetByKey(context.Background(), p.IdempotencyKey); err == nil {
		return ports.ErrDuplicate
	}
	r.m[p.ID] = *p
	return nil
}
func (r repo) Update(_ context.Context, p *domain.Payment) error { r.m[p.ID] = *p; return nil }
func (r repo) GetByID(_ context.Context, id string) (*domain.Payment, error) {
	return r.find(func(p domain.Payment) bool { return p.ID == id })
}
func (r repo) GetByKey(_ context.Context, k string) (*domain.Payment, error) {
	return r.find(func(p domain.Payment) bool { return p.IdempotencyKey == k })
}
func (r repo) GetByProviderRef(_ context.Context, ref string) (*domain.Payment, error) {
	return r.find(func(p domain.Payment) bool { return p.ProviderRef == ref && ref != "" })
}
func (r repo) ListByOrder(_ context.Context, o string) ([]*domain.Payment, error) {
	var out []*domain.Payment
	for _, p := range r.m {
		if p.OrderID == o {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (s *Store) Do(ctx context.Context, fn func(ctx context.Context, tx ports.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	staged := make(map[string]domain.Payment, len(s.byID))
	for k, v := range s.byID {
		staged[k] = v
	}
	mtx := s.Messages.Begin()
	if err := fn(ctx, ports.Tx{Payments: repo{staged}, Outbox: mtx, Inbox: mtx}); err != nil {
		return err
	}
	s.byID = staged
	mtx.Commit()
	return nil
}

func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

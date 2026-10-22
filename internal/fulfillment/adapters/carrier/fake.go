// Package carrier holds carrier adapters. Fake simulates a carrier API with
// configurable transient failures; customers whose id contains
// "undeliverable" are rejected permanently (used by the chaos demo).
package carrier

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
)

type Fake struct {
	FailureRate float64
	mu          sync.Mutex
	polls       map[string]int
}

func NewFake(failureRate float64) *Fake {
	return &Fake{FailureRate: failureRate, polls: map[string]int{}}
}

func (f *Fake) Book(_ context.Context, s *domain.Shipment) (ports.Label, error) {
	if strings.Contains(s.CustomerID, "undeliverable") {
		return ports.Label{}, fmt.Errorf("%w: address not serviceable", ports.ErrUndeliverable)
	}
	if rand.Float64() < f.FailureRate {
		return ports.Label{}, errors.New("carrier api timeout")
	}
	return ports.Label{Carrier: "FAKEX", TrackingNumber: fmt.Sprintf("FX%010d", rand.IntN(1e9))}, nil
}

// Track reports IN_TRANSIT on the first poll and DELIVERED on the second.
func (f *Fake) Track(_ context.Context, tracking string) (domain.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls[tracking]++
	if f.polls[tracking] == 1 {
		return domain.InTransit, nil
	}
	return domain.Delivered, nil
}

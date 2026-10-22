package domain

import (
	"errors"
	"testing"
	"time"
)

func TestLifecycle(t *testing.T) {
	now := time.Now()
	s := New("s1", "o1", "c1", nil, now)
	_ = s.Advance("w", now)
	if err := s.Ship("X", "T", "w", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("shipped before packing")
	}
	_ = s.Advance("w", now)
	if err := s.Ship("FAKE", "TRK", "w", now); err != nil {
		t.Fatal(err)
	}
	_ = s.Advance("carrier", now)
	_ = s.Advance("carrier", now)
	if s.Status != Delivered || len(s.History) != 6 {
		t.Fatal(s.Status, len(s.History))
	}
	if s.Advance("x", now) == nil || s.Fail("x", "x", now) == nil {
		t.Fatal("terminal state changed")
	}
}

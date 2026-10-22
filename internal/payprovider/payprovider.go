// Package payprovider is a payment-service-provider simulator used for local
// runs and chaos experiments. Its behaviour is changed at runtime through
// PUT /admin/chaos.
package payprovider

import (
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Chaos struct {
	FailureRate float64 `json:"failure_rate"` // share of 503s
	DeclineRate float64 `json:"decline_rate"` // share of 402 declines
	LatencyMS   int     `json:"latency_ms"`   // base latency
	JitterMS    int     `json:"jitter_ms"`    // extra random latency
	Outage      bool    `json:"outage"`       // every call fails
}

type Server struct {
	mu    sync.Mutex
	chaos Chaos
	seen  map[string]response // idempotency cache
	Rand  func() float64
}

type response struct {
	code int
	body map[string]any
}

func New(c Chaos) *Server { return &Server{chaos: c, seen: map[string]response{}, Rand: rand.Float64} }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/charges", s.op("ch"))
	mux.HandleFunc("POST /v1/refunds", s.op("re"))
	mux.HandleFunc("GET /admin/chaos", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		writeJSON(w, 200, s.chaos)
	})
	mux.HandleFunc("PUT /admin/chaos", func(w http.ResponseWriter, r *http.Request) {
		var c Chaos
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		s.mu.Lock()
		s.chaos = c
		s.mu.Unlock()
		writeJSON(w, 200, c)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	return mux
}

func (s *Server) op(prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		s.mu.Lock()
		c := s.chaos
		prev, replay := s.seen[prefix+key]
		s.mu.Unlock()
		delay := time.Duration(c.LatencyMS) * time.Millisecond
		if c.JitterMS > 0 {
			delay += time.Duration(s.Rand()*float64(c.JitterMS)) * time.Millisecond
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		if replay && key != "" {
			w.Header().Set("Idempotent-Replayed", "true")
			writeJSON(w, prev.code, prev.body)
			return
		}
		if c.Outage || s.Rand() < c.FailureRate {
			writeJSON(w, 503, map[string]any{"error": "simulated provider failure"})
			return // transient failures are not cached
		}
		res := response{code: 200, body: map[string]any{"id": prefix + "_" + uuid.NewString()[:8], "status": "succeeded"}}
		if prefix == "ch" && s.Rand() < c.DeclineRate {
			res = response{code: 402, body: map[string]any{"error": "card_declined"}}
		}
		if key != "" {
			s.mu.Lock()
			s.seen[prefix+key] = res
			s.mu.Unlock()
		}
		writeJSON(w, res.code, res.body)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

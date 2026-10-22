package resilience

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-client token bucket (keyed by X-Forwarded-For or the
// remote IP).
type RateLimiter struct {
	mu      sync.Mutex
	rps     rate.Limit
	burst   int
	clients map[string]*client
	OnLimit func()
}

type client struct {
	l    *rate.Limiter
	seen time.Time
}

func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{rps: rate.Limit(rps), burst: burst, clients: map[string]*client{}}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	c, ok := rl.clients[key]
	if !ok {
		if len(rl.clients) > 10000 { // crude eviction to bound memory
			for k, v := range rl.clients {
				if time.Since(v.seen) > time.Minute {
					delete(rl.clients, k)
				}
			}
		}
		c = &client{l: rate.NewLimiter(rl.rps, rl.burst)}
		rl.clients[key] = c
	}
	c.seen = time.Now()
	return c.l.Allow()
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-Forwarded-For")
		if key == "" {
			key, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		if !rl.Allow(key) {
			if rl.OnLimit != nil {
				rl.OnLimit()
			}
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

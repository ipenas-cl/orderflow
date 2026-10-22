// Package httpx provides the HTTP server plumbing: instrumentation
// middleware, health/readiness probes, JSON helpers and graceful shutdown.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ipenas-cl/orderflow/internal/platform/observability"
)

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// Instrument adds tracing, RED metrics, access logs and panic recovery.
func Instrument(service string, log *slog.Logger, next http.Handler) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		defer func() {
			if p := recover(); p != nil {
				log.ErrorContext(r.Context(), "panic", "panic", p)
				sw.WriteHeader(http.StatusInternalServerError)
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			observability.HTTPRequests.WithLabelValues(service, r.Method, route, strconv.Itoa(sw.code)).Inc()
			observability.HTTPDuration.WithLabelValues(service, r.Method, route).Observe(time.Since(start).Seconds())
			if route != "GET /metrics" && route != "GET /healthz" && route != "GET /readyz" {
				log.InfoContext(r.Context(), "http", "method", r.Method, "route", route, "code", sw.code, "dur_ms", time.Since(start).Milliseconds())
			}
		}()
		next.ServeHTTP(sw, r)
	})
	return otelhttp.NewHandler(inner, service, otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
		return r.Method + " " + r.URL.Path
	}))
}

// Probes implements liveness (/healthz) and readiness (/readyz).
type Probes struct {
	ready  atomic.Bool
	checks map[string]func(context.Context) error
}

func NewProbes(checks map[string]func(context.Context) error) *Probes {
	p := &Probes{checks: checks}
	p.ready.Store(true)
	return p
}

func (p *Probes) SetReady(v bool) { p.ready.Store(v) }

func (p *Probes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !p.ready.Load() {
			JSON(w, 503, map[string]string{"status": "shutting down"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		res := map[string]string{}
		code := 200
		for name, c := range p.checks {
			if err := c(ctx); err != nil {
				res[name] = err.Error()
				code = 503
			} else {
				res[name] = "ok"
			}
		}
		JSON(w, code, res)
	})
}

// Serve runs srv until ctx is done, then flips readiness, waits for the load
// balancer to notice, and drains in-flight requests.
func Serve(ctx context.Context, srv *http.Server, probes *Probes, drainDelay time.Duration, log *slog.Logger) error {
	errc := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutdown: marking not ready")
	if probes != nil {
		probes.SetReady(false)
	}
	time.Sleep(drainDelay)
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(sctx)
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]string{"error": msg})
}

func Decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

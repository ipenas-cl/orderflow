// Package bootstrap is the composition root shared by the service binaries:
// config, logging, tracing, Postgres, NATS, the outbox relay, consumers,
// HTTP and graceful shutdown.
package bootstrap

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
	"github.com/ipenas-cl/orderflow/internal/platform/messaging"
	"github.com/ipenas-cl/orderflow/internal/platform/observability"
	"github.com/ipenas-cl/orderflow/internal/platform/outbox"
	"github.com/ipenas-cl/orderflow/internal/platform/pg"
	"github.com/ipenas-cl/orderflow/internal/platform/resilience"
)

type Deps struct {
	Ctx  context.Context
	Log  *slog.Logger
	Pool *pgxpool.Pool
	Bus  *messaging.Bus
}

type Subscription struct {
	Durable  string
	Subjects []string
	Handler  messaging.Handler
}

type Component struct {
	Schema        string
	Routes        func(mux *http.ServeMux)
	Subscriptions []Subscription
	Background    []func(ctx context.Context)
}

func Env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func EnvFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

// Main runs a service until SIGTERM/SIGINT.
func Main(service string, build func(d Deps) (Component, error)) {
	log := observability.NewLogger(service)
	if err := run(service, log, build); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(service string, log *slog.Logger, build func(d Deps) (Component, error)) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.InitTracing(ctx, service)
	if err != nil {
		return err
	}
	defer shutdownTracing(context.Background()) //nolint:errcheck

	pool, err := pg.Connect(ctx, Env("DATABASE_URL", "postgres://orderflow:orderflow@localhost:5432/"+service+"?sslmode=disable"))
	if err != nil {
		return err
	}
	defer pool.Close()

	bus, err := messaging.Connect(ctx, Env("NATS_URL", "nats://localhost:4222"), service, log)
	if err != nil {
		return err
	}

	comp, err := build(Deps{Ctx: ctx, Log: log, Pool: pool, Bus: bus})
	if err != nil {
		return err
	}
	if err := pg.Migrate(ctx, pool, pg.PlatformSchema, comp.Schema); err != nil {
		return err
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	relay := &outbox.Relay{Store: pg.OutboxStore{Pool: pool}, Pub: bus, Service: service, Log: log}
	go func() { relay.Run(bgCtx); close(done) }()
	for _, b := range comp.Background {
		go b(bgCtx)
	}
	for _, s := range comp.Subscriptions {
		if err := bus.Subscribe(ctx, s.Durable, s.Subjects, s.Handler); err != nil {
			bgCancel()
			return err
		}
	}

	mux := http.NewServeMux()
	probes := httpx.NewProbes(map[string]func(context.Context) error{"postgres": pool.Ping, "nats": bus.Healthy})
	probes.Register(mux)
	mux.Handle("GET /metrics", promhttp.Handler())
	comp.Routes(mux)

	rl := resilience.NewRateLimiter(EnvFloat("RATE_LIMIT_RPS", 200), int(EnvFloat("RATE_LIMIT_BURST", 400)))
	rl.OnLimit = func() { observability.RateLimited.WithLabelValues(service).Inc() }
	srv := &http.Server{
		Addr:              Env("HTTP_ADDR", ":8080"),
		Handler:           httpx.Instrument(service, log, rl.Middleware(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	drain, _ := time.ParseDuration(Env("SHUTDOWN_DRAIN_DELAY", "2s"))
	err = httpx.Serve(ctx, srv, probes, drain, log)

	log.Info("shutdown: draining consumers and outbox relay")
	bus.Close()
	bgCancel()
	<-done
	log.Info("shutdown complete")
	return err
}

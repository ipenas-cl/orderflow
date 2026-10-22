// Command payprovider is the PSP simulator (latency, 5xx and declines on demand).
package main

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ipenas-cl/orderflow/internal/payprovider"
)

func env(k string, d float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return d
}

func main() {
	s := payprovider.New(payprovider.Chaos{
		FailureRate: env("FAILURE_RATE", 0.1), DeclineRate: env("DECLINE_RATE", 0.05),
		LatencyMS: int(env("LATENCY_MS", 40)), JitterMS: int(env("JITTER_MS", 60)),
	})
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	slog.Info("payprovider listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

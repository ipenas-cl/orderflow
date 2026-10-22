// Command devissuer is a local OIDC-shaped token issuer for development. It
// stands in for Auth0 so the stack runs offline. Never deploy it.
package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/ipenas-cl/orderflow/internal/platform/auth"
)

func main() {
	issuer := os.Getenv("ISSUER")
	if issuer == "" {
		issuer = "http://localhost:9000/"
	}
	aud := os.Getenv("AUDIENCE")
	if aud == "" {
		aud = "orderflow-admin"
	}
	d, err := auth.NewDevIssuer(issuer, aud)
	if err != nil {
		slog.Error("init", "err", err)
		os.Exit(1)
	}
	slog.Info("dev issuer listening", "issuer", issuer)
	srv := &http.Server{Addr: ":9000", Handler: d.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		os.Exit(1)
	}
}

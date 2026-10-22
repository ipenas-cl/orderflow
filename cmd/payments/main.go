// Command payments runs the payments service. PAYMENT_PROVIDER selects the
// adapter: "fake" (default, the chaos-capable simulator) or "stripe".
package main

import (
	"errors"
	"net/http"
	"os"
	"time"

	payhttp "github.com/ipenas-cl/orderflow/internal/payments/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/provider"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/stripe"
	"github.com/ipenas-cl/orderflow/internal/payments/app"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/bootstrap"
)

func main() {
	bootstrap.Main("payments", func(d bootstrap.Deps) (bootstrap.Component, error) {
		var inner ports.PaymentProvider
		switch bootstrap.Env("PAYMENT_PROVIDER", "fake") {
		case "stripe":
			key := os.Getenv("STRIPE_SECRET_KEY")
			if key == "" {
				return bootstrap.Component{}, errors.New("STRIPE_SECRET_KEY is required when PAYMENT_PROVIDER=stripe")
			}
			inner = stripe.New(key)
		default:
			inner = provider.NewFakeClient(bootstrap.Env("PROVIDER_URL", "http://localhost:8090"))
		}
		timeout, _ := time.ParseDuration(bootstrap.Env("PROVIDER_ATTEMPT_TIMEOUT", "800ms"))
		p := provider.NewResilient(inner, provider.Options{
			MaxAttempts:      int(bootstrap.EnvFloat("PROVIDER_MAX_ATTEMPTS", 4)),
			AttemptTimeout:   timeout,
			MaxConcurrent:    int(bootstrap.EnvFloat("PROVIDER_MAX_CONCURRENT", 32)),
			FailureThreshold: int(bootstrap.EnvFloat("BREAKER_FAILURE_THRESHOLD", 5)),
			OpenTimeout:      10 * time.Second,
		}, d.Log)
		svc := app.NewService(postgres.UnitOfWork{Pool: d.Pool}, p, d.Log)
		return bootstrap.Component{
			Schema: postgres.Schema,
			Routes: func(mux *http.ServeMux) {
				payhttp.Register(mux, svc, payhttp.Options{StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"), Log: d.Log})
			},
			Subscriptions: []bootstrap.Subscription{{Durable: app.Consumer, Subjects: app.Subjects, Handler: svc.HandleMessage}},
		}, nil
	})
}

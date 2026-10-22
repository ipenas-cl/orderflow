// Command fulfillment runs the fulfillment service (warehouse + carrier).
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/carrier"
	fulhttp "github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/app"
	"github.com/ipenas-cl/orderflow/internal/platform/bootstrap"
)

func main() {
	bootstrap.Main("fulfillment", func(d bootstrap.Deps) (bootstrap.Component, error) {
		svc := app.NewService(postgres.UnitOfWork{Pool: d.Pool}, carrier.NewFake(bootstrap.EnvFloat("CARRIER_FAILURE_RATE", 0.1)), d.Log)
		c := bootstrap.Component{
			Schema:        postgres.Schema,
			Routes:        func(mux *http.ServeMux) { fulhttp.Register(mux, svc) },
			Subscriptions: []bootstrap.Subscription{{Durable: app.Consumer, Subjects: app.Subjects, Handler: svc.HandleMessage}},
		}
		if every, err := time.ParseDuration(bootstrap.Env("FULFILLMENT_AUTOPILOT", "")); err == nil && every > 0 {
			c.Background = append(c.Background, func(ctx context.Context) { svc.Autopilot(ctx, every) })
		}
		return c, nil
	})
}

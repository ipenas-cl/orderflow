// Command orders runs the orders service and the saga orchestrator.
package main

import (
	"net/http"

	ordhttp "github.com/ipenas-cl/orderflow/internal/orders/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/orders/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/orders/app"
	"github.com/ipenas-cl/orderflow/internal/platform/bootstrap"
)

func main() {
	bootstrap.Main("orders", func(d bootstrap.Deps) (bootstrap.Component, error) {
		svc := app.NewService(postgres.UnitOfWork{Pool: d.Pool}, d.Log)
		return bootstrap.Component{
			Schema:        postgres.Schema,
			Routes:        func(mux *http.ServeMux) { ordhttp.Register(mux, svc) },
			Subscriptions: []bootstrap.Subscription{{Durable: app.SagaConsumer, Subjects: app.SagaSubjects, Handler: svc.HandleMessage}},
		}, nil
	})
}

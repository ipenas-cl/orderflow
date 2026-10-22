// Command inventory runs the inventory service.
package main

import (
	"net/http"

	invhttp "github.com/ipenas-cl/orderflow/internal/inventory/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/inventory/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/inventory/app"
	"github.com/ipenas-cl/orderflow/internal/platform/bootstrap"
)

func main() {
	bootstrap.Main("inventory", func(d bootstrap.Deps) (bootstrap.Component, error) {
		svc := app.NewService(postgres.UnitOfWork{Pool: d.Pool}, d.Log)
		return bootstrap.Component{
			Schema:        postgres.Schema,
			Routes:        func(mux *http.ServeMux) { invhttp.Register(mux, svc) },
			Subscriptions: []bootstrap.Subscription{{Durable: app.Consumer, Subjects: app.Subjects, Handler: svc.HandleMessage}},
		}, nil
	})
}

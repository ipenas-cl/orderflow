// Command admin runs the backoffice BFF and serves the SPA.
package main

import (
	"net/http"

	"github.com/ipenas-cl/orderflow/internal/admin/adapters/clients"
	adminhttp "github.com/ipenas-cl/orderflow/internal/admin/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/admin/adapters/postgres"
	"github.com/ipenas-cl/orderflow/internal/admin/app"
	"github.com/ipenas-cl/orderflow/internal/platform/auth"
	"github.com/ipenas-cl/orderflow/internal/platform/bootstrap"
)

func main() {
	bootstrap.Main("admin", func(d bootstrap.Deps) (bootstrap.Component, error) {
		e := bootstrap.Env
		backend := clients.New(e("ORDERS_URL", "http://localhost:8080"), e("PAYMENTS_URL", "http://localhost:8081"),
			e("INVENTORY_URL", "http://localhost:8082"), e("FULFILLMENT_URL", "http://localhost:8083"))
		svc := app.NewService(backend, postgres.Audit{Pool: d.Pool})
		issuer := e("OIDC_ISSUER", "http://localhost:9000/")
		verifier := auth.NewJWTVerifier(issuer, e("OIDC_AUDIENCE", "orderflow-admin"), e("OIDC_JWKS_URL", ""), e("OIDC_ROLES_CLAIM", ""))
		cfg := adminhttp.Config{
			Verifier: verifier,
			WebDir:   e("WEB_DIR", ""),
			PublicConfig: map[string]string{
				"authMode":      e("AUTH_MODE", "dev"), // dev | auth0
				"devIssuer":     e("DEV_ISSUER_PUBLIC_URL", "http://localhost:9000"),
				"auth0Domain":   e("AUTH0_DOMAIN", ""),
				"auth0ClientId": e("AUTH0_CLIENT_ID", ""),
				"auth0Audience": e("OIDC_AUDIENCE", "orderflow-admin"),
			},
		}
		return bootstrap.Component{
			Schema: postgres.Schema,
			Routes: func(mux *http.ServeMux) { adminhttp.Register(mux, svc, cfg) },
		}, nil
	})
}

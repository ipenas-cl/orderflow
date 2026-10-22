// Package http is the driving adapter of the BFF: a JSON API for the
// backoffice SPA (bearer JWT required) plus static hosting of the SPA.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ipenas-cl/orderflow/internal/admin/adapters/clients"
	"github.com/ipenas-cl/orderflow/internal/admin/app"
	"github.com/ipenas-cl/orderflow/internal/platform/auth"
	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
)

type Config struct {
	Verifier auth.Verifier
	WebDir   string
	// Public config exposed to the SPA (auth mode, Auth0 domain/client id).
	PublicConfig map[string]string
}

func Register(mux *http.ServeMux, svc *app.Service, cfg Config) {
	api := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeErr(w, err)
			return
		}
		if raw, ok := v.(json.RawMessage); ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw) // #nosec G705 -- JSON from trusted upstream, served as application/json
			return
		}
		httpx.JSON(w, 200, v)
	}
	api.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) { v, err := svc.Me(r.Context()); reply(w, v, err) })
	api.HandleFunc("GET /api/orders", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.ListOrders(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("q"))
		reply(w, v, err)
	})
	api.HandleFunc("GET /api/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.OrderDetail(r.Context(), r.PathValue("id"))
		reply(w, v, err)
	})
	api.HandleFunc("POST /api/orders/{id}/refund", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		v, err := svc.Refund(r.Context(), r.PathValue("id"), b.Reason)
		reply(w, v, err)
	})
	api.HandleFunc("GET /api/inventory", func(w http.ResponseWriter, r *http.Request) { v, err := svc.Stock(r.Context()); reply(w, v, err) })
	api.HandleFunc("POST /api/inventory/adjustments", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			SKU    string `json:"sku"`
			Delta  int    `json:"delta"`
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		v, err := svc.AdjustStock(r.Context(), b.SKU, b.Delta, b.Reason)
		reply(w, v, err)
	})
	api.HandleFunc("GET /api/shipments", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.Shipments(r.Context(), r.URL.Query().Get("status"))
		reply(w, v, err)
	})
	api.HandleFunc("POST /api/shipments/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.AdvanceShipment(r.Context(), r.PathValue("id"))
		reply(w, v, err)
	})
	api.HandleFunc("GET /api/audit", func(w http.ResponseWriter, r *http.Request) { v, err := svc.Audit(r.Context()); reply(w, v, err) })

	mux.Handle("/api/", auth.Authenticate(cfg.Verifier, api))
	mux.HandleFunc("GET /config.json", func(w http.ResponseWriter, _ *http.Request) { httpx.JSON(w, 200, cfg.PublicConfig) })
	if cfg.WebDir != "" {
		fs := http.FileServer(http.Dir(cfg.WebDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				httpx.Error(w, 405, "method not allowed")
				return
			}
			// #nosec G703 -- path is cleaned and rooted in WebDir; http.Dir also rejects ".."
			if _, err := os.Stat(filepath.Join(cfg.WebDir, filepath.Clean("/"+r.URL.Path))); err != nil {
				http.ServeFile(w, r, filepath.Join(cfg.WebDir, "index.html")) // SPA fallback
				return
			}
			fs.ServeHTTP(w, r)
		})
	}
}

func writeErr(w http.ResponseWriter, err error) {
	var up *clients.UpstreamError
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		httpx.Error(w, 401, err.Error())
	case errors.Is(err, auth.ErrForbidden):
		httpx.Error(w, 403, err.Error())
	case errors.Is(err, app.ErrNotRefundable):
		httpx.Error(w, 409, err.Error())
	case errors.Is(err, clients.ErrNotFound):
		httpx.Error(w, 404, "not found")
	case errors.As(err, &up):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(up.Code)
		_, _ = w.Write([]byte(up.Body))
	default:
		httpx.Error(w, 502, err.Error())
	}
}

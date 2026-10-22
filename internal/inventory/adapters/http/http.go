// Package http is the driving HTTP adapter for inventory.
package http

import (
	"errors"
	"net/http"

	"github.com/ipenas-cl/orderflow/internal/inventory/app"
	"github.com/ipenas-cl/orderflow/internal/inventory/domain"
	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
)

func Register(mux *http.ServeMux, svc *app.Service) {
	mux.HandleFunc("GET /v1/stock", func(w http.ResponseWriter, r *http.Request) {
		items, err := svc.List(r.Context())
		if err != nil {
			httpx.Error(w, 500, err.Error())
			return
		}
		if items == nil {
			items = []*domain.Item{}
		}
		httpx.JSON(w, 200, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /v1/stock/adjustments", func(w http.ResponseWriter, r *http.Request) {
		var cmd app.AdjustStock
		if err := httpx.Decode(r, &cmd); err != nil {
			httpx.Error(w, 400, err.Error())
			return
		}
		it, err := svc.Adjust(r.Context(), cmd)
		if errors.Is(err, domain.ErrInvalidQuantity) {
			httpx.Error(w, 422, err.Error())
			return
		}
		if err != nil {
			httpx.Error(w, 500, err.Error())
			return
		}
		httpx.JSON(w, 200, it)
	})
	mux.HandleFunc("GET /v1/stock/adjustments", func(w http.ResponseWriter, r *http.Request) {
		out, err := svc.Adjustments(r.Context(), 100)
		if err != nil {
			httpx.Error(w, 500, err.Error())
			return
		}
		httpx.JSON(w, 200, map[string]any{"adjustments": out})
	})
}

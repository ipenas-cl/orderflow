// Package http is the driving HTTP adapter for fulfillment.
package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ipenas-cl/orderflow/internal/fulfillment/app"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/domain"
	"github.com/ipenas-cl/orderflow/internal/fulfillment/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
)

func Register(mux *http.ServeMux, svc *app.Service) {
	mux.HandleFunc("GET /v1/shipments", func(w http.ResponseWriter, r *http.Request) {
		var st []domain.Status
		if q := r.URL.Query().Get("status"); q != "" {
			for _, s := range strings.Split(q, ",") {
				st = append(st, domain.Status(s))
			}
		}
		if o := r.URL.Query().Get("order_id"); o != "" {
			sh, err := svc.GetByOrder(r.Context(), o)
			if err != nil {
				writeErr(w, err)
				return
			}
			httpx.JSON(w, 200, map[string]any{"shipments": []*domain.Shipment{sh}})
			return
		}
		out, err := svc.List(r.Context(), st, 100)
		if err != nil {
			writeErr(w, err)
			return
		}
		if out == nil {
			out = []*domain.Shipment{}
		}
		httpx.JSON(w, 200, map[string]any{"shipments": out})
	})
	mux.HandleFunc("POST /v1/shipments/{id}/advance", func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Actor")
		if actor == "" {
			actor = "api"
		}
		sh, err := svc.Advance(r.Context(), r.PathValue("id"), actor)
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, 200, sh)
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		httpx.Error(w, 404, "shipment not found")
	case errors.Is(err, domain.ErrInvalidTransition):
		httpx.Error(w, 409, err.Error())
	default:
		httpx.Error(w, 502, err.Error())
	}
}

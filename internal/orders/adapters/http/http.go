// Package http is the driving HTTP adapter for the orders use cases.
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ipenas-cl/orderflow/internal/orders/app"
	"github.com/ipenas-cl/orderflow/internal/orders/domain"
	"github.com/ipenas-cl/orderflow/internal/orders/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
)

func Register(mux *http.ServeMux, svc *app.Service) {
	mux.HandleFunc("POST /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		var cmd app.PlaceOrder
		if err := httpx.Decode(r, &cmd); err != nil {
			httpx.Error(w, 400, err.Error())
			return
		}
		o, err := svc.PlaceOrder(r.Context(), cmd)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Location", "/v1/orders/"+o.ID)
		httpx.JSON(w, http.StatusAccepted, o)
	})
	mux.HandleFunc("GET /v1/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, err := svc.GetOrder(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, 200, o)
	})
	mux.HandleFunc("GET /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		os, err := svc.ListOrders(r.Context(), ports.ListFilter{
			Status: domain.Status(r.URL.Query().Get("status")), Query: r.URL.Query().Get("q"), Limit: limit})
		if err != nil {
			writeErr(w, err)
			return
		}
		if os == nil {
			os = []*domain.Order{}
		}
		httpx.JSON(w, 200, map[string]any{"orders": os})
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidOrder):
		httpx.Error(w, 400, err.Error())
	case errors.Is(err, ports.ErrNotFound):
		httpx.Error(w, 404, "order not found")
	case errors.Is(err, ports.ErrConflict):
		httpx.Error(w, 409, err.Error())
	default:
		httpx.Error(w, 500, "internal error")
	}
}

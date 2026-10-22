// Package http is the driving HTTP adapter for payments: the public payments
// API (Idempotency-Key required) and the Stripe webhook endpoint.
package http

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ipenas-cl/orderflow/internal/payments/adapters/stripe"
	"github.com/ipenas-cl/orderflow/internal/payments/app"
	"github.com/ipenas-cl/orderflow/internal/payments/domain"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
	"github.com/ipenas-cl/orderflow/internal/platform/httpx"
)

type Options struct {
	StripeWebhookSecret string
	Log                 *slog.Logger
}

func Register(mux *http.ServeMux, svc *app.Service, o Options) {
	mux.HandleFunc("POST /v1/payments", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || len(key) > 255 {
			httpx.Error(w, 400, "Idempotency-Key header is required")
			return
		}
		var cmd app.Authorize
		if err := httpx.Decode(r, &cmd); err != nil {
			httpx.Error(w, 400, err.Error())
			return
		}
		cmd.IdempotencyKey = key
		p, replayed, err := svc.Authorize(r.Context(), cmd)
		if err != nil {
			writeErr(w, err)
			return
		}
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		code := http.StatusCreated
		if !p.Succeeded() {
			code = http.StatusPaymentRequired
		}
		httpx.JSON(w, code, p)
	})
	mux.HandleFunc("GET /v1/payments/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, err := svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, 200, p)
	})
	mux.HandleFunc("GET /v1/payments", func(w http.ResponseWriter, r *http.Request) {
		ps, err := svc.ListByOrder(r.Context(), r.URL.Query().Get("order_id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if ps == nil {
			ps = []*domain.Payment{}
		}
		httpx.JSON(w, 200, map[string]any{"payments": ps})
	})
	mux.HandleFunc("POST /v1/payments/{id}/refund", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = httpx.Decode(r, &body)
		p, err := svc.Refund(r.Context(), app.Refund{IdempotencyKey: r.Header.Get("Idempotency-Key"), PaymentID: r.PathValue("id"), Reason: body.Reason})
		if err != nil {
			writeErr(w, err)
			return
		}
		httpx.JSON(w, 200, p)
	})
	mux.HandleFunc("POST /v1/webhooks/stripe", func(w http.ResponseWriter, r *http.Request) {
		if o.StripeWebhookSecret == "" {
			httpx.Error(w, 404, "stripe webhooks not configured")
			return
		}
		payload, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.Error(w, 400, "read body")
			return
		}
		if err := stripe.VerifySignature(payload, r.Header.Get("Stripe-Signature"), o.StripeWebhookSecret, 5*time.Minute, time.Now()); err != nil {
			httpx.Error(w, 400, "invalid signature")
			return
		}
		ev, ok, err := stripe.ParseEvent(payload)
		if err != nil {
			httpx.Error(w, 400, "bad event")
			return
		}
		if ok {
			if err := svc.HandleProviderEvent(r.Context(), ev); err != nil {
				o.Log.ErrorContext(r.Context(), "webhook handling failed", "event", ev.ID, "err", err)
				httpx.Error(w, 500, "retry later") // Stripe retries with backoff
				return
			}
		}
		httpx.JSON(w, 200, map[string]bool{"received": true})
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrIdempotencyMismatch):
		httpx.Error(w, 422, err.Error())
	case errors.Is(err, domain.ErrInvalidPayment):
		httpx.Error(w, 400, err.Error())
	case errors.Is(err, domain.ErrInvalidTransition):
		httpx.Error(w, 409, err.Error())
	case errors.Is(err, ports.ErrNotFound):
		httpx.Error(w, 404, "payment not found")
	case errors.Is(err, ports.ErrProviderUnavailable):
		w.Header().Set("Retry-After", "2")
		httpx.Error(w, 503, err.Error())
	default:
		httpx.Error(w, 502, err.Error())
	}
}

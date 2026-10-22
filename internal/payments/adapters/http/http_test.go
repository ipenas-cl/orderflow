package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	phttp "github.com/ipenas-cl/orderflow/internal/payments/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/payments/adapters/stripe"
	"github.com/ipenas-cl/orderflow/internal/payments/app"
	"github.com/ipenas-cl/orderflow/internal/payments/ports"
)

type okProvider struct{}

func (okProvider) Name() string { return "ok" }
func (okProvider) Authorize(context.Context, ports.AuthorizeRequest) (string, error) {
	return "pi_1", nil
}
func (okProvider) Refund(context.Context, ports.RefundRequest) (string, error) { return "re_1", nil }

func server() http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mux := http.NewServeMux()
	phttp.Register(mux, app.NewService(memory.New(), okProvider{}, log), phttp.Options{StripeWebhookSecret: "whsec_x", Log: log})
	return mux
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIdempotencyKeyHeader(t *testing.T) {
	h := server()
	body := `{"order_id":"o1","amount_cents":500,"currency":"USD"}`
	if rec := do(h, "POST", "/v1/payments", body, nil); rec.Code != 400 {
		t.Fatal("missing key accepted", rec.Code)
	}
	first := do(h, "POST", "/v1/payments", body, map[string]string{"Idempotency-Key": "abc"})
	second := do(h, "POST", "/v1/payments", body, map[string]string{"Idempotency-Key": "abc"})
	if first.Code != 201 || second.Code != 201 || second.Header().Get("Idempotent-Replayed") != "true" || first.Body.String() != second.Body.String() {
		t.Fatal(first.Code, second.Code, second.Header())
	}
	if rec := do(h, "POST", "/v1/payments", `{"order_id":"o1","amount_cents":999,"currency":"USD"}`, map[string]string{"Idempotency-Key": "abc"}); rec.Code != 422 {
		t.Fatal("mismatch", rec.Code)
	}
}

func TestStripeWebhookSignature(t *testing.T) {
	h := server()
	payload := `{"id":"evt_1","type":"charge.refunded","data":{"object":{"payment_intent":"pi_unknown"}}}`
	if rec := do(h, "POST", "/v1/webhooks/stripe", payload, map[string]string{"Stripe-Signature": "t=1,v1=00"}); rec.Code != 400 {
		t.Fatal("bad signature accepted", rec.Code)
	}
	sig := stripe.Sign([]byte(payload), "whsec_x", time.Now())
	if rec := do(h, "POST", "/v1/webhooks/stripe", payload, map[string]string{"Stripe-Signature": sig}); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

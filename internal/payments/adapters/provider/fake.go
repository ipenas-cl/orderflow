package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ipenas-cl/orderflow/internal/payments/ports"
)

// FakeClient talks to cmd/payprovider, a PSP simulator that injects latency,
// 5xx errors and declines on demand.
type FakeClient struct {
	BaseURL string
	HTTP    *http.Client
}

func NewFakeClient(base string) *FakeClient {
	return &FakeClient{BaseURL: base, HTTP: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}}
}

func (c *FakeClient) Name() string { return "fakepsp" }

func (c *FakeClient) post(ctx context.Context, path, key string, body any) (string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusPaymentRequired:
		return "", fmt.Errorf("%w: %s", ports.ErrDeclined, out.Error)
	case resp.StatusCode >= 300:
		return "", fmt.Errorf("provider status %d: %s", resp.StatusCode, out.Error)
	}
	return out.ID, nil
}

func (c *FakeClient) Authorize(ctx context.Context, r ports.AuthorizeRequest) (string, error) {
	return c.post(ctx, "/v1/charges", r.IdempotencyKey, map[string]any{"amount": r.AmountCents, "currency": r.Currency, "order_id": r.OrderID})
}

func (c *FakeClient) Refund(ctx context.Context, r ports.RefundRequest) (string, error) {
	return c.post(ctx, "/v1/refunds", r.IdempotencyKey, map[string]any{"charge": r.ProviderRef, "amount": r.AmountCents})
}

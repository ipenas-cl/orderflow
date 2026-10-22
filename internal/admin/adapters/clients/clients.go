// Package clients is the outbound HTTP adapter from the BFF to the services,
// with a per-call timeout and trace propagation.
package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ipenas-cl/orderflow/internal/admin/ports"
)

var ErrNotFound = errors.New("not found")

type UpstreamError struct {
	Code int
	Body string
}

func (e *UpstreamError) Error() string { return fmt.Sprintf("upstream %d: %s", e.Code, e.Body) }

type Backend struct {
	Orders, Payments, Inventory, Fulfillment string
	HTTP                                     *http.Client
}

func New(orders, payments, inventory, fulfillment string) *Backend {
	return &Backend{Orders: orders, Payments: payments, Inventory: inventory, Fulfillment: fulfillment,
		HTTP: &http.Client{Timeout: 10 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}}
}

func (b *Backend) do(ctx context.Context, method, u string, body any, hdr map[string]string) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return nil, &UpstreamError{Code: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

func (b *Backend) ListOrders(ctx context.Context, status, q string) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Orders+"/v1/orders?"+url.Values{"status": {status}, "q": {q}, "limit": {"100"}}.Encode(), nil, nil)
}

func (b *Backend) GetOrder(ctx context.Context, id string) (ports.Order, error) {
	raw, err := b.do(ctx, "GET", b.Orders+"/v1/orders/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return ports.Order{}, err
	}
	var o ports.Order
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	o.Raw = raw
	return o, nil
}

func (b *Backend) PaymentsForOrder(ctx context.Context, id string) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Payments+"/v1/payments?order_id="+url.QueryEscape(id), nil, nil)
}

func (b *Backend) ShipmentForOrder(ctx context.Context, id string) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Fulfillment+"/v1/shipments?order_id="+url.QueryEscape(id), nil, nil)
}

func (b *Backend) Refund(ctx context.Context, paymentID, key, reason string) (json.RawMessage, error) {
	return b.do(ctx, "POST", b.Payments+"/v1/payments/"+url.PathEscape(paymentID)+"/refund", map[string]string{"reason": reason},
		map[string]string{"Idempotency-Key": key})
}

func (b *Backend) Stock(ctx context.Context) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Inventory+"/v1/stock", nil, nil)
}

func (b *Backend) Adjustments(ctx context.Context) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Inventory+"/v1/stock/adjustments", nil, nil)
}

func (b *Backend) AdjustStock(ctx context.Context, sku string, delta int, reason, actor string) (json.RawMessage, error) {
	return b.do(ctx, "POST", b.Inventory+"/v1/stock/adjustments", map[string]any{"sku": sku, "delta": delta, "reason": reason, "actor": actor}, nil)
}

func (b *Backend) Shipments(ctx context.Context, statuses string) (json.RawMessage, error) {
	return b.do(ctx, "GET", b.Fulfillment+"/v1/shipments?status="+url.QueryEscape(statuses), nil, nil)
}

func (b *Backend) AdvanceShipment(ctx context.Context, id, actor string) (json.RawMessage, error) {
	return b.do(ctx, "POST", b.Fulfillment+"/v1/shipments/"+url.PathEscape(id)+"/advance", nil, map[string]string{"X-Actor": actor})
}

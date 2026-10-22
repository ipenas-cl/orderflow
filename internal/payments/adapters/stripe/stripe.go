// Package stripe is the Stripe adapter for the PaymentProvider port. It uses
// the REST API directly (PaymentIntents + Refunds) and verifies webhook
// signatures. Use test-mode keys (sk_test_...) from the environment only.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/ipenas-cl/orderflow/internal/payments/ports"
)

type Client struct {
	SecretKey     string
	BaseURL       string // https://api.stripe.com
	PaymentMethod string // e.g. pm_card_visa in test mode
	HTTP          *http.Client
}

func New(secretKey string) *Client {
	return &Client{SecretKey: secretKey, BaseURL: "https://api.stripe.com", PaymentMethod: "pm_card_visa",
		HTTP: &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}}
}

func (c *Client) Name() string { return "stripe" }

type apiError struct {
	Error struct {
		Type, Code, DeclineCode, Message string
	} `json:"error"`
}

func (c *Client) post(ctx context.Context, path, key string, form url.Values, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Authorization", "Bearer "+c.SecretKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Idempotency-Key", key) // Stripe dedupes retries for 24h
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e apiError
		_ = json.NewDecoder(resp.Body).Decode(&e)
		// card_error / 402 is a decline: a business outcome, not retryable.
		if resp.StatusCode == http.StatusPaymentRequired || e.Error.Type == "card_error" {
			return fmt.Errorf("%w: %s %s", ports.ErrDeclined, e.Error.DeclineCode, e.Error.Message)
		}
		if resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 404 {
			return fmt.Errorf("stripe %d (permanent): %s", resp.StatusCode, e.Error.Message)
		}
		return fmt.Errorf("stripe %d: %s", resp.StatusCode, e.Error.Message)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Authorize creates and confirms a PaymentIntent (automatic capture).
func (c *Client) Authorize(ctx context.Context, r ports.AuthorizeRequest) (string, error) {
	form := url.Values{
		"amount":                             {strconv.FormatInt(r.AmountCents, 10)},
		"currency":                           {strings.ToLower(r.Currency)},
		"confirm":                            {"true"},
		"payment_method":                     {c.PaymentMethod},
		"metadata[order_id]":                 {r.OrderID},
		"automatic_payment_methods[enabled]": {"true"},
		"automatic_payment_methods[allow_redirects]": {"never"},
	}
	var pi struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := c.post(ctx, "/v1/payment_intents", r.IdempotencyKey, form, &pi); err != nil {
		return "", err
	}
	if pi.Status != "succeeded" && pi.Status != "requires_capture" && pi.Status != "processing" {
		return "", fmt.Errorf("%w: payment intent status %s", ports.ErrDeclined, pi.Status)
	}
	return pi.ID, nil
}

func (c *Client) Refund(ctx context.Context, r ports.RefundRequest) (string, error) {
	form := url.Values{"payment_intent": {r.ProviderRef}, "metadata[reason]": {r.Reason}}
	var rf struct {
		ID string `json:"id"`
	}
	if err := c.post(ctx, "/v1/refunds", r.IdempotencyKey, form, &rf); err != nil {
		return "", err
	}
	return rf.ID, nil
}

var ErrBadSignature = errors.New("invalid stripe signature")

// VerifySignature checks the Stripe-Signature header (t=...,v1=...) as an
// HMAC-SHA256 of "t.payload" with the endpoint secret, with a replay window.
func VerifySignature(payload []byte, header, secret string, tolerance time.Duration, now time.Time) error {
	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 || len(sigs) == 0 {
		return ErrBadSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return fmt.Errorf("%w: timestamp outside tolerance", ErrBadSignature)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "."))
	mac.Write(payload)
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return ErrBadSignature
}

// Sign builds a Stripe-Signature header (tests and local webhook replay).
func Sign(payload []byte, secret string, ts time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts.Unix())
	mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

// ParseEvent maps the Stripe events we care about to the port's ProviderEvent.
// ok=false means "acknowledge and ignore".
func ParseEvent(payload []byte) (ports.ProviderEvent, bool, error) {
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID               string `json:"id"`
				PaymentIntent    string `json:"payment_intent"`
				LastPaymentError *struct {
					Message string `json:"message"`
				} `json:"last_payment_error"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return ports.ProviderEvent{}, false, err
	}
	o := ev.Data.Object
	switch ev.Type {
	case "charge.refunded":
		return ports.ProviderEvent{ID: ev.ID, Type: "refunded", ProviderRef: o.PaymentIntent, Reason: "refunded in Stripe"}, true, nil
	case "payment_intent.payment_failed":
		reason := "payment failed"
		if o.LastPaymentError != nil {
			reason = o.LastPaymentError.Message
		}
		return ports.ProviderEvent{ID: ev.ID, Type: "payment_failed", ProviderRef: o.ID, Reason: reason}, true, nil
	}
	return ports.ProviderEvent{ID: ev.ID}, false, nil
}

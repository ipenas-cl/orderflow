package stripe

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ipenas-cl/orderflow/internal/payments/ports"
)

func TestSignatureRoundTripAndTamper(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Now()
	h := Sign(body, "whsec_test", now)
	if err := VerifySignature(body, h, "whsec_test", 5*time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if VerifySignature([]byte(`{"id":"evt_2"}`), h, "whsec_test", 5*time.Minute, now) == nil {
		t.Fatal("tampered body accepted")
	}
	if VerifySignature(body, h, "other", 5*time.Minute, now) == nil {
		t.Fatal("wrong secret accepted")
	}
	if VerifySignature(body, h, "whsec_test", 5*time.Minute, now.Add(10*time.Minute)) == nil {
		t.Fatal("replay accepted")
	}
}

func TestParseEvent(t *testing.T) {
	ev, ok, err := ParseEvent([]byte(`{"id":"evt_1","type":"charge.refunded","data":{"object":{"id":"ch_1","payment_intent":"pi_1"}}}`))
	if err != nil || !ok || ev.ProviderRef != "pi_1" || ev.Type != "refunded" {
		t.Fatal(ev, ok, err)
	}
	if _, ok, _ := ParseEvent([]byte(`{"id":"evt_2","type":"customer.created","data":{"object":{}}}`)); ok {
		t.Fatal("irrelevant event handled")
	}
}

func TestAuthorizeAgainstStubbedStripe(t *testing.T) {
	var gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.Header.Get("Idempotency-Key"), r.Header.Get("Authorization")
		_ = r.ParseForm()
		if r.Form.Get("amount") == "666" {
			w.WriteHeader(402)
			_, _ = w.Write([]byte(`{"error":{"type":"card_error","decline_code":"insufficient_funds","message":"Your card has insufficient funds."}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"pi_123","status":"succeeded"}`))
	}))
	defer srv.Close()
	c := New("sk_test_dummy")
	c.BaseURL = srv.URL
	ref, err := c.Authorize(context.Background(), ports.AuthorizeRequest{IdempotencyKey: "k1", AmountCents: 1000, Currency: "USD"})
	if err != nil || ref != "pi_123" || gotKey != "k1" || gotAuth != "Bearer sk_test_dummy" {
		t.Fatal(ref, err, gotKey, gotAuth)
	}
	if _, err := c.Authorize(context.Background(), ports.AuthorizeRequest{IdempotencyKey: "k2", AmountCents: 666, Currency: "USD"}); !errors.Is(err, ports.ErrDeclined) {
		t.Fatal(err)
	}
}

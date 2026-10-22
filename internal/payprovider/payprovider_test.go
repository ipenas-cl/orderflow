package payprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/charges", strings.NewReader(`{}`))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestChaosAndIdempotency(t *testing.T) {
	s := New(Chaos{Outage: true})
	h := s.Handler()
	if post(h, "a").Code != 503 {
		t.Fatal("outage")
	}
	s.chaos = Chaos{}
	r1, r2 := post(h, "a"), post(h, "a")
	if r1.Code != 200 || r1.Body.String() != r2.Body.String() || r2.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal(r1.Body.String(), r2.Body.String())
	}
	s.chaos = Chaos{DeclineRate: 1}
	if post(h, "b").Code != 402 {
		t.Fatal("decline")
	}
}

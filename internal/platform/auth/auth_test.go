package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func setup(t *testing.T) (*DevIssuer, *JWTVerifier) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	iss, err := NewDevIssuer(srv.URL, "orderflow-admin")
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = iss.Handler()
	return iss, NewJWTVerifier(srv.URL, "orderflow-admin", "", "")
}

func TestVerifyValidToken(t *testing.T) {
	iss, v := setup(t)
	tok, _ := iss.Mint("u1", "a@b.c", []string{RoleSupport}, time.Minute)
	p, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subject != "u1" || !p.Has(RoleSupport) || p.Has(RoleViewer) {
		t.Fatalf("%+v", p)
	}
}

func TestVerifyRejectsWrongAudienceAndExpired(t *testing.T) {
	iss, v := setup(t)
	expired, _ := iss.Mint("u1", "", nil, -time.Hour)
	if _, err := v.Verify(context.Background(), expired); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("expired token accepted")
	}
	v.Audience = "other"
	ok, _ := iss.Mint("u1", "", nil, time.Minute)
	if _, err := v.Verify(context.Background(), ok); err == nil {
		t.Fatal("wrong audience accepted")
	}
	other, _ := NewDevIssuer(iss.Issuer, "orderflow-admin")
	forged, _ := other.Mint("u1", "", []string{RoleAdmin}, time.Minute)
	v.Audience = "orderflow-admin"
	if _, err := v.Verify(context.Background(), forged); err == nil {
		t.Fatal("token signed by another key accepted")
	}
}

func TestRBACMiddleware(t *testing.T) {
	iss, v := setup(t)
	h := Authenticate(v, Require([]string{RoleSupport}, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	cases := map[string]int{"": 401}
	for role, want := range map[string]int{RoleViewer: 403, RoleSupport: 204, RoleAdmin: 204} {
		tok, _ := iss.Mint("u", "", []string{role}, time.Minute)
		cases[tok] = want
	}
	for tok, want := range cases {
		req := httptest.NewRequest("POST", "/", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("want %d got %d", want, rec.Code)
		}
	}
}

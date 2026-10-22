// Package auth defines the authentication port (Verifier), an OIDC/JWKS
// adapter that works with Auth0 or any OIDC issuer, RBAC middleware, and a
// local development issuer so tests and docker-compose need no Auth0 tenant.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	RoleAdmin   = "admin"
	RoleSupport = "support"
	RoleViewer  = "viewer"
)

// DefaultRolesClaim is an Auth0-style namespaced custom claim set by an Action.
const DefaultRolesClaim = "https://orderflow.dev/roles"

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
)

type Principal struct {
	Subject string   `json:"sub"`
	Email   string   `json:"email,omitempty"`
	Roles   []string `json:"roles"`
}

// Has reports whether the principal holds any of roles. admin implies all.
func (p Principal) Has(roles ...string) bool {
	if slices.Contains(p.Roles, RoleAdmin) {
		return true
	}
	for _, r := range roles {
		if slices.Contains(p.Roles, r) {
			return true
		}
	}
	return false
}

// Verifier is the port: turn a bearer token into a Principal.
type Verifier interface {
	Verify(ctx context.Context, token string) (Principal, error)
}

// JWTVerifier validates RS256 JWTs against an issuer's JWKS.
type JWTVerifier struct {
	Issuer     string
	Audience   string
	RolesClaim string
	Keys       *JWKS
}

func NewJWTVerifier(issuer, audience, jwksURL, rolesClaim string) *JWTVerifier {
	if !strings.HasSuffix(issuer, "/") {
		issuer += "/"
	}
	if jwksURL == "" {
		jwksURL = issuer + ".well-known/jwks.json"
	}
	if rolesClaim == "" {
		rolesClaim = DefaultRolesClaim
	}
	return &JWTVerifier{Issuer: issuer, Audience: audience, RolesClaim: rolesClaim, Keys: &JWKS{URL: jwksURL}}
}

func (v *JWTVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.Keys.Key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(v.Issuer), jwt.WithAudience(v.Audience),
		jwt.WithExpirationRequired(), jwt.WithLeeway(30*time.Second))
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	p := Principal{}
	p.Subject, _ = claims["sub"].(string)
	p.Email, _ = claims["email"].(string)
	if rs, ok := claims[v.RolesClaim].([]any); ok {
		for _, r := range rs {
			if s, ok := r.(string); ok {
				p.Roles = append(p.Roles, s)
			}
		}
	}
	return p, nil
}

// JWKS fetches and caches RSA keys, refreshing on an unknown kid (key rotation)
// at most once every 30s.
type JWKS struct {
	URL       string
	Client    *http.Client
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

func (j *JWKS) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if k, ok := j.keys[kid]; ok {
		return k, nil
	}
	if time.Since(j.fetchedAt) < 30*time.Second && j.keys != nil {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	if err := j.refresh(ctx); err != nil {
		return nil, err
	}
	if k, ok := j.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

func (j *JWKS) refresh(ctx context.Context) error {
	c := j.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, j.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []struct{ Kid, Kty, N, E string } `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	j.keys, j.fetchedAt = keys, time.Now()
	return nil
}

type ctxKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

// Authenticate validates the bearer token and stores the Principal in the context.
func Authenticate(v Verifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			writeErr(w, 401, "missing bearer token")
			return
		}
		p, err := v.Verify(r.Context(), raw)
		if err != nil {
			writeErr(w, 401, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// Require gates a handler by role (RBAC).
func Require(roles []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeErr(w, 401, "unauthenticated")
			return
		}
		if !p.Has(roles...) {
			writeErr(w, 403, "forbidden: requires one of "+strings.Join(roles, ","))
			return
		}
		next(w, r)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

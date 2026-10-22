package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DevIssuer is a minimal OIDC-shaped issuer for local development and tests.
// It is never deployed to production: there the verifier points at Auth0.
type DevIssuer struct {
	Issuer   string // must end with "/"
	Audience string
	key      *rsa.PrivateKey
	kid      string
}

// DevUsers are the demo identities offered by the backoffice login screen.
var DevUsers = map[string][]string{
	"ana.admin@orderflow.dev":   {RoleAdmin},
	"sam.support@orderflow.dev": {RoleSupport},
	"vera.viewer@orderflow.dev": {RoleViewer},
}

func NewDevIssuer(issuer, audience string) (*DevIssuer, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(issuer, "/") {
		issuer += "/"
	}
	return &DevIssuer{Issuer: issuer, Audience: audience, key: k, kid: "dev-1"}, nil
}

func (d *DevIssuer) Mint(sub, email string, roles []string, ttl time.Duration) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": d.Issuer, "aud": d.Audience, "sub": sub, "email": email,
		"iat": time.Now().Unix(), "exp": time.Now().Add(ttl).Unix(),
		DefaultRolesClaim: roles,
	})
	t.Header["kid"] = d.kid
	return t.SignedString(d.key)
}

func (d *DevIssuer) JWKS() map[string]any {
	pub := d.key.PublicKey
	return map[string]any{"keys": []map[string]string{{
		"kid": d.kid, "kty": "RSA", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}}
}

// Handler serves the discovery doc, JWKS and a dev-only token endpoint:
// POST /token {"email": "..."} returns a token for one of DevUsers.
func (d *DevIssuer) Handler() http.Handler {
	mux := http.NewServeMux()
	cors := func(w http.ResponseWriter) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Content-Type", "application/json")
	}
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": d.Issuer, "jwks_uri": d.Issuer + ".well-known/jwks.json"})
	})
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(d.JWKS())
	})
	mux.HandleFunc("OPTIONS /token", func(w http.ResponseWriter, _ *http.Request) { cors(w) })
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, _ *http.Request) {
		cors(w)
		_ = json.NewEncoder(w).Encode(DevUsers)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		cors(w)
		var req struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		roles, ok := DevUsers[req.Email]
		if !ok {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unknown dev user"})
			return
		}
		tok, err := d.Mint("dev|"+req.Email, req.Email, slices.Clone(roles), 8*time.Hour)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": 28800})
	})
	return mux
}

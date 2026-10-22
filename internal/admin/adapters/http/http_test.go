package http_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipenas-cl/orderflow/internal/admin/adapters/clients"
	adminhttp "github.com/ipenas-cl/orderflow/internal/admin/adapters/http"
	"github.com/ipenas-cl/orderflow/internal/admin/adapters/memory"
	"github.com/ipenas-cl/orderflow/internal/admin/app"
	"github.com/ipenas-cl/orderflow/internal/platform/auth"
)

func TestRoutesAuthAndSPA(t *testing.T) {
	iss := httptest.NewUnstartedServer(nil)
	iss.Start()
	defer iss.Close()
	dev, _ := auth.NewDevIssuer(iss.URL, "aud")
	iss.Config.Handler = dev.Handler()

	web := t.TempDir()
	_ = os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>spa</html>"), 0o644)
	mux := http.NewServeMux()
	adminhttp.Register(mux, app.NewService(clients.New("", "", "", ""), &memory.Audit{}), adminhttp.Config{
		Verifier: auth.NewJWTVerifier(iss.URL, "aud", "", ""), WebDir: web, PublicConfig: map[string]string{"authMode": "dev"}})

	get := func(path, tok string) int {
		req := httptest.NewRequest("GET", path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get("/api/me", ""); c != 401 {
		t.Fatal("unauthenticated /api/me", c)
	}
	tok, _ := dev.Mint("u", "v@x", []string{auth.RoleViewer}, time.Minute)
	if c := get("/api/me", tok); c != 200 {
		t.Fatal("/api/me", c)
	}
	if c := get("/api/audit", tok); c != 403 {
		t.Fatal("viewer read audit", c)
	}
	if c := get("/orders/123", ""); c != 200 {
		t.Fatal("spa fallback", c)
	}
	if c := get("/config.json", ""); c != 200 {
		t.Fatal("config", c)
	}
}

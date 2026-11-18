package controlplane

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/why-xn/kbridge/internal/auth"
)

const wantCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func TestConsole(t *testing.T) {
	tests := []struct {
		name, method, path, ctype, location string
		status                              int
		cache                               string
	}{
		{"index", "GET", "/ui/", "text/html", "", 200, "no-store"},
		{"index head", "HEAD", "/ui/", "text/html", "", 200, "no-store"},
		{"logo", "GET", "/ui/logo.svg", "image/svg+xml", "", 200, "max-age=300"},
		{"logo head", "HEAD", "/ui/logo.svg", "image/svg+xml", "", 200, "max-age=300"},
		{"missing", "GET", "/ui/missing.js", "", "", 404, ""},
		{"traversal", "GET", "/ui/../go.mod", "", "", 404, ""},
		{"directory", "GET", "/ui/console/", "", "", 404, ""},
		{"root redirect", "GET", "/", "", "/ui/", 302, ""},
	}
	srv, _, _ := newTestHTTPServer()
	srv.registerConsole()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, nil))
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
			if tt.location != "" && w.Header().Get("Location") != tt.location {
				t.Errorf("Location = %q", w.Header().Get("Location"))
			}
			if tt.status != 200 {
				return
			}
			if tt.method == "HEAD" && w.Body.Len() != 0 {
				t.Errorf("HEAD body = %d bytes, want none", w.Body.Len())
			}
			h := w.Header()
			if got := h.Get("Content-Type"); len(got) < len(tt.ctype) || got[:len(tt.ctype)] != tt.ctype {
				t.Errorf("Content-Type = %q, want %q", got, tt.ctype)
			}
			for k, v := range map[string]string{
				"Content-Security-Policy": wantCSP, "X-Content-Type-Options": "nosniff",
				"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY", "Cache-Control": tt.cache,
			} {
				if h.Get(k) != v {
					t.Errorf("%s = %q, want %q", k, h.Get(k), v)
				}
			}
		})
	}
}

func TestServerUIEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint("enabled=", enabled), func(t *testing.T) {
			cfg := testServerConfig()
			cfg.UI.Enabled = enabled
			srv, err := NewServer(cfg)
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			token, _ := auth.NewJWTManager(cfg.Auth.JWTSecret, time.Hour).
				GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "admin@x.com", IsAdmin: true})
			ui := map[bool]int{true: 200, false: 404}[enabled]
			root := map[bool]int{true: 302, false: 404}[enabled]
			for path, want := range map[string]int{"/ui/": ui, "/": root, "/api/v1/admin/clusters": 200} {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				srv.httpServer.Handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Errorf("%s = %d, want %d", path, w.Code, want)
				}
			}
		})
	}
}

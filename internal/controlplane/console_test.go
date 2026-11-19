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
		name, method, path, ctype string
		status                    int
		cache                     string
	}{
		{"index", "GET", "/", "text/html", 200, "no-store"},
		{"index head", "HEAD", "/", "text/html", 200, "no-store"},
		{"js", "GET", "/assets/app.js", "text/javascript", 200, "max-age=300"},
		{"css", "GET", "/assets/app.css", "text/css", 200, "max-age=300"},
		{"logo", "GET", "/assets/logo.svg", "image/svg+xml", 200, "max-age=300"},
		{"logo head", "HEAD", "/assets/logo.svg", "image/svg+xml", 200, "max-age=300"},
		{"logo dark", "GET", "/assets/logo-dark.svg", "image/svg+xml", 200, "max-age=300"},
		{"missing", "GET", "/assets/missing.js", "", 404, ""},
		{"traversal", "GET", "/assets/../go.mod", "", 404, ""},
		{"assets listing", "GET", "/assets/", "", 404, ""},
		{"index.html is not an asset", "GET", "/assets/index.html", "", 404, ""},
		{"old ui root", "GET", "/ui/", "", 404, ""},
		{"old ui", "GET", "/ui", "", 404, ""},
		{"old ui asset", "GET", "/ui/app.js", "", 404, ""},
		{"unknown path", "GET", "/foo", "", 404, ""},
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
			for path, want := range map[string]int{"/": ui, "/assets/app.js": ui, "/ui/": 404, "/api/v1/admin/clusters": 200} {
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

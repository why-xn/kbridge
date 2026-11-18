package controlplane

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const wantCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func TestConsole(t *testing.T) {
	tests := []struct {
		name, path, ctype, location string
		status                      int
		cache                       string
	}{
		{"index", "/ui/", "text/html", "", 200, "no-store"},
		{"logo", "/ui/logo.svg", "image/svg+xml", "", 200, "max-age=300"},
		{"missing", "/ui/missing.js", "", "", 404, ""},
		{"traversal", "/ui/../go.mod", "", "", 404, ""},
		{"directory", "/ui/console/", "", "", 404, ""},
		{"root redirect", "/", "", "/ui/", 302, ""},
	}
	srv, _, _ := newTestHTTPServer()
	srv.registerConsole()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != tt.status {
				t.Fatalf("status = %d, want %d", w.Code, tt.status)
			}
			if tt.location != "" && w.Header().Get("Location") != tt.location {
				t.Errorf("Location = %q", w.Header().Get("Location"))
			}
			if tt.status != 200 {
				return
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

func TestConsoleDisabled(t *testing.T) {
	srv, _, _ := newTestHTTPServer() // registerConsole not called: ui.enabled=false
	for path, want := range map[string]int{"/ui/": 404, "/": 404, "/api/v1/clusters": 200} {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
}

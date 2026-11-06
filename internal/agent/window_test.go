package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// echoed is what the test upstream reports back about a forwarded request.
type echoed struct {
	Path   string      `json:"path"`
	Header http.Header `json:"header"`
}

func newEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(echoed{Path: r.URL.Path, Header: r.Header})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeToken(t *testing.T, path, token string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("writing token: %v", err)
	}
}

// windowFixture starts a window in front of an echo upstream and returns it,
// a client trusting the window's CA file, and the token file path.
func windowFixture(t *testing.T) (*Window, *http.Client, string) {
	t.Helper()
	srv := newEchoUpstream(t)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	writeToken(t, tokenFile, "file-token")
	w, err := NewWindow(Upstream{Server: srv.URL, CA: pool, TokenFile: tokenFile}, []string{"kbridge"}, dir)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, windowClient(t, dir), tokenFile
}

func windowClient(t *testing.T, dir string) *http.Client {
	t.Helper()
	pem, err := os.ReadFile(filepath.Join(dir, "window-ca.crt"))
	if err != nil {
		t.Fatalf("reading window ca: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("window ca file holds no certificate")
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

// serverOf extracts the --server value from the window's kubectl args.
func serverOf(t *testing.T, w *Window, nonce string) string {
	t.Helper()
	for _, a := range w.KubectlArgs(nonce) {
		if v, ok := strings.CutPrefix(a, "--server="); ok {
			return v
		}
	}
	t.Fatal("no --server in kubectl args")
	return ""
}

type windowResp struct {
	status int
	body   string
}

func do(t *testing.T, c *http.Client, method, url, nonce string, extra map[string]string) windowResp {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+nonce)
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return windowResp{status: resp.StatusCode, body: string(b)}
}

func upstreamSaw(t *testing.T, r windowResp) echoed {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("status = %d, body %q", r.status, r.body)
	}
	var e echoed
	if err := json.Unmarshal([]byte(r.body), &e); err != nil {
		t.Fatalf("decoding echo: %v", err)
	}
	return e
}

func TestWindow_StampsIdentityAndDropsClaims(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x", RequestID: "r1"})
	defer release()
	r := do(t, c, "GET", serverOf(t, w, nonce)+"/api/v1/namespaces/dev/pods", nonce, map[string]string{
		"Impersonate-User":  "admin",
		"Impersonate-Group": "system:masters",
	})
	h := upstreamSaw(t, r).Header
	want := map[string]string{
		"Authorization":                        "Bearer file-token",
		"Impersonate-User":                     "kbridge:a@x",
		"Impersonate-Extra-Kbridge-Request-Id": "r1",
		"Impersonate-Group":                    "",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if vs := h.Values("Impersonate-User"); len(vs) != 1 {
		t.Errorf("Impersonate-User values = %v, want exactly one", vs)
	}
}

func TestWindow_ConnectionHeaderCannotStripStamp(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x", RequestID: "r1"})
	defer release()
	h := upstreamSaw(t, do(t, c, "GET", serverOf(t, w, nonce)+"/api/v1/namespaces/dev/pods", nonce, map[string]string{
		"Connection": "Impersonate-User, Authorization, Impersonate-Extra-Kbridge-Request-Id",
	})).Header
	if h.Get("Impersonate-User") != "kbridge:a@x" || h.Get("Authorization") != "Bearer file-token" {
		t.Fatalf("stamp stripped: Impersonate-User %q, Authorization %q", h.Get("Impersonate-User"), h.Get("Authorization"))
	}
}

func TestWindow_UnknownNonce(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	r := do(t, c, "GET", serverOf(t, w, nonce)+"/api/v1/pods", "not-a-nonce", nil)
	if r.status != http.StatusUnauthorized || r.body != "unknown command token" {
		t.Fatalf("got %d %q, want 401 %q", r.status, r.body, "unknown command token")
	}
}

func TestWindow_ReleasedNonce(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	url := serverOf(t, w, nonce) + "/api/v1/namespaces/dev/pods"
	upstreamSaw(t, do(t, c, "GET", url, nonce, nil))
	release()
	release() // idempotent
	r := do(t, c, "GET", url, nonce, nil)
	if r.status != http.StatusUnauthorized || r.body != "unknown command token" {
		t.Fatalf("got %d %q, want 401 %q", r.status, r.body, "unknown command token")
	}
}

func TestWindow_ConcurrentNonces(t *testing.T) {
	w, c, _ := windowFixture(t)
	users := []string{"kbridge:a@x", "kbridge:b@x"}
	nonces := make([]string, len(users))
	for i, u := range users {
		n, release := w.Register(Identity{User: u, RequestID: u})
		defer release()
		nonces[i] = n
	}
	got := make([]windowResp, len(users))
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = do(t, c, "GET", serverOf(t, w, nonces[i])+"/api/v1/namespaces/dev/pods", nonces[i], nil)
		}()
	}
	wg.Wait()
	for i, u := range users {
		if h := upstreamSaw(t, got[i]).Header; h.Get("Impersonate-User") != u {
			t.Errorf("nonce %d arrived as %q, want %q", i, h.Get("Impersonate-User"), u)
		}
	}
}

func TestProtectedPath(t *testing.T) {
	tests := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/namespaces/kbridge/pods", true},
		{"GET", "/apis/apps/v1/namespaces/kbridge/deployments/x", true},
		{"DELETE", "/api/v1/namespaces/kbridge", true},
		{"GET", "/api/v1/namespaces/kbridge", false},
		{"GET", "/api/v1/namespaces/kbridgex/pods", false},
		{"GET", "/api/v1/namespaces/dev/pods", false},
		{"POST", "/api/v1/namespaces/kbridge/serviceaccounts/agent/token", true},
		{"GET", "/api/v1/namespaces/kbridge/", true},
		{"GET", "/api/v1/watch/namespaces/kbridge/pods", true},
		{"GET", "/api/v1/watch/namespaces/kbridge/secrets/x", true},
		{"GET", "/apis/apps/v1/watch/namespaces/kbridge/deployments", true},
		{"GET", "/api/v1/namespaces", false},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if got := protectedPath(tt.method, tt.path, []string{"kbridge"}); got != tt.want {
				t.Errorf("protectedPath = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWindow_ProtectedNamespace(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	base := serverOf(t, w, nonce)
	want := `namespace "kbridge" is reserved for the kbridge agent`
	for _, p := range []string{"/api/v1/namespaces/kbridge/secrets", "/api/v1/namespaces/kbr%69dge/secrets"} {
		r := do(t, c, "GET", base+p, nonce, nil)
		if r.status != http.StatusForbidden || r.body != want {
			t.Errorf("%s: got %d %q, want 403 %q", p, r.status, r.body, want)
		}
	}
}

func TestWindow_ClusterWideSecrets(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	base := serverOf(t, w, nonce)
	want := "reading secrets across all namespaces is not allowed through kbridge"
	for _, p := range []string{
		"/api/v1/secrets",
		"/api/v1/secrets?watch=true",
		"/api/v1/watch/secrets",
		"/api/v1/watch/secrets/",
		"/api/v1/watch/secrets/x",
		"/api/v1/watch/s%65crets",
		"/api/v1/watch/dev/../secrets",
		"/api/v1/dev/../secrets",
	} {
		if r := do(t, c, "GET", base+p, nonce, nil); r.status != http.StatusForbidden || r.body != want {
			t.Errorf("%s: got %d %q, want 403 %q", p, r.status, r.body, want)
		}
	}
	if e := upstreamSaw(t, do(t, c, "GET", base+"/api/v1/namespaces/dev/secrets", nonce, nil)); e.Path != "/api/v1/namespaces/dev/secrets" {
		t.Errorf("upstream path = %q", e.Path)
	}
}

func TestWindow_NodeProxy(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	base := serverOf(t, w, nonce)
	want := "node proxy is not allowed through kbridge"
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/nodes/n1/proxy/healthz"},
		{"POST", "/api/v1/nodes/n1/proxy/run/kbridge/p/agent?cmd=cat+/x"},
		{"GET", "/api/v1/nodes/n1/proxy"},
		{"GET", "/api/v1/nodes/n1/pro%78y/stats"},
		{"GET", "/api/v1/nodes/n1/../n1/proxy/healthz"},
	} {
		if r := do(t, c, tc.method, base+tc.path, nonce, nil); r.status != http.StatusForbidden || r.body != want {
			t.Errorf("%s %s: got %d %q, want 403 %q", tc.method, tc.path, r.status, r.body, want)
		}
	}
	for _, p := range []string{"/api/v1/nodes/n1", "/api/v1/nodes/n1/status"} {
		if e := upstreamSaw(t, do(t, c, "GET", base+p, nonce, nil)); e.Path != p {
			t.Errorf("upstream path = %q, want %q", e.Path, p)
		}
	}
}

func TestWindow_ServiceAccountToken(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	base := serverOf(t, w, nonce)
	want := "creating service account tokens is not allowed through kbridge"
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/namespaces/dev/serviceaccounts/default/token"},
		{"POST", "/api/v1/namespaces/dev/serviceaccounts/default/token/"},
		{"POST", "/api/v1/namespaces/dev/serviceaccounts/default/tok%65n"},
		{"POST", "/api/v1/namespaces/dev/serviceaccounts/x/../default/token"},
		{"GET", "/api/v1/namespaces/dev/serviceaccounts/default/token"},
	} {
		if r := do(t, c, tc.method, base+tc.path, nonce, nil); r.status != http.StatusForbidden || r.body != want {
			t.Errorf("%s %s: got %d %q, want 403 %q", tc.method, tc.path, r.status, r.body, want)
		}
	}
	p := "/api/v1/namespaces/dev/serviceaccounts/default"
	if e := upstreamSaw(t, do(t, c, "GET", base+p, nonce, nil)); e.Path != p {
		t.Errorf("upstream path = %q, want %q", e.Path, p)
	}
}

func TestWindow_TokenReReadPerRequest(t *testing.T) {
	w, c, tokenFile := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	url := serverOf(t, w, nonce) + "/api/v1/namespaces/dev/pods"
	for _, tok := range []string{"first", "second"} {
		writeToken(t, tokenFile, tok)
		if got := upstreamSaw(t, do(t, c, "GET", url, nonce, nil)).Header.Get("Authorization"); got != "Bearer "+tok {
			t.Errorf("Authorization = %q, want %q", got, "Bearer "+tok)
		}
	}
}

func TestWindow_UnreadableTokenFile(t *testing.T) {
	w, c, tokenFile := windowFixture(t)
	nonce, release := w.Register(Identity{User: "kbridge:a@x"})
	defer release()
	if err := os.Remove(tokenFile); err != nil {
		t.Fatal(err)
	}
	r := do(t, c, "GET", serverOf(t, w, nonce)+"/api/v1/namespaces/dev/pods", nonce, nil)
	if r.status != http.StatusBadGateway || !strings.HasPrefix(r.body, "reading agent token: ") {
		t.Fatalf("got %d %q, want 502 reading agent token", r.status, r.body)
	}
}

func TestWindow_EmptyUserStampsOnlyToken(t *testing.T) {
	w, c, _ := windowFixture(t)
	nonce, release := w.Register(Identity{})
	defer release()
	h := upstreamSaw(t, do(t, c, "GET", serverOf(t, w, nonce)+"/api/v1/namespaces/dev/pods", nonce, map[string]string{
		"Impersonate-User":        "admin",
		"Impersonate-Extra-Scope": "x",
	})).Header
	for k := range h {
		if strings.HasPrefix(k, "Impersonate-") {
			t.Errorf("unexpected header %s upstream", k)
		}
	}
	if got := h.Get("Authorization"); got != "Bearer file-token" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestWindow_KubectlArgs(t *testing.T) {
	w, _, _ := windowFixture(t)
	args := w.KubectlArgs("abc")
	if len(args) != 3 || !strings.HasPrefix(args[0], "--server=https://127.0.0.1:") ||
		!strings.HasPrefix(args[1], "--certificate-authority=") || !strings.HasSuffix(args[1], "window-ca.crt") ||
		args[2] != "--token=abc" {
		t.Fatalf("KubectlArgs = %v", args)
	}
}

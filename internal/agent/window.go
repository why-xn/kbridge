package agent

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const windowCAFile = "window-ca.crt"

// Identity is what the window stamps on a command's requests. Empty User means
// "stamp only the agent token" (Part A); Part B makes an empty User a refusal.
type Identity struct{ User, RequestID string }

// Upstream is the real cluster connection the window owns.
type Upstream struct {
	Server     string // https://host:port
	CA         *x509.CertPool
	TokenFile  string // re-read per request; empty when ClientCert is set
	ClientCert *tls.Certificate
}

// Window is a TLS reverse proxy on loopback. kubectl talks to it with a
// one-off nonce; the window swaps the nonce for the agent's credentials and
// the command's identity, and refuses requests into protected namespaces.
type Window struct {
	listener  net.Listener
	server    *http.Server
	proxy     *httputil.ReverseProxy
	up        Upstream
	protected []string
	caPath    string

	mu     sync.Mutex
	nonces map[string]Identity
}

// NewWindow starts a TLS listener on 127.0.0.1:0 and writes its CA to <dir>/window-ca.crt.
func NewWindow(up Upstream, protected []string, dir string) (*Window, error) {
	target, err := url.Parse(up.Server)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream server: %w", err)
	}
	ln, caPath, err := listenLoopback(dir)
	if err != nil {
		return nil, err
	}
	w := &Window{listener: ln, up: up, protected: protected, caPath: caPath, nonces: map[string]Identity{}}
	w.proxy = newUpstreamProxy(target, up)
	w.server = &http.Server{Handler: http.HandlerFunc(w.serveHTTP)}
	go func() { _ = w.server.Serve(ln) }()
	return w, nil
}

// listenLoopback opens the window's TLS listener and writes its CA for kubectl.
func listenLoopback(dir string) (net.Listener, string, error) {
	cert, caPEM, err := loopbackCert()
	if err != nil {
		return nil, "", err
	}
	caPath := filepath.Join(dir, windowCAFile)
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		return nil, "", fmt.Errorf("writing window ca: %w", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		return nil, "", fmt.Errorf("listening on loopback: %w", err)
	}
	return ln, caPath, nil
}

// stamp is what serveHTTP decided for a request, handed to Rewrite.
type stamp struct {
	token string
	id    Identity
}

type stampKey struct{}

// newUpstreamProxy forwards to target over a verified TLS connection.
// FlushInterval -1 keeps logs -f and watch streaming.
func newUpstreamProxy(target *url.URL, up Upstream) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite:       func(pr *httputil.ProxyRequest) { rewrite(pr, target) },
		Transport:     up.transport(),
		FlushInterval: -1,
	}
}

// transport verifies the API server against CA and presents ClientCert when set.
func (up Upstream) transport() *http.Transport {
	tlsCfg := &tls.Config{RootCAs: up.CA}
	if up.ClientCert != nil {
		tlsCfg.Certificates = []tls.Certificate{*up.ClientCert}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsCfg
	transport.ForceAttemptHTTP2 = false // exec and port-forward need HTTP/1.1 Upgrade
	return transport
}

// rewrite stamps the outbound request. It must run here, after ReverseProxy
// drops hop-by-hop headers, or a client's "Connection: Impersonate-User" would
// strip the stamped identity.
func rewrite(pr *httputil.ProxyRequest, target *url.URL) {
	pr.SetURL(target)
	s, _ := pr.In.Context().Value(stampKey{}).(stamp)
	stampIdentity(pr.Out.Header, s.token, s.id)
}

// Close stops the listener and drops any open connections.
func (w *Window) Close() error {
	return w.server.Close()
}

// Register binds a fresh nonce to id until release is called.
func (w *Window) Register(id Identity) (string, func()) {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	nonce := hex.EncodeToString(b)
	w.mu.Lock()
	w.nonces[nonce] = id
	w.mu.Unlock()
	release := func() {
		w.mu.Lock()
		delete(w.nonces, nonce)
		w.mu.Unlock()
	}
	return nonce, release
}

// KubectlArgs returns the leading args for a registered nonce.
func (w *Window) KubectlArgs(nonce string) []string {
	port := w.listener.Addr().(*net.TCPAddr).Port
	return []string{
		fmt.Sprintf("--server=https://127.0.0.1:%d", port),
		"--certificate-authority=" + w.caPath,
		"--token=" + nonce,
	}
}

func (w *Window) lookup(r *http.Request) (Identity, bool) {
	nonce, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return Identity{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.nonces[nonce]
	return id, ok
}

func (w *Window) serveHTTP(rw http.ResponseWriter, r *http.Request) {
	id, ok := w.lookup(r)
	if !ok {
		refuse(rw, "unknown command token", http.StatusUnauthorized)
		return
	}
	if msg := w.refusal(r); msg != "" {
		refuse(rw, msg, http.StatusForbidden)
		return
	}
	token, err := w.up.token()
	if err != nil {
		refuse(rw, err.Error(), http.StatusBadGateway)
		return
	}
	ctx := context.WithValue(r.Context(), stampKey{}, stamp{token: token, id: id})
	w.proxy.ServeHTTP(rw, r.WithContext(ctx))
}

// refuse writes an exact plain-text body; http.Error would append a newline.
func refuse(rw http.ResponseWriter, msg string, code int) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	rw.Header().Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(code)
	_, _ = rw.Write([]byte(msg))
}

// refusal returns the 403 body for a request the window will not forward, or "".
func (w *Window) refusal(r *http.Request) string {
	// Check the decoded, escaped and cleaned forms so encoding tricks cannot hide a namespace.
	paths := []string{r.URL.Path, r.URL.EscapedPath(), path.Clean(r.URL.Path)}
	for _, ns := range w.protected {
		for _, p := range paths {
			if protectedPath(r.Method, p, []string{ns}) {
				return fmt.Sprintf("namespace %q is reserved for the kbridge agent", ns)
			}
		}
	}
	if r.Method == http.MethodGet && clusterSecretsPath(paths) {
		return "reading secrets across all namespaces is not allowed through kbridge"
	}
	return ""
}

// clusterSecretsPath reports whether any form of the path lists or watches
// secrets across all namespaces, including the legacy /api/v1/watch/secrets.
func clusterSecretsPath(paths []string) bool {
	for _, p := range paths {
		c := path.Clean(p)
		if c == "/api/v1/secrets" || c == "/api/v1/watch/secrets" || strings.HasPrefix(c, "/api/v1/watch/secrets/") {
			return true
		}
	}
	return false
}

// protectedPath reports whether a request reaches into a protected namespace:
// any path with the segments namespaces/<ns>/..., or a non-GET on the namespace itself.
func protectedPath(method, p string, protected []string) bool {
	segs := strings.Split(p, "/")
	for _, ns := range protected {
		if method != http.MethodGet && p == "/api/v1/namespaces/"+ns {
			return true
		}
		for i := 0; i+2 < len(segs); i++ {
			if segs[i] == "namespaces" && segs[i+1] == ns {
				return true
			}
		}
	}
	return false
}

// token re-reads the token file on each request; projected tokens rotate.
func (up Upstream) token() (string, error) {
	if up.TokenFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(up.TokenFile)
	if err != nil {
		return "", fmt.Errorf("reading agent token: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("reading agent token: token file is empty")
	}
	return tok, nil
}

// stampIdentity drops every identity claim the client made and sets the agent's
// token and, when given, the command's impersonated user.
func stampIdentity(h http.Header, token string, id Identity) {
	for k := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Impersonate-") {
			h.Del(k)
		}
	}
	h.Del("Authorization")
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	if id.User != "" {
		h.Set("Impersonate-User", id.User)
		h.Set("Impersonate-Extra-Kbridge-Request-Id", id.RequestID)
	}
}

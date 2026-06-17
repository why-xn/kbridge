package central

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/why-xn/kbridge/internal/auth"
)

// newRBACTestServer builds an HTTP server enforcing the given policy, with one
// connected agent for cluster "prod".
func newRBACTestServer(t *testing.T, policyYAML string) (*HTTPServer, *auth.JWTManager) {
	t.Helper()
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)

	eng := &PolicyEngine{}
	eng.current.Store(mustParse(t, policyYAML))

	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})

	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store), eng, nil, jm)
	return srv, jm
}

func execRequest(t *testing.T, srv *HTTPServer, token string, command []string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(ExecRequest{Command: command})
	req, _ := http.NewRequest("POST", "/api/v1/clusters/prod/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestExecHandler_RBACDeniesUnpermittedCommand(t *testing.T) {
	// viewer default: read-only on everything.
	srv, jm := newRBACTestServer(t, `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get", "list"]
`)
	token, err := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "dev@x.com"})
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	// "delete" is not in the viewer verb set -> must be rejected with 403,
	// before the command is ever routed to the agent.
	w := execRequest(t, srv, token, []string{"delete", "pods", "web-1"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 for denied command, got %d: %s", w.Code, w.Body.String())
	}
}

func TestExecHandler_RBACRequiresAuthClaims(t *testing.T) {
	srv, _ := newRBACTestServer(t, `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
`)
	// No token -> the auth middleware on /api/v1 rejects with 401 before the
	// handler runs.
	w := execRequest(t, srv, "", []string{"get", "pods"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d: %s", w.Code, w.Body.String())
	}
}

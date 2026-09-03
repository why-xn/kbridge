package controlplane

import (
	"bytes"
	"context"
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
	return newRBACTestServerWithStore(t, policyYAML, newTestStore(t))
}

// newRBACTestServerWithStore is newRBACTestServer over a caller-owned store, so
// a test can read back the audit entries the server wrote.
func newRBACTestServerWithStore(t *testing.T, policyYAML string, store *SQLiteStore) (*HTTPServer, *auth.JWTManager) {
	t.Helper()
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)

	eng := mustPolicyEngine(t, policyYAML)

	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})

	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper), eng,
		NewAuditRecorder(store), nil, jm)
	return srv, jm
}

// userToken seeds a user and mints an access token for it. The user must exist
// because audit_logs.user_id is a foreign key onto users.id, so a token for a
// phantom user would make every audit write fail.
func userToken(t *testing.T, jm *auth.JWTManager, store *SQLiteStore, email string) string {
	t.Helper()
	user := &User{Email: email, Name: email, PasswordHash: "h", IsActive: true}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, err := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: email})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return token
}

func execRequest(t *testing.T, srv *HTTPServer, token string, command []string) *httptest.ResponseRecorder {
	t.Helper()
	return execRequestBody(t, srv, token, ExecRequest{Command: command})
}

// execRequestBody posts an arbitrary exec request, for tests that need to set
// fields beyond the command itself.
func execRequestBody(t *testing.T, srv *HTTPServer, token string, er ExecRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(er)
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

// mustPolicyEngine builds a policy engine from an inline document, failing the
// test if it does not parse.
func mustPolicyEngine(t *testing.T, doc string) *PolicyEngine {
	t.Helper()
	eng, err := NewPolicyEngineFromBytes([]byte(doc))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	return eng
}

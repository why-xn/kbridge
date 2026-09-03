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

// guardedPolicyYAML lets developers do anything, then guards production.
const guardedPolicyYAML = `
default: developer
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
guardrails:
  - name: no-prod-ns-delete
    match:
      clusters: ["prod"]
      resources: ["namespaces", "ns"]
      verbs: ["delete"]
    action: deny
    message: "deleting namespaces in production is not allowed"
  - name: prod-delete-needs-reason
    match:
      clusters: ["prod"]
      verbs: ["delete"]
    action: require-reason
`

// newGuardedServer builds a guardrail-enforcing server over a store the test
// keeps, so it can read back what was audited.
func newGuardedServer(t *testing.T) (*HTTPServer, *SQLiteStore, string) {
	t.Helper()
	store := newTestStore(t)
	srv, jm := newRBACTestServerWithStore(t, guardedPolicyYAML, store)
	return srv, store, userToken(t, jm, store, "dev@corp.com")
}

// rejectionBody decodes a 403 response body.
func rejectionBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

// auditEntries reads every audit row the server wrote.
func auditEntries(t *testing.T, store *SQLiteStore) []*AuditLog {
	t.Helper()
	logs, _, err := store.ListAuditLogs(context.Background(), AuditLogFilter{})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	return logs
}

func TestGuardrail_DenyBlocksCommand(t *testing.T) {
	srv, store, token := newGuardedServer(t)

	w := execRequest(t, srv, token, []string{"delete", "ns", "payments"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}

	body := rejectionBody(t, w)
	if body["guardrail"] != "no-prod-ns-delete" {
		t.Errorf("guardrail = %v, want no-prod-ns-delete", body["guardrail"])
	}
	if body["error"] != "deleting namespaces in production is not allowed" {
		t.Errorf("error = %v, want the guardrail's own message", body["error"])
	}
	if _, ok := body["reason_required"]; ok {
		t.Error("a deny guardrail must not suggest that a reason would help")
	}

	logs := auditEntries(t, store)
	if len(logs) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(logs))
	}
	if logs[0].Status != AuditStatusBlocked {
		t.Errorf("status = %q, want %q", logs[0].Status, AuditStatusBlocked)
	}
	if logs[0].ErrorMessage == "" {
		t.Error("a blocked entry should record why it was blocked")
	}
}

func TestGuardrail_RequireReasonWithoutOne(t *testing.T) {
	srv, store, token := newGuardedServer(t)

	w := execRequest(t, srv, token, []string{"delete", "pod", "api-0"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}

	body := rejectionBody(t, w)
	if body["reason_required"] != true {
		t.Errorf("reason_required = %v, want true so the CLI can prompt", body["reason_required"])
	}
	if body["guardrail"] != "prod-delete-needs-reason" {
		t.Errorf("guardrail = %v, want prod-delete-needs-reason", body["guardrail"])
	}

	logs := auditEntries(t, store)
	if len(logs) != 1 || logs[0].Status != AuditStatusBlocked {
		t.Fatalf("want one blocked audit entry, got %d", len(logs))
	}
}

// TestGuardrail_ReasonAdmitsAndIsAudited covers the accepted path: a reason gets
// the command past the guardrail and is stored on the audit entry. No agent
// answers, so the command then times out; the short client timeout keeps the
// wait to the handler's fixed grace period.
func TestGuardrail_ReasonAdmitsAndIsAudited(t *testing.T) {
	srv, store, token := newGuardedServer(t)

	w := execRequestBody(t, srv, token, ExecRequest{
		Command: []string{"delete", "pod", "api-0"},
		Reason:  "  INC-4521 rollback  ",
		Timeout: 1,
	})
	if w.Code == http.StatusForbidden {
		t.Fatalf("a command carrying a reason was refused: %s", w.Body.String())
	}

	logs := auditEntries(t, store)
	if len(logs) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(logs))
	}
	if logs[0].Reason != "INC-4521 rollback" {
		t.Errorf("reason = %q, want the trimmed justification", logs[0].Reason)
	}
	if logs[0].Status == AuditStatusBlocked {
		t.Error("status is blocked, but the guardrail should have admitted this command")
	}
}

// TestGuardrail_NonProdClusterUnaffected pins the match scoping: the same
// command against a cluster the guardrails do not name must pass untouched.
func TestGuardrail_NonProdClusterUnaffected(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	eng := mustPolicyEngine(t, guardedPolicyYAML)

	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a2", ClusterName: "dev-1"})
	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper), eng,
		NewAuditRecorder(store), nil, jm)
	token := userToken(t, jm, store, "dev@corp.com")

	body, _ := json.Marshal(ExecRequest{Command: []string{"delete", "ns", "payments"}, Timeout: 1})
	req, _ := http.NewRequest("POST", "/api/v1/clusters/dev-1/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("guardrails scoped to prod must not fire on dev-1: %s", w.Body.String())
	}
}

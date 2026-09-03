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

// approvalPolicyYAML guards production writes behind an approved grant.
const approvalPolicyYAML = `
default: developer
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
guardrails:
  - name: prod-writes-need-approval
    match:
      clusters: ["prod"]
      verbs: ["delete", "apply"]
    action: require-approval
    message: "changing production requires an approved grant"
`

// jitServer is a control plane with guardrails and just-in-time access wired up.
type jitServer struct {
	srv    *HTTPServer
	store  *SQLiteStore
	grants *GrantService
	jm     *auth.JWTManager
	user   string
	admin  string
}

// newJITServer builds a server enforcing approvalPolicyYAML, with grants backed
// by a frozen clock so expiry is exact.
func newJITServer(t *testing.T, now time.Time) *jitServer {
	t.Helper()
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	eng := mustPolicyEngine(t, approvalPolicyYAML)
	audit := NewAuditRecorder(store)

	limits := GrantsConfig{MaxDuration: 8 * time.Hour, DefaultDuration: time.Hour}
	grants := NewGrantService(store, audit, limits)
	grants.now = func() time.Time { return now }

	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})

	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper), eng,
		audit, nil, jm, WithGrants(grants, limits))

	return &jitServer{
		srv: srv, store: store, grants: grants, jm: jm,
		user:  userToken(t, jm, store, "dev@corp.com"),
		admin: adminToken(t, jm, store, "boss@corp.com"),
	}
}

// adminToken seeds an admin user and mints a token carrying the admin claim.
func adminToken(t *testing.T, jm *auth.JWTManager, store *SQLiteStore, email string) string {
	t.Helper()
	user := &User{Email: email, Name: email, PasswordHash: "h", IsActive: true, IsAdmin: true}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	token, err := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: email, IsAdmin: true})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return token
}

// do issues a request against the server and returns the decoded body and code.
func (j *jitServer) do(t *testing.T, method, path, token string, payload any) (map[string]any, int) {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req, _ := http.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	j.srv.Handler().ServeHTTP(w, req)

	var decoded map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return decoded, w.Code
}

// exec posts a command as the given user.
func (j *jitServer) exec(t *testing.T, token string, command []string) (map[string]any, int) {
	t.Helper()
	return j.do(t, http.MethodPost, "/api/v1/clusters/prod/exec", token,
		ExecRequest{Command: command, Timeout: 1})
}

// requestGrant asks for access as the ordinary user and returns the grant ID.
func (j *jitServer) requestGrant(t *testing.T, cluster, namespace string) string {
	t.Helper()
	body, code := j.do(t, http.MethodPost, "/api/v1/grants", j.user, map[string]any{
		"cluster":   cluster,
		"namespace": namespace,
		"duration":  "2h",
		"reason":    "INC-4521 rollback",
	})
	if code != http.StatusCreated {
		t.Fatalf("request grant: status %d, body %v", code, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("grant response has no id: %v", body)
	}
	return id
}

func TestGrantHTTP_RequestLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)

	id := j.requestGrant(t, "prod", "")

	t.Run("a new request is pending and grants nothing yet", func(t *testing.T) {
		body, code := j.do(t, http.MethodGet, "/api/v1/grants", j.user, nil)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200", code)
		}
		grants := body["grants"].([]any)
		if len(grants) != 1 {
			t.Fatalf("got %d grants, want 1", len(grants))
		}
		g := grants[0].(map[string]any)
		if g["display_status"] != "pending" {
			t.Errorf("display_status = %v, want pending", g["display_status"])
		}
		if g["expires_at"] != nil {
			t.Error("a pending grant must not carry an expiry")
		}
	})

	t.Run("approval activates it", func(t *testing.T) {
		body, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.admin,
			map[string]any{"note": "paged, go ahead"})
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %v", code, body)
		}
		if body["display_status"] != "approved" {
			t.Errorf("display_status = %v, want approved", body["display_status"])
		}
		if body["decided_by"] != "boss@corp.com" {
			t.Errorf("decided_by = %v, want the approver", body["decided_by"])
		}
		if body["expires_at"] == nil {
			t.Error("an approved grant must carry an expiry")
		}
	})

	t.Run("a decided grant refuses a second decision", func(t *testing.T) {
		_, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.admin, nil)
		if code != http.StatusConflict {
			t.Errorf("status = %d, want 409", code)
		}
	})

	t.Run("revoke ends it", func(t *testing.T) {
		body, code := j.do(t, http.MethodDelete, "/api/v1/admin/grants/"+id, j.admin, nil)
		if code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %v", code, body)
		}
		if body["display_status"] != "revoked" {
			t.Errorf("display_status = %v, want revoked", body["display_status"])
		}
	})
}

func TestGrantHTTP_RequestValidation(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)

	tests := []struct {
		name    string
		payload map[string]any
		want    int
	}{
		{"valid", map[string]any{"cluster": "prod", "reason": "INC-4521 rollback", "duration": "2h"}, http.StatusCreated},
		{"duration defaults when omitted", map[string]any{"cluster": "prod", "reason": "INC-4521 rollback"}, http.StatusCreated},
		{"missing cluster", map[string]any{"reason": "INC-4521 rollback"}, http.StatusBadRequest},
		{"missing reason", map[string]any{"cluster": "prod"}, http.StatusBadRequest},
		{"short reason", map[string]any{"cluster": "prod", "reason": "oops"}, http.StatusBadRequest},
		{"unparseable duration", map[string]any{"cluster": "prod", "reason": "INC-4521 rollback", "duration": "soon"}, http.StatusBadRequest},
		{"duration over the ceiling", map[string]any{"cluster": "prod", "reason": "INC-4521 rollback", "duration": "100h"}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, code := j.do(t, http.MethodPost, "/api/v1/grants", j.user, tt.payload)
			if code != tt.want {
				t.Errorf("status = %d, want %d", code, tt.want)
			}
		})
	}
}

func TestGrantHTTP_AuthorizationBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)
	id := j.requestGrant(t, "prod", "")

	t.Run("an ordinary user cannot list every grant", func(t *testing.T) {
		_, code := j.do(t, http.MethodGet, "/api/v1/admin/grants", j.user, nil)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("an ordinary user cannot approve", func(t *testing.T) {
		_, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.user, nil)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", code)
		}
	})

	t.Run("requesting needs authentication", func(t *testing.T) {
		_, code := j.do(t, http.MethodPost, "/api/v1/grants", "", map[string]any{
			"cluster": "prod", "reason": "INC-4521 rollback"})
		if code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
	})

	t.Run("an unknown grant is a 404", func(t *testing.T) {
		_, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/no-such-id/approve", j.admin, nil)
		if code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})
}

// TestGrantHTTP_AdmitsGuardedCommand is the point of the whole feature: a
// require-approval guardrail refuses the command until a grant exists, then
// admits it, and stops again once the grant is revoked.
func TestGrantHTTP_AdmitsGuardedCommand(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)
	command := []string{"delete", "pod", "api-0"}

	t.Run("refused before any grant", func(t *testing.T) {
		body, code := j.exec(t, j.user, command)
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["approval_required"] != true {
			t.Errorf("approval_required = %v, want true so the CLI can point at kb request", body["approval_required"])
		}
		if body["guardrail"] != "prod-writes-need-approval" {
			t.Errorf("guardrail = %v, want prod-writes-need-approval", body["guardrail"])
		}
	})

	id := j.requestGrant(t, "prod", "")

	t.Run("still refused while the grant is pending", func(t *testing.T) {
		_, code := j.exec(t, j.user, command)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: a pending request grants nothing", code)
		}
	})

	t.Run("admitted once approved", func(t *testing.T) {
		if _, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.admin, nil); code != http.StatusOK {
			t.Fatalf("approve: status %d", code)
		}
		_, code := j.exec(t, j.user, command)
		if code == http.StatusForbidden {
			t.Fatal("an approved grant should admit the command")
		}
	})

	t.Run("the command is audited against the grant", func(t *testing.T) {
		logs, _, err := j.store.ListAuditLogs(context.Background(), AuditLogFilter{GrantID: id})
		if err != nil {
			t.Fatalf("list audit logs: %v", err)
		}
		var ran bool
		for _, l := range logs {
			if l.Command == "delete pod api-0" {
				ran = true
				if l.Reason != "INC-4521 rollback" {
					t.Errorf("reason = %q, want the grant's justification carried onto the command", l.Reason)
				}
			}
		}
		if !ran {
			t.Errorf("no audit entry ties the command to grant %s", id)
		}
	})

	t.Run("refused again after revocation", func(t *testing.T) {
		if _, code := j.do(t, http.MethodDelete, "/api/v1/admin/grants/"+id, j.admin, nil); code != http.StatusOK {
			t.Fatalf("revoke: status %d", code)
		}
		_, code := j.exec(t, j.user, command)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 after revocation", code)
		}
	})
}

func TestGrantHTTP_ExpiredGrantStopsAdmitting(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)
	command := []string{"delete", "pod", "api-0"}

	id := j.requestGrant(t, "prod", "")
	if _, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.admin, nil); code != http.StatusOK {
		t.Fatalf("approve: status %d", code)
	}
	if _, code := j.exec(t, j.user, command); code == http.StatusForbidden {
		t.Fatal("precondition: the fresh grant should admit the command")
	}

	// Walk past the window. Nothing rewrites the row: expiry is derived.
	j.grants.now = func() time.Time { return now.Add(3 * time.Hour) }

	if _, code := j.exec(t, j.user, command); code != http.StatusForbidden {
		t.Error("an expired grant must stop admitting commands")
	}
}

func TestGrantHTTP_GrantScopeIsHonoured(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)

	// A grant scoped to one namespace must not open up another.
	id := j.requestGrant(t, "prod", "payments")
	if _, code := j.do(t, http.MethodPost, "/api/v1/admin/grants/"+id+"/approve", j.admin, nil); code != http.StatusOK {
		t.Fatalf("approve: status %d", code)
	}

	if _, code := j.exec(t, j.user, []string{"delete", "pod", "api-0", "-n", "payments"}); code == http.StatusForbidden {
		t.Error("the granted namespace should be admitted")
	}
	if _, code := j.exec(t, j.user, []string{"delete", "pod", "api-0", "-n", "billing"}); code != http.StatusForbidden {
		t.Error("a namespace outside the grant must stay refused")
	}
}

// TestGrantHTTP_ClientCannotAssertItsOwnGrant pins that grant_id is server-side
// only. A client that sends one must not have it believed.
func TestGrantHTTP_ClientCannotAssertItsOwnGrant(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	j := newJITServer(t, now)

	body, code := j.do(t, http.MethodPost, "/api/v1/clusters/prod/exec", j.user, map[string]any{
		"command":  []string{"delete", "pod", "api-0"},
		"grant_id": "forged-grant",
		"timeout":  1,
	})
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: a forged grant_id must not admit a command", code)
	}
	if body["approval_required"] != true {
		t.Errorf("approval_required = %v, want true", body["approval_required"])
	}
}

// TestGrantHTTP_DisabledWhenNotWired confirms a deployment without grants still
// enforces the guardrail rather than failing open.
func TestGrantHTTP_DisabledWhenNotWired(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	eng := mustPolicyEngine(t, approvalPolicyYAML)
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})
	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper), eng,
		NewAuditRecorder(store), nil, jm)
	token := userToken(t, jm, store, "dev@corp.com")

	w := execRequestBody(t, srv, token, ExecRequest{Command: []string{"delete", "pod", "api-0"}, Timeout: 1})
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: without grants the guardrail must still refuse", w.Code)
	}

	// The grant endpoints are simply absent.
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/grants", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when grants are not wired", rec.Code)
	}
}

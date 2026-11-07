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

func TestAuditStatusCanceled(t *testing.T) {
	if AuditStatusCanceled != "canceled" {
		t.Errorf("want canceled, got %q", AuditStatusCanceled)
	}
}

func TestAuditRecorder_Record(t *testing.T) {
	store := newTestStore(t)
	r := NewAuditRecorder(store)

	r.Record(&AuditLog{
		UserEmail:   "u@example.com",
		ClusterName: "dev-1",
		Command:     "get pods",
		Namespace:   "default",
		Status:      AuditStatusSuccess,
	})

	logs, total, err := store.ListAuditLogs(context.Background(), AuditLogFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("want 1 audit log, got total=%d len=%d", total, len(logs))
	}
	if logs[0].Command != "get pods" || logs[0].Status != AuditStatusSuccess {
		t.Errorf("unexpected entry: %+v", logs[0])
	}
}

func TestAdminHandler_ListAuditLogs(t *testing.T) {
	ah, store := newTestAdminHandlers(t)
	ctx := context.Background()
	seed := func(email, cluster, status string) {
		if err := store.CreateAuditLog(ctx, &AuditLog{
			UserEmail: email, ClusterName: cluster, Command: "get pods", Status: status,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	seed("a@x.com", "dev", AuditStatusSuccess)
	seed("a@x.com", "prod", AuditStatusDenied)
	seed("b@x.com", "dev", AuditStatusSuccess)

	type auditResp struct {
		Logs  []AuditLog `json:"logs"`
		Total int        `json:"total"`
	}
	get := func(query string) auditResp {
		t.Helper()
		w := doRequest(t, "GET", "/api/v1/admin/audit", ah.HandleListAuditLogs,
			"GET", "/api/v1/admin/audit"+query, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("query %q: want 200, got %d", query, w.Code)
		}
		var r auditResp
		json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}

	if r := get(""); r.Total != 3 {
		t.Errorf("all: want total 3, got %d", r.Total)
	}
	if r := get("?user=a@x.com"); r.Total != 2 {
		t.Errorf("user filter: want 2, got %d", r.Total)
	}
	if r := get("?status=denied"); r.Total != 1 || r.Logs[0].ClusterName != "prod" {
		t.Errorf("status filter: want 1 prod, got %+v", r)
	}
	if r := get("?cluster=dev"); r.Total != 2 {
		t.Errorf("cluster filter: want 2, got %d", r.Total)
	}
}

func TestAdminHandler_ListAuditLogs_BadTimestamp(t *testing.T) {
	ah, _ := newTestAdminHandlers(t)
	w := doRequest(t, "GET", "/api/v1/admin/audit", ah.HandleListAuditLogs,
		"GET", "/api/v1/admin/audit?from=not-a-time", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad timestamp, got %d", w.Code)
	}
}

func TestExecHandler_RecordsDeniedAudit(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	eng := &PolicyEngine{}
	eng.current.Store(mustParse(t, `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
`))
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod", Capabilities: []string{capImpersonation}})
	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper, nil), eng, NewAuditRecorder(store), nil, jm)

	// The audited user must exist (audit_logs.user_id FK -> users.id).
	user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: "dev@x.com"})
	if w := execRequest(t, srv, token, []string{"delete", "pods", "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}

	logs, total, _ := store.ListAuditLogs(context.Background(), AuditLogFilter{})
	if total != 1 {
		t.Fatalf("want 1 audit entry, got %d", total)
	}
	if logs[0].Status != AuditStatusDenied || logs[0].UserEmail != "dev@x.com" || logs[0].Command != "delete pods x" {
		t.Errorf("unexpected denied audit entry: %+v", logs[0])
	}
}

// A command refused because the cluster's agent is gone is still an attempt,
// so it is audited as failed with no exit code.
func TestExecHandler_RecordsDisconnectedAudit(t *testing.T) {
	register := func(a *AgentStore) {
		a.Register(&AgentInfo{ID: "a1", ClusterName: "prod", TokenID: "tok-1", Capabilities: []string{capImpersonation}})
	}
	tests := []struct {
		name  string
		setup func(*AgentStore, *SQLiteStore)
	}{
		{"agent disconnected", func(a *AgentStore, _ *SQLiteStore) { register(a); a.MarkAgentDisconnected("a1") }},
		{"agent token revoked", func(a *AgentStore, _ *SQLiteStore) { register(a); a.DropByToken("tok-1") }},
		// A token was issued but no agent registered yet, or the control plane
		// restarted: the cluster is known, so it is disconnected, not unknown.
		{"known cluster, no agent", func(_ *AgentStore, db *SQLiteStore) {
			if err := db.CreateCluster(context.Background(), &Cluster{Name: "prod", Status: AgentStatusDisconnected}); err != nil {
				t.Fatalf("seed cluster: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
			agents := NewAgentStore()
			tt.setup(agents, store)
			srv := NewHTTPServer(agents, NewCommandQueue(),
				NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper, nil), nil, NewAuditRecorder(store), nil, jm)
			user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
			if err := store.CreateUser(context.Background(), user); err != nil {
				t.Fatalf("seed user: %v", err)
			}
			token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: "dev@x.com"})

			if w := execRequest(t, srv, token, []string{"get", "pods"}); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("want 503, got %d", w.Code)
			}
			logs, total, _ := store.ListAuditLogs(context.Background(), AuditLogFilter{})
			if total != 1 {
				t.Fatalf("want 1 audit entry, got %d", total)
			}
			got := logs[0]
			if got.Status != AuditStatusFailed || got.ErrorMessage != "agent is disconnected" || got.ExitCode != nil ||
				got.Command != "get pods" || got.UserEmail != "dev@x.com" || got.ClusterName != "prod" {
				t.Errorf("unexpected audit entry: %+v", got)
			}
		})
	}
}

func TestAuthorizeExec_RefusesUnsafeArgs(t *testing.T) {
	const want = "flag --server is not allowed: kbridge manages the cluster connection"
	denyAll := `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
`
	tests := []struct {
		name, path string
		policy     bool
	}{
		{"exec with policy", "exec", true},
		{"exec nil policy", "exec", false},
		{"stream with policy", "stream", true},
		{"stream nil policy", "stream", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
			var eng *PolicyEngine
			if tt.policy {
				eng = &PolicyEngine{}
				eng.current.Store(mustParse(t, denyAll))
			}
			agents := NewAgentStore()
			agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod", Capabilities: []string{capImpersonation}})
			queue := NewCommandQueue()
			srv := NewHTTPServer(agents, queue, NewAuthHandlers(store, jm, time.Hour),
				NewAdminHandlers(store, testPepper, nil), eng, NewAuditRecorder(store), NewSessionManager(10), jm)
			user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
			if err := store.CreateUser(context.Background(), user); err != nil {
				t.Fatal(err)
			}
			token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: user.Email})
			body, _ := json.Marshal(ExecRequest{Command: []string{"--server=x", "get", "pods"}})
			req, _ := http.NewRequest("POST", "/api/v1/clusters/prod/"+tt.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
			}
			var got map[string]string
			json.Unmarshal(w.Body.Bytes(), &got)
			if got["error"] != want {
				t.Errorf("error = %q, want %q", got["error"], want)
			}
			logs, total, _ := store.ListAuditLogs(context.Background(), AuditLogFilter{})
			if total != 1 || logs[0].Status != AuditStatusDenied {
				t.Errorf("want 1 denied audit row, got %d: %+v", total, logs)
			}
			if n := len(queue.GetPendingForAgent("a1")); n != 0 {
				t.Errorf("want nothing enqueued, got %d", n)
			}
		})
	}
}

func TestAuthorizeExec_DenialBody(t *testing.T) {
	const want = `permission denied: delete pods in namespace "team-b" on cluster "prod" is not allowed for dev@x.com; ask a kbridge admin to extend your role`
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	eng := &PolicyEngine{}
	eng.current.Store(mustParse(t, `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
`))
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod", Capabilities: []string{capImpersonation}})
	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper, nil), eng, NewAuditRecorder(store), nil, jm)
	user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: user.ID, Email: user.Email})

	w := execRequest(t, srv, token, []string{"delete", "pods", "-n", "team-b"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["error"] != want {
		t.Errorf("body error = %q, want %q", got["error"], want)
	}
	logs, _, _ := store.ListAuditLogs(context.Background(), AuditLogFilter{})
	if len(logs) != 1 || logs[0].ErrorMessage != want {
		t.Errorf("audit error column = %+v, want %q", logs, want)
	}
}

func TestAuthorizeExec_ManifestNamespace(t *testing.T) {
	srv, jm := newRBACTestServer(t, `
default: developer
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["team-a"]
        resources: ["*"]
        verbs: ["*"]
`)
	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "dev@x.com"})
	tests := []struct {
		name      string
		command   []string
		namespace string
		manifest  string
		wantDeny  string
	}{
		{"manifest namespace allowed without -n", []string{"apply", "-f", "-"}, "", "metadata:\n  namespace: team-a\n", ""},
		{"unnamed document falls back to default", []string{"apply", "-f", "-"}, "", "metadata:\n  name: x\n",
			`permission denied: apply in namespace "default" on cluster "prod" is not allowed for dev@x.com; ask a kbridge admin to extend your role`},
		{"manifest namespace differs from -n", []string{"apply", "-f", "-", "-n", "team-a"}, "team-a",
			"metadata:\n  namespace: team-a\n---\nmetadata:\n  namespace: team-b\n",
			`permission denied: apply in namespace "team-b" on cluster "prod" is not allowed for dev@x.com; ask a kbridge admin to extend your role`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(ExecRequest{Command: tt.command, Namespace: tt.namespace, Stdin: tt.manifest, Timeout: 1})
			// No agent answers: an allowed command would wait out the timeout grace.
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", "/api/v1/clusters/prod/exec", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			var got map[string]string
			json.Unmarshal(w.Body.Bytes(), &got)
			if tt.wantDeny == "" && w.Code == http.StatusForbidden {
				t.Fatalf("want allowed, got 403: %s", got["error"])
			}
			if tt.wantDeny != "" && (w.Code != http.StatusForbidden || got["error"] != tt.wantDeny) {
				t.Errorf("got %d %q, want 403 %q", w.Code, got["error"], tt.wantDeny)
			}
		})
	}
}

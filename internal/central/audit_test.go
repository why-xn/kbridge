package central

import (
	"context"
	"encoding/json"
	"net/http"
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
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})
	srv := NewHTTPServer(agents, NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store), eng, NewAuditRecorder(store), nil, jm)

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

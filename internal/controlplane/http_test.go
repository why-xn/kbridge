package controlplane

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
)

func newTestHTTPServer() (*HTTPServer, *AgentStore, *CommandQueue) {
	store := NewAgentStore()
	cmdQueue := NewCommandQueue()
	return NewHTTPServer(store, cmdQueue, nil, nil, nil, nil, nil, nil), store, cmdQueue
}

func TestHTTPServer_Health(t *testing.T) {
	srv, _, _ := newTestHTTPServer()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("expected status='healthy', got %q", resp["status"])
	}
}

func TestHTTPServer_ListClusters_Empty(t *testing.T) {
	srv, _, _ := newTestHTTPServer()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string][]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	clusters, ok := resp["clusters"]
	if !ok {
		t.Error("expected 'clusters' key in response")
	}
	if len(clusters) != 0 {
		t.Errorf("expected empty clusters list, got %d items", len(clusters))
	}
}

func TestHTTPServer_ListClusters_WithAgents(t *testing.T) {
	srv, store, _ := newTestHTTPServer()

	// Register some agents
	store.Register(&AgentInfo{
		ID:          "agent-1",
		ClusterName: "production",
	})
	store.Register(&AgentInfo{
		ID:          "agent-2",
		ClusterName: "staging",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp struct {
		Clusters []ClusterResponse `json:"clusters"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(resp.Clusters))
	}

	// Check that cluster data is returned
	foundProduction := false
	foundStaging := false
	for _, c := range resp.Clusters {
		if c.Name == "production" {
			foundProduction = true
			if c.Status != AgentStatusConnected {
				t.Errorf("expected status %q, got %q", AgentStatusConnected, c.Status)
			}
		}
		if c.Name == "staging" {
			foundStaging = true
		}
	}

	if !foundProduction {
		t.Error("expected to find 'production' cluster")
	}
	if !foundStaging {
		t.Error("expected to find 'staging' cluster")
	}
}

func TestHTTPServer_ExecCommand_ClusterNotFound(t *testing.T) {
	srv, _, _ := newTestHTTPServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/nonexistent/exec", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["error"] != "cluster not found" {
		t.Errorf("expected error='cluster not found', got %q", resp["error"])
	}
}

// A cluster whose agent was dropped by token revocation reads disconnected,
// as `kb clusters list` shows it, not unknown.
func TestHTTPServer_ExecCommand_RevokedCluster(t *testing.T) {
	srv, store, _ := newTestHTTPServer()
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test-cluster", TokenID: "tok-1"})
	store.DropByToken("tok-1")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test-cluster/exec", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusServiceUnavailable || resp["error"] != "cluster agent is disconnected" {
		t.Fatalf("got %d %q, want 503 'cluster agent is disconnected'", rec.Code, resp["error"])
	}
}

func TestHTTPServer_ExecCommand_AgentDisconnected(t *testing.T) {
	srv, store, _ := newTestHTTPServer()

	// Register a disconnected agent
	store.Register(&AgentInfo{
		ID:          "agent-1",
		ClusterName: "test-cluster",
	})
	// Manually set status to disconnected
	store.mu.Lock()
	store.agents["agent-1"].Status = AgentStatusDisconnected
	store.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test-cluster/exec", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp["error"] != "cluster agent is disconnected" {
		t.Errorf("expected error='cluster agent is disconnected', got %q", resp["error"])
	}
}

func TestHTTPServer_ExecCommand_InvalidRequest(t *testing.T) {
	srv, store, _ := newTestHTTPServer()

	// Register a connected agent
	store.Register(&AgentInfo{
		ID:          "agent-1",
		ClusterName: "test-cluster",
	})

	// Send request without body
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test-cluster/exec", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	// Should return 400 Bad Request for missing body
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestHTTPServer_NotFound(t *testing.T) {
	srv, _, _ := newTestHTTPServer()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestBodyLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(bodyLimitMiddleware(16))
	r.POST("/x", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(make([]byte, 1024)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code=%d want 413", w.Code)
	}
}

// newCapabilityTestServer serves cluster "prod" through agent a1 with the given
// capabilities. No agent stream is open, so a session that slips past the gate
// fails with a different message.
func newCapabilityTestServer(t *testing.T, caps []string) (*HTTPServer, *CommandQueue, *SessionManager, string) {
	t.Helper()
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod", Capabilities: caps})
	queue, sm := NewCommandQueue(), NewSessionManager(10)
	srv := NewHTTPServer(agents, queue, NewAuthHandlers(store, jm, time.Hour),
		NewAdminHandlers(store, testPepper, nil), nil, nil, sm, jm)
	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "alice@x"})
	return srv, queue, sm, token
}

func TestExec_RefusesAgentWithoutImpersonation(t *testing.T) {
	execBody, _ := json.Marshal(ExecRequest{Command: []string{"get", "pods"}, Timeout: 1})
	streamBody, _ := json.Marshal(ExecRequest{Command: []string{"logs", "-f", "web"}})
	tests := []struct {
		name string
		path string
		body []byte
	}{
		{"exec", "/api/v1/clusters/prod/exec", execBody},
		{"stream", "/api/v1/clusters/prod/stream", streamBody},
		{"exec attach", "/api/v1/clusters/prod/exec/attach?pod=web&command=sh", nil},
		{"port-forward", "/api/v1/clusters/prod/port-forward?pod=web&port=5432", nil},
	}
	const want = `{"error":"agent does not support impersonation: upgrade the agent for cluster prod"}`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, queue, sm, token := newCapabilityTestServer(t, nil)
			req, _ := http.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusServiceUnavailable || w.Body.String() != want {
				t.Fatalf("got %d %s, want 503 %s", w.Code, w.Body.String(), want)
			}
			if len(queue.commands) != 0 || len(sm.sessions) != 0 {
				t.Errorf("nothing may be routed: %d queued, %d sessions", len(queue.commands), len(sm.sessions))
			}
		})
	}
}

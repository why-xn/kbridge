package central

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHTTPServer() (*HTTPServer, *AgentStore) {
	store := NewAgentStore()
	return NewHTTPServer(store), store
}

func TestHTTPServer_Health(t *testing.T) {
	srv, _ := newTestHTTPServer()
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
	srv, _ := newTestHTTPServer()
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
	srv, store := newTestHTTPServer()

	// Register some agents
	store.Register(&AgentInfo{
		ID:                "agent-1",
		ClusterName:       "production",
		KubernetesVersion: "1.28.0",
		NodeCount:         5,
		Region:            "us-east-1",
		Provider:          "aws",
	})
	store.Register(&AgentInfo{
		ID:                "agent-2",
		ClusterName:       "staging",
		KubernetesVersion: "1.27.0",
		NodeCount:         3,
		Region:            "us-west-2",
		Provider:          "aws",
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
			if c.KubernetesVersion != "1.28.0" {
				t.Errorf("expected k8s version '1.28.0', got %q", c.KubernetesVersion)
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
	srv, _ := newTestHTTPServer()
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

func TestHTTPServer_ExecCommand_AgentDisconnected(t *testing.T) {
	srv, store := newTestHTTPServer()

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

func TestHTTPServer_ExecCommand_ConnectedAgent(t *testing.T) {
	srv, store := newTestHTTPServer()

	// Register a connected agent
	store.Register(&AgentInfo{
		ID:          "agent-1",
		ClusterName: "test-cluster",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/test-cluster/exec", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	// Should return 501 Not Implemented (command execution not yet implemented)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("expected status %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestHTTPServer_NotFound(t *testing.T) {
	srv, _ := newTestHTTPServer()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}

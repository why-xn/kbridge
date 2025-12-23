package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCentralClient_ListClusters(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clusters" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		resp := ClustersResponse{
			Clusters: []ClusterInfo{
				{
					Name:              "prod",
					Status:            "connected",
					KubernetesVersion: "1.28.0",
					NodeCount:         5,
					Region:            "us-east-1",
					Provider:          "aws",
				},
				{
					Name:   "staging",
					Status: "connected",
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	clusters, err := client.ListClusters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(clusters))
	}

	if clusters[0].Name != "prod" {
		t.Errorf("expected first cluster name 'prod', got %q", clusters[0].Name)
	}

	if clusters[0].KubernetesVersion != "1.28.0" {
		t.Errorf("expected kubernetes version '1.28.0', got %q", clusters[0].KubernetesVersion)
	}
}

func TestCentralClient_ListClusters_Empty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ClustersResponse{Clusters: []ClusterInfo{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	clusters, err := client.ListClusters()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters, got %d", len(clusters))
	}
}

func TestCentralClient_ListClusters_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	_, err := client.ListClusters()
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestCentralClient_GetCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ClustersResponse{
			Clusters: []ClusterInfo{
				{Name: "prod", Status: "connected"},
				{Name: "staging", Status: "disconnected"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)

	// Get existing cluster
	cluster, err := client.GetCluster("prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cluster.Name != "prod" {
		t.Errorf("expected name 'prod', got %q", cluster.Name)
	}

	if cluster.Status != "connected" {
		t.Errorf("expected status 'connected', got %q", cluster.Status)
	}

	// Get non-existent cluster
	_, err = client.GetCluster("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent cluster")
	}
}

func TestCentralClient_CheckHealth(t *testing.T) {
	// Healthy server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	err := client.CheckHealth()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCentralClient_CheckHealth_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	err := client.CheckHealth()
	if err == nil {
		t.Error("expected error for unhealthy server")
	}
}

func TestCentralClient_ConnectionError(t *testing.T) {
	// Use a non-existent server
	client := NewCentralClient("http://localhost:59999")

	_, err := client.ListClusters()
	if err == nil {
		t.Error("expected error for connection failure")
	}
}

func TestNewCentralClient(t *testing.T) {
	client := NewCentralClient("http://example.com")

	if client.baseURL != "http://example.com" {
		t.Errorf("expected baseURL 'http://example.com', got %q", client.baseURL)
	}

	if client.httpClient == nil {
		t.Error("expected httpClient to be initialized")
	}
}

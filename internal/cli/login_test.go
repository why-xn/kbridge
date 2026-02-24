package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCentralClient_Login_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)

		if req["email"] != "admin@test.com" {
			t.Errorf("expected email admin@test.com, got %q", req["email"])
		}

		resp := LoginResponse{
			AccessToken:  "access-token-123",
			RefreshToken: "refresh-token-456",
			ExpiresIn:    86400,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	resp, err := client.Login("admin@test.com", "password123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.AccessToken != "access-token-123" {
		t.Errorf("expected access-token-123, got %q", resp.AccessToken)
	}
	if resp.RefreshToken != "refresh-token-456" {
		t.Errorf("expected refresh-token-456, got %q", resp.RefreshToken)
	}
}

func TestCentralClient_Login_InvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	_, err := client.Login("bad@test.com", "wrongpass")
	if err == nil {
		t.Fatal("expected error for invalid credentials")
	}
}

func TestCentralClient_Login_DisabledAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"account is disabled"}`))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	_, err := client.Login("disabled@test.com", "password")
	if err == nil {
		t.Fatal("expected error for disabled account")
	}
}

func TestCentralClient_SetToken_AuthHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		resp := ClustersResponse{Clusters: []ClusterInfo{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	client.SetToken("my-jwt-token")
	client.ListClusters()

	if gotHeader != "Bearer my-jwt-token" {
		t.Errorf("expected 'Bearer my-jwt-token', got %q", gotHeader)
	}
}

func TestCentralClient_NoToken_NoAuthHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		resp := ClustersResponse{Clusters: []ClusterInfo{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	client.ListClusters()

	if gotHeader != "" {
		t.Errorf("expected empty authorization header, got %q", gotHeader)
	}
}

func TestCentralClient_Unauthorized_Response(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid or expired token"}`))
	}))
	defer server.Close()

	client := NewCentralClient(server.URL)
	_, err := client.ListClusters()
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

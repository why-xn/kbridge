package cli

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestControlPlaneClient_Login_Success(t *testing.T) {
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

	client := NewControlPlaneClient(server.URL)
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

func TestControlPlaneClient_Login_InvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	_, err := client.Login("bad@test.com", "wrongpass")
	if err == nil {
		t.Fatal("expected error for invalid credentials")
	}
}

func TestControlPlaneClient_Login_DisabledAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"account is disabled"}`))
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	_, err := client.Login("disabled@test.com", "password")
	if err == nil {
		t.Fatal("expected error for disabled account")
	}
}

func TestControlPlaneClient_SetToken_AuthHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		resp := ClustersResponse{Clusters: []ClusterInfo{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	client.SetToken("my-jwt-token")
	client.ListClusters()

	if gotHeader != "Bearer my-jwt-token" {
		t.Errorf("expected 'Bearer my-jwt-token', got %q", gotHeader)
	}
}

func TestControlPlaneClient_NoToken_NoAuthHeader(t *testing.T) {
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		resp := ClustersResponse{Clusters: []ClusterInfo{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	client.ListClusters()

	if gotHeader != "" {
		t.Errorf("expected empty authorization header, got %q", gotHeader)
	}
}

func TestControlPlaneClient_Unauthorized_Response(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid or expired token"}`))
	}))
	defer server.Close()

	client := NewControlPlaneClient(server.URL)
	_, err := client.ListClusters()
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestPromptControlPlaneURL(t *testing.T) {
	tests := []struct{ name, in, want, wantErr string }{
		{"value", " http://cp:8080 \n", "http://cp:8080", ""},
		{"empty", "\n", "", "control plane URL is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got, err := promptControlPlaneURL(bufio.NewReader(strings.NewReader(tt.in)), &out)
			if got != tt.want || (err != nil) != (tt.wantErr != "") || (err != nil && err.Error() != tt.wantErr) {
				t.Errorf("got %q, %v; want %q, %q", got, err, tt.want, tt.wantErr)
			}
			if out.String() != "Control plane URL: " {
				t.Errorf("prompt = %q", out.String())
			}
		})
	}
}

func TestReadPassword_NonTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	got, err := readPassword(bufio.NewReader(strings.NewReader("secret\n")), int(r.Fd()))
	if err != nil || got != "secret" {
		t.Errorf("got %q, %v; want secret", got, err)
	}
}

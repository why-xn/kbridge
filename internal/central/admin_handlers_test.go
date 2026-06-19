package central

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
)

func newTestAdminHandlers(t *testing.T) (*AdminHandlers, *SQLiteStore) {
	t.Helper()
	store := newTestStore(t)
	return NewAdminHandlers(store, testPepper), store
}

// doRequest runs a single request against a router that has the given route registered.
func doRequest(t *testing.T, method, path string, handler gin.HandlerFunc, reqMethod, reqPath string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.Handle(method, path, handler)
	req, _ := http.NewRequest(reqMethod, reqPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestAdminHandler_CreateAgentToken(t *testing.T) {
	t.Run("valid request creates token and cluster", func(t *testing.T) {
		ah, store := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{
			"cluster_name": "dev-cluster",
			"description":  "ci token",
		})
		w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
			"POST", "/api/v1/admin/agent-tokens", body)

		if w.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
		}

		var resp createAgentTokenResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Token == "" {
			t.Error("expected non-empty plaintext token")
		}
		if resp.TokenPrefix == "" {
			t.Error("expected non-empty token prefix")
		}
		if resp.ClusterName != "dev-cluster" {
			t.Errorf("want cluster dev-cluster, got %q", resp.ClusterName)
		}

		// Cluster row must have been created.
		cl, err := store.GetClusterByName(context.Background(), "dev-cluster")
		if err != nil || cl == nil {
			t.Fatalf("expected cluster to be created, err=%v cl=%v", err, cl)
		}

		// Token must be retrievable by its hash, bound to the cluster.
		stored, err := store.GetAgentTokenByHash(context.Background(), hashAgentToken(testPepper, resp.Token))
		if err != nil || stored == nil {
			t.Fatalf("expected token persisted by hash, err=%v stored=%v", err, stored)
		}
		if stored.ClusterID != cl.ID {
			t.Errorf("token not bound to cluster: token.ClusterID=%q cluster.ID=%q", stored.ClusterID, cl.ID)
		}
	})

	t.Run("reuses existing cluster", func(t *testing.T) {
		ah, store := newTestAdminHandlers(t)
		existing := &Cluster{Name: "prod", Status: "connected"}
		if err := store.CreateCluster(context.Background(), existing); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
		body, _ := json.Marshal(map[string]any{"cluster_name": "prod"})
		w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
			"POST", "/api/v1/admin/agent-tokens", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d", w.Code)
		}
		toks, _ := store.ListAgentTokensByCluster(context.Background(), existing.ID)
		if len(toks) != 1 {
			t.Fatalf("want 1 token on existing cluster, got %d", len(toks))
		}
	})

	t.Run("missing cluster_name is rejected", func(t *testing.T) {
		ah, _ := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{"description": "no cluster"})
		w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
			"POST", "/api/v1/admin/agent-tokens", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", w.Code)
		}
	})

	t.Run("expires_in_days sets expiry", func(t *testing.T) {
		ah, store := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{"cluster_name": "c1", "expires_in_days": 7})
		w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
			"POST", "/api/v1/admin/agent-tokens", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d", w.Code)
		}
		var resp createAgentTokenResponse
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.ExpiresAt == nil {
			t.Fatal("expected expires_at to be set")
		}
		cl, _ := store.GetClusterByName(context.Background(), "c1")
		stored, _ := store.GetAgentTokenByHash(context.Background(), hashAgentToken(testPepper, resp.Token))
		if stored.ExpiresAt == nil || stored.ClusterID != cl.ID {
			t.Error("stored token missing expiry or cluster binding")
		}
	})
}

func TestAdminHandler_ListAgentTokens(t *testing.T) {
	mkToken := func(t *testing.T, ah *AdminHandlers, cluster string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"cluster_name": cluster})
		w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
			"POST", "/api/v1/admin/agent-tokens", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("seed token: want 201, got %d", w.Code)
		}
	}

	t.Run("filter by cluster", func(t *testing.T) {
		ah, _ := newTestAdminHandlers(t)
		mkToken(t, ah, "alpha")
		mkToken(t, ah, "alpha")
		mkToken(t, ah, "beta")

		w := doRequest(t, "GET", "/api/v1/admin/agent-tokens", ah.HandleListAgentTokens,
			"GET", "/api/v1/admin/agent-tokens?cluster=alpha", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var resp struct {
			Tokens []agentTokenResponse `json:"tokens"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Tokens) != 2 {
			t.Fatalf("want 2 tokens for alpha, got %d", len(resp.Tokens))
		}
		for _, tok := range resp.Tokens {
			if tok.ClusterName != "alpha" {
				t.Errorf("want cluster alpha, got %q", tok.ClusterName)
			}
		}
	})

	t.Run("list all clusters", func(t *testing.T) {
		ah, _ := newTestAdminHandlers(t)
		mkToken(t, ah, "alpha")
		mkToken(t, ah, "beta")
		mkToken(t, ah, "beta")

		w := doRequest(t, "GET", "/api/v1/admin/agent-tokens", ah.HandleListAgentTokens,
			"GET", "/api/v1/admin/agent-tokens", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", w.Code)
		}
		var resp struct {
			Tokens []agentTokenResponse `json:"tokens"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Tokens) != 3 {
			t.Fatalf("want 3 tokens total, got %d", len(resp.Tokens))
		}
	})
}

func TestAdminHandler_CreateUser(t *testing.T) {
	t.Run("valid request creates active user with hashed password", func(t *testing.T) {
		ah, store := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{
			"email": "new@example.com", "name": "New User", "password": "s3cret-pass",
		})
		w := doRequest(t, "POST", "/api/v1/admin/users", ah.HandleCreateUser,
			"POST", "/api/v1/admin/users", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
		}

		var resp User
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.ID == "" || !resp.IsActive {
			t.Errorf("expected active user with id, got %+v", resp)
		}
		if resp.PasswordHash != "" {
			t.Error("response must not leak password hash")
		}

		stored, _ := store.GetUserByEmail(context.Background(), "new@example.com")
		if stored == nil || !auth.CheckPassword("s3cret-pass", stored.PasswordHash) {
			t.Error("user not persisted with a valid bcrypt hash")
		}
	})

	t.Run("duplicate email is rejected", func(t *testing.T) {
		ah, _ := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{"email": "dup@example.com", "name": "A", "password": "pw12345678"})
		if w := doRequest(t, "POST", "/api/v1/admin/users", ah.HandleCreateUser, "POST", "/api/v1/admin/users", body); w.Code != http.StatusCreated {
			t.Fatalf("seed: want 201, got %d", w.Code)
		}
		w := doRequest(t, "POST", "/api/v1/admin/users", ah.HandleCreateUser, "POST", "/api/v1/admin/users", body)
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409 for duplicate, got %d", w.Code)
		}
	})

	t.Run("missing fields rejected", func(t *testing.T) {
		ah, _ := newTestAdminHandlers(t)
		body, _ := json.Marshal(map[string]any{"email": "x@example.com"})
		w := doRequest(t, "POST", "/api/v1/admin/users", ah.HandleCreateUser, "POST", "/api/v1/admin/users", body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", w.Code)
		}
	})
}

func TestAdminHandler_ListUsers(t *testing.T) {
	ah, store := newTestAdminHandlers(t)
	for _, e := range []string{"a@x.com", "b@x.com"} {
		store.CreateUser(context.Background(), &User{Email: e, Name: e, PasswordHash: "h", IsActive: true})
	}
	w := doRequest(t, "GET", "/api/v1/admin/users", ah.HandleListUsers, "GET", "/api/v1/admin/users", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Users []User `json:"users"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Users) != 2 {
		t.Fatalf("want 2 users, got %d", len(resp.Users))
	}
	for _, u := range resp.Users {
		if u.PasswordHash != "" {
			t.Error("list must not leak password hashes")
		}
	}
}

func TestAdminHandler_UpdateUser(t *testing.T) {
	ah, store := newTestAdminHandlers(t)
	u := &User{Email: "u@x.com", Name: "Old", PasswordHash: "h", IsActive: true}
	store.CreateUser(context.Background(), u)

	t.Run("updates name, active, password", func(t *testing.T) {
		disabled := false
		body, _ := json.Marshal(updateUserRequest{
			Name: ptr("New Name"), IsActive: &disabled, Password: ptr("brand-new-pass"),
		})
		w := doRequest(t, "PUT", "/api/v1/admin/users/:id", ah.HandleUpdateUser, "PUT", "/api/v1/admin/users/"+u.ID, body)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
		got, _ := store.GetUserByID(context.Background(), u.ID)
		if got.Name != "New Name" || got.IsActive {
			t.Errorf("name/active not updated: %+v", got)
		}
		if !auth.CheckPassword("brand-new-pass", got.PasswordHash) {
			t.Error("password not updated")
		}
	})

	t.Run("not found", func(t *testing.T) {
		body, _ := json.Marshal(updateUserRequest{Name: ptr("x")})
		w := doRequest(t, "PUT", "/api/v1/admin/users/:id", ah.HandleUpdateUser, "PUT", "/api/v1/admin/users/nope", body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", w.Code)
		}
	})
}

func TestAdminHandler_DeleteUser(t *testing.T) {
	ah, store := newTestAdminHandlers(t)
	u := &User{Email: "d@x.com", Name: "D", PasswordHash: "h", IsActive: true}
	store.CreateUser(context.Background(), u)

	w := doRequest(t, "DELETE", "/api/v1/admin/users/:id", ah.HandleDeleteUser, "DELETE", "/api/v1/admin/users/"+u.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got, _ := store.GetUserByID(context.Background(), u.ID); got != nil {
		t.Error("user should be deleted")
	}
}

func ptr[T any](v T) *T { return &v }

func TestAdminRoutes_RequireAdminRole(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	srv := NewHTTPServer(NewAgentStore(), NewCommandQueue(),
		NewAuthHandlers(store, jm, time.Hour), NewAdminHandlers(store, testPepper), nil, nil, nil, jm)

	tokenFor := func(isAdmin bool) string {
		tok, err := jm.GenerateAccessToken(&auth.UserClaims{
			UserID: "u1", Email: "u@example.com", IsAdmin: isAdmin,
		})
		if err != nil {
			t.Fatalf("generate token: %v", err)
		}
		return tok
	}

	tests := []struct {
		name     string
		auth     string
		wantCode int
	}{
		{"no token", "", http.StatusUnauthorized},
		{"non-admin", "Bearer " + tokenFor(false), http.StatusForbidden},
		{"admin", "Bearer " + tokenFor(true), http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"cluster_name": "c1"})
			req, _ := http.NewRequest("POST", "/api/v1/admin/agent-tokens", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			if w.Code != tt.wantCode {
				t.Fatalf("want %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminHandler_RevokeAgentToken(t *testing.T) {
	ah, store := newTestAdminHandlers(t)
	body, _ := json.Marshal(map[string]any{"cluster_name": "c1"})
	w := doRequest(t, "POST", "/api/v1/admin/agent-tokens", ah.HandleCreateAgentToken,
		"POST", "/api/v1/admin/agent-tokens", body)
	var created createAgentTokenResponse
	json.Unmarshal(w.Body.Bytes(), &created)

	rw := doRequest(t, "DELETE", "/api/v1/admin/agent-tokens/:id", ah.HandleRevokeAgentToken,
		"DELETE", "/api/v1/admin/agent-tokens/"+created.ID, nil)
	if rw.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rw.Code, rw.Body.String())
	}

	stored, _ := store.GetAgentTokenByHash(context.Background(), hashAgentToken(testPepper, created.Token))
	if stored == nil || !stored.IsRevoked {
		t.Fatalf("expected token to be revoked, got %+v", stored)
	}
}

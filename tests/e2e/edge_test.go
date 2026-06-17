//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// rbacPolicyPath points at the RBAC policy file the running central watches.
// The e2e harness writes it to tests/e2e/config/rbac.yaml; tests run from
// tests/e2e, so the default is relative to that.
var rbacPolicyPath = flag.String("rbac-policy", "config/rbac.yaml", "path to the central RBAC policy file")

// centralPidfile is where the harness records central's PID, used to trigger a
// SIGHUP policy reload (works regardless of whether the filesystem delivers
// inotify events).
var centralPidfile = flag.String("central-pidfile", "logs/central.pid", "path to central's pid file")

// signalCentralReload sends SIGHUP to the running central to reload its policy.
func signalCentralReload() error {
	data, err := os.ReadFile(*centralPidfile)
	if err != nil {
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGHUP)
}

const edgePassword = "edge-password-123"

func execURL() string {
	return fmt.Sprintf("%s/api/v1/clusters/%s/exec", *centralURL, *clusterName)
}

// doJSON issues a JSON request with an optional bearer token and returns the
// body and status code.
func doJSON(t *testing.T, method, url, token string, payload any) ([]byte, int) {
	t.Helper()
	return httpPostAuthMethod(t, method, url, token, payload)
}

// httpPostAuthMethod is httpPostAuth generalised to any method.
func httpPostAuthMethod(t *testing.T, method, url, token string, payload any) ([]byte, int) {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	client := &http.Client{Timeout: 35 * time.Second}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return out, resp.StatusCode
}

// ensureUser creates a user via the admin API, tolerating "already exists".
func ensureUser(t *testing.T, email string) {
	t.Helper()
	_, code := httpPostAuth(t, fmt.Sprintf("%s/api/v1/admin/users", *centralURL), authToken,
		map[string]any{"email": email, "name": email, "password": edgePassword})
	if code != http.StatusCreated && code != http.StatusConflict {
		t.Fatalf("ensure user %s: unexpected status %d", email, code)
	}
}

// loginTokens logs in and returns both the access and refresh tokens.
func loginTokens(t *testing.T, email, password string) (access, refresh string) {
	t.Helper()
	body, code := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
		map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, code, body)
	}
	var lr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return lr.AccessToken, lr.RefreshToken
}

// pollExec retries an exec until want(code) is true or the timeout elapses,
// used to wait for hot-reloaded policy changes to take effect.
func pollExec(t *testing.T, token string, command []string, want func(int) bool, timeout time.Duration) (int, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for {
		_, code := httpPostAuth(t, execURL(), token, map[string]any{"command": command})
		last = code
		if want(code) {
			return last, true
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// --- Authentication edge cases ---

func TestAuthRejectsBadCredentials(t *testing.T) {
	cases := []struct {
		name, email, password string
	}{
		{"wrong password", "admin@e2e.test", "wrong-password"},
		{"unknown user", "nobody@e2e.test", "whatever"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, code := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
				map[string]string{"email": tc.email, "password": tc.password})
			if code != http.StatusUnauthorized {
				t.Errorf("want 401, got %d", code)
			}
		})
	}
}

func TestAuthRejectsMissingOrBadToken(t *testing.T) {
	url := fmt.Sprintf("%s/api/v1/clusters", *centralURL)
	t.Run("no token", func(t *testing.T) {
		_, code := httpGet(t, url)
		if code != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", code)
		}
	})
	t.Run("garbage token", func(t *testing.T) {
		_, code := httpGetAuth(t, url, "not-a-real-jwt")
		if code != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", code)
		}
	})
	t.Run("malformed authorization header", func(t *testing.T) {
		client := &http.Client{Timeout: 10 * time.Second}
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", authToken) // missing "Bearer " prefix
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", resp.StatusCode)
		}
	})
}

func TestDisabledUserCannotLogin(t *testing.T) {
	email := "edge-disabled@e2e.test"
	body, code := httpPostAuth(t, fmt.Sprintf("%s/api/v1/admin/users", *centralURL), authToken,
		map[string]any{"email": email, "name": "Disabled", "password": edgePassword})
	if code != http.StatusCreated && code != http.StatusConflict {
		t.Fatalf("create user: %d", code)
	}
	if code == http.StatusConflict {
		t.Skip("user already exists from a prior run; skipping to stay idempotent")
	}
	var u struct {
		ID string `json:"id"`
	}
	json.Unmarshal(body, &u)

	// Logging in works while active.
	if _, c := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
		map[string]string{"email": email, "password": edgePassword}); c != http.StatusOK {
		t.Fatalf("active login: want 200, got %d", c)
	}

	// Disable, then login must be forbidden.
	if _, c := doJSON(t, http.MethodPut, fmt.Sprintf("%s/api/v1/admin/users/%s", *centralURL, u.ID), authToken,
		map[string]any{"is_active": false}); c != http.StatusOK {
		t.Fatalf("disable user: want 200, got %d", c)
	}
	if _, c := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
		map[string]string{"email": email, "password": edgePassword}); c != http.StatusForbidden {
		t.Errorf("disabled login: want 403, got %d", c)
	}
}

func TestRefreshTokenRotation(t *testing.T) {
	email := "edge-refresh@e2e.test"
	ensureUser(t, email)
	_, refresh := loginTokens(t, email, edgePassword)

	// First refresh succeeds.
	body, code := httpPostAuth(t, fmt.Sprintf("%s/auth/refresh", *centralURL), "",
		map[string]string{"refresh_token": refresh})
	if code != http.StatusOK {
		t.Fatalf("first refresh: want 200, got %d %s", code, body)
	}

	// Reusing the now-rotated refresh token must fail.
	_, code = httpPostAuth(t, fmt.Sprintf("%s/auth/refresh", *centralURL), "",
		map[string]string{"refresh_token": refresh})
	if code != http.StatusUnauthorized {
		t.Errorf("reused refresh token: want 401, got %d", code)
	}
}

func TestLogoutInvalidatesRefreshToken(t *testing.T) {
	email := "edge-logout@e2e.test"
	ensureUser(t, email)
	access, refresh := loginTokens(t, email, edgePassword)

	// Logout (requires a valid access token; refresh token in the body).
	if _, code := httpPostAuth(t, fmt.Sprintf("%s/api/v1/auth/logout", *centralURL), access,
		map[string]string{"refresh_token": refresh}); code != http.StatusOK {
		t.Fatalf("logout: want 200, got %d", code)
	}

	// The refresh token must no longer be usable after logout.
	_, code := httpPostAuth(t, fmt.Sprintf("%s/auth/refresh", *centralURL), "",
		map[string]string{"refresh_token": refresh})
	if code != http.StatusUnauthorized {
		t.Errorf("refresh after logout: want 401, got %d", code)
	}
}

func TestChangePasswordFlow(t *testing.T) {
	email := "edge-chpw@e2e.test"
	ensureUser(t, email)
	access, _ := loginTokens(t, email, edgePassword)

	newPassword := "edge-new-password-456"
	if _, code := httpPostAuth(t, fmt.Sprintf("%s/api/v1/auth/change-password", *centralURL), access,
		map[string]string{"current_password": edgePassword, "new_password": newPassword}); code != http.StatusOK {
		t.Fatalf("change password: want 200, got %d", code)
	}

	// Old password rejected, new password accepted.
	if _, code := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
		map[string]string{"email": email, "password": edgePassword}); code != http.StatusUnauthorized {
		t.Errorf("login with old password: want 401, got %d", code)
	}
	if _, code := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
		map[string]string{"email": email, "password": newPassword}); code != http.StatusOK {
		t.Errorf("login with new password: want 200, got %d", code)
	}
}

// --- RBAC edge cases ---

func TestRBACViewerVerbsEnforced(t *testing.T) {
	email := "edge-verbs@e2e.test"
	ensureUser(t, email)
	viewer := loginAs(t, email, edgePassword)

	allowed := [][]string{
		{"get", "pods", "-n", "kube-system"},
		{"describe", "nodes"},
	}
	for _, cmd := range allowed {
		if _, code := httpPostAuth(t, execURL(), viewer, map[string]any{"command": cmd}); code != http.StatusOK {
			t.Errorf("viewer %v: want 200, got %d", cmd, code)
		}
	}

	denied := [][]string{
		{"delete", "pods", "x", "-n", "default"},
		{"apply", "-f", "/dev/null"},
		{"exec", "somepod", "--", "sh"},
	}
	for _, cmd := range denied {
		if _, code := httpPostAuth(t, execURL(), viewer, map[string]any{"command": cmd}); code != http.StatusForbidden {
			t.Errorf("viewer %v: want 403, got %d", cmd, code)
		}
	}
}

func TestRBACPolicyHotReload(t *testing.T) {
	original, err := os.ReadFile(*rbacPolicyPath)
	if err != nil {
		t.Fatalf("read policy %s: %v (set -rbac-policy)", *rbacPolicyPath, err)
	}
	email := "edge-reload@e2e.test"
	ensureUser(t, email)
	viewer := loginAs(t, email, edgePassword)
	writeCmd := []string{"delete", "pods", "edge-nonexistent", "-n", "default"}

	// Restore the original policy and wait for the agent to lose write access.
	defer func() {
		os.WriteFile(*rbacPolicyPath, original, 0o644)
		signalCentralReload()
		pollExec(t, viewer, writeCmd, func(c int) bool { return c == http.StatusForbidden }, 8*time.Second)
	}()

	// Precondition: writes denied under the default viewer role.
	if _, code := httpPostAuth(t, execURL(), viewer, map[string]any{"command": writeCmd}); code != http.StatusForbidden {
		t.Fatalf("precondition: want 403, got %d", code)
	}

	// Grant this user admin by editing the policy file in place.
	granted := fmt.Sprintf(`default: viewer
roles:
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get", "list", "watch", "describe", "logs"]
bindings:
  - subject: admin@e2e.test
    roles: ["admin"]
  - subject: %s
    roles: ["admin"]
`, email)
	if err := os.WriteFile(*rbacPolicyPath, []byte(granted), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	// Trigger a reload via SIGHUP (the file watcher also fires on filesystems
	// that deliver inotify events; SIGHUP makes this deterministic everywhere).
	if err := signalCentralReload(); err != nil {
		t.Fatalf("signal reload: %v", err)
	}

	// After reload the write should no longer be denied.
	if last, ok := pollExec(t, viewer, writeCmd, func(c int) bool { return c != http.StatusForbidden }, 8*time.Second); !ok {
		t.Errorf("policy reload did not grant access within timeout; last status=%d", last)
	}
}

// --- Admin authorization edge cases ---

func TestAdminEndpointsForbiddenForNonAdmin(t *testing.T) {
	email := "edge-nonadmin@e2e.test"
	ensureUser(t, email)
	viewer := loginAs(t, email, edgePassword)

	cases := []struct {
		name, method, path string
		payload            any
	}{
		{"list users", http.MethodGet, "/api/v1/admin/users", nil},
		{"audit", http.MethodGet, "/api/v1/admin/audit", nil},
		{"create agent token", http.MethodPost, "/api/v1/admin/agent-tokens", map[string]any{"cluster_name": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, code := doJSON(t, tc.method, *centralURL+tc.path, viewer, tc.payload)
			if code != http.StatusForbidden {
				t.Errorf("non-admin %s: want 403, got %d", tc.path, code)
			}
		})
	}
}

func TestUserManagementEdgeCases(t *testing.T) {
	usersURL := fmt.Sprintf("%s/api/v1/admin/users", *centralURL)

	t.Run("duplicate email rejected", func(t *testing.T) {
		email := "edge-dup@e2e.test"
		ensureUser(t, email)
		_, code := httpPostAuth(t, usersURL, authToken,
			map[string]any{"email": email, "name": "Dup", "password": edgePassword})
		if code != http.StatusConflict {
			t.Errorf("duplicate: want 409, got %d", code)
		}
	})

	t.Run("missing fields rejected", func(t *testing.T) {
		_, code := httpPostAuth(t, usersURL, authToken, map[string]any{"email": "x@e2e.test"})
		if code != http.StatusBadRequest {
			t.Errorf("missing fields: want 400, got %d", code)
		}
	})

	t.Run("update nonexistent user", func(t *testing.T) {
		_, code := doJSON(t, http.MethodPut, usersURL+"/does-not-exist", authToken,
			map[string]any{"name": "x"})
		if code != http.StatusNotFound {
			t.Errorf("update missing: want 404, got %d", code)
		}
	})

	t.Run("delete then login fails", func(t *testing.T) {
		email := "edge-delete@e2e.test"
		body, code := httpPostAuth(t, usersURL, authToken,
			map[string]any{"email": email, "name": "Del", "password": edgePassword})
		if code == http.StatusConflict {
			t.Skip("user exists from prior run")
		}
		if code != http.StatusCreated {
			t.Fatalf("create: %d", code)
		}
		var u struct {
			ID string `json:"id"`
		}
		json.Unmarshal(body, &u)

		if _, c := doJSON(t, http.MethodDelete, usersURL+"/"+u.ID, authToken, nil); c != http.StatusOK {
			t.Fatalf("delete: want 200, got %d", c)
		}
		if _, c := httpPostAuth(t, fmt.Sprintf("%s/auth/login", *centralURL), "",
			map[string]string{"email": email, "password": edgePassword}); c != http.StatusUnauthorized {
			t.Errorf("login after delete: want 401, got %d", c)
		}
	})
}

// --- Agent token edge cases ---

func TestAgentTokenLifecycle(t *testing.T) {
	tokensURL := fmt.Sprintf("%s/api/v1/admin/agent-tokens", *centralURL)
	cluster := "edge-token-cluster"

	body, code := httpPostAuth(t, tokensURL, authToken, map[string]any{
		"cluster_name": cluster, "description": "edge test",
	})
	if code != http.StatusCreated {
		t.Fatalf("create token: want 201, got %d %s", code, body)
	}
	var created struct {
		ID          string `json:"id"`
		Token       string `json:"token"`
		TokenPrefix string `json:"token_prefix"`
	}
	json.Unmarshal(body, &created)
	if created.Token == "" || created.ID == "" || created.TokenPrefix == "" {
		t.Fatalf("create response missing fields: %s", body)
	}

	// List must show the token's metadata but never the secret.
	listBody, code := httpGetAuth(t, tokensURL+"?cluster="+cluster, authToken)
	if code != http.StatusOK {
		t.Fatalf("list tokens: want 200, got %d", code)
	}
	if !strings.Contains(string(listBody), created.TokenPrefix) {
		t.Errorf("list missing token prefix")
	}
	if strings.Contains(string(listBody), created.Token) {
		t.Errorf("list leaked the token secret")
	}

	// Revoke, then confirm it is marked revoked.
	if _, c := doJSON(t, http.MethodDelete, tokensURL+"/"+created.ID, authToken, nil); c != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d", c)
	}
	listBody, _ = httpGetAuth(t, tokensURL+"?cluster="+cluster, authToken)
	var listResp struct {
		Tokens []struct {
			ID        string `json:"id"`
			IsRevoked bool   `json:"is_revoked"`
		} `json:"tokens"`
	}
	json.Unmarshal(listBody, &listResp)
	found := false
	for _, tok := range listResp.Tokens {
		if tok.ID == created.ID {
			found = true
			if !tok.IsRevoked {
				t.Errorf("token not marked revoked")
			}
		}
	}
	if !found {
		t.Errorf("revoked token missing from list")
	}
}

// --- Exec edge cases ---

func TestExecEdgeCases(t *testing.T) {
	t.Run("nonexistent cluster", func(t *testing.T) {
		url := fmt.Sprintf("%s/api/v1/clusters/no-such-cluster/exec", *centralURL)
		_, code := httpPostAuth(t, url, authToken, map[string]any{"command": []string{"get", "pods"}})
		if code != http.StatusNotFound {
			t.Errorf("want 404, got %d", code)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		_, code := httpPostAuth(t, execURL(), authToken, map[string]any{"command": []string{}})
		if code != http.StatusBadRequest {
			t.Errorf("want 400, got %d", code)
		}
	})
}

// --- Audit filtering edge cases ---

func TestAuditFilterByStatus(t *testing.T) {
	// Generate one denied entry as a viewer.
	email := "edge-audit@e2e.test"
	ensureUser(t, email)
	viewer := loginAs(t, email, edgePassword)
	if _, code := httpPostAuth(t, execURL(), viewer,
		map[string]any{"command": []string{"delete", "pods", "x", "-n", "default"}}); code != http.StatusForbidden {
		t.Fatalf("seed denied entry: want 403, got %d", code)
	}

	body, code := httpGetAuth(t, fmt.Sprintf("%s/api/v1/admin/audit?status=denied", *centralURL), authToken)
	if code != http.StatusOK {
		t.Fatalf("audit query: want 200, got %d", code)
	}
	var resp struct {
		Logs []struct {
			Status string `json:"status"`
		} `json:"logs"`
		Total int `json:"total"`
	}
	json.Unmarshal(body, &resp)
	if resp.Total == 0 {
		t.Fatal("expected at least one denied entry")
	}
	for _, l := range resp.Logs {
		if l.Status != "denied" {
			t.Errorf("status filter leaked %q", l.Status)
		}
	}
}

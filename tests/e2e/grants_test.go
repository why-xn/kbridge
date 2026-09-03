//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// approvalPolicy guards writes behind an approved grant. Extra subjects are
// bound to the admin role so their commands reach the guardrail rather than
// being denied outright by the role rules.
func approvalPolicy(subjects ...string) string {
	var bindings strings.Builder
	for _, s := range subjects {
		fmt.Fprintf(&bindings, "  - subject: %s\n    roles: [\"admin\"]\n", s)
	}
	return `default: viewer
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
` + bindings.String() + `guardrails:
  - name: e2e-writes-need-approval
    match:
      verbs: ["delete", "apply"]
      args_not: ["--dry-run*"]
    action: require-approval
    message: "e2e approval required"
`
}

// grantAPI is the subset of a grant these tests assert on.
type grantAPI struct {
	ID            string     `json:"id"`
	Subject       string     `json:"subject"`
	ClusterName   string     `json:"cluster_name"`
	Namespace     string     `json:"namespace"`
	Status        string     `json:"status"`
	DisplayStatus string     `json:"display_status"`
	Reason        string     `json:"reason"`
	Duration      string     `json:"duration"`
	DecidedBy     string     `json:"decided_by"`
	ExpiresAt     *time.Time `json:"expires_at"`
}

// grantURL builds a grants endpoint path.
func grantURL(suffix string) string {
	return fmt.Sprintf("%s/api/v1%s", *controlPlaneURL, suffix)
}

// requestGrantAPI asks for access over HTTP and returns the created grant.
func requestGrantAPI(t *testing.T, token, cluster, namespace, duration, reason string) grantAPI {
	t.Helper()
	payload := map[string]any{"cluster": cluster, "reason": reason}
	if namespace != "" {
		payload["namespace"] = namespace
	}
	if duration != "" {
		payload["duration"] = duration
	}
	raw, code := httpPostAuth(t, grantURL("/grants"), token, payload)
	if code != http.StatusCreated {
		t.Fatalf("request grant: status %d, body %s", code, string(raw))
	}
	var g grantAPI
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode grant: %v", err)
	}
	return g
}

// decideGrantAPI approves, denies, or revokes a grant as the e2e admin.
func decideGrantAPI(t *testing.T, id, action string, payload map[string]any) (grantAPI, int) {
	t.Helper()
	method, url := http.MethodPost, grantURL("/admin/grants/"+id+"/"+action)
	if action == "revoke" {
		method, url = http.MethodDelete, grantURL("/admin/grants/"+id)
	}
	raw, code := httpPostAuthMethod(t, method, url, authToken, payload)
	var g grantAPI
	_ = json.Unmarshal(raw, &g)
	return g, code
}

// installApprovalPolicy swaps in the approval policy for one test, using a
// write as the probe for "is it live yet?".
func installApprovalPolicy(t *testing.T, subjects ...string) {
	t.Helper()
	probe := []string{"delete", "pod", "e2e-nonexistent", "-n", "default"}
	installPolicy(t, approvalPolicy(subjects...), probe,
		func(c int) bool { return c == http.StatusForbidden })
}

// jitRequester seeds a non-admin user and returns their email and token. Grants
// need two people: the harness admin approves, this user asks. Self-approval is
// refused by design, so a test that used one identity for both would be testing
// nothing.
func jitRequester(t *testing.T, email string) (string, string) {
	t.Helper()
	ensureUser(t, email)
	return email, loginAs(t, email, edgePassword)
}

// TestGrantsHTTPFlow walks the whole lifecycle over the API: a guarded command
// is refused, a request is made, approval admits it, and revocation stops it.
func TestGrantsHTTPFlow(t *testing.T) {
	email, userTok := jitRequester(t, "edge-jit-http@e2e.test")
	installApprovalPolicy(t, email)
	command := []string{"delete", "pod", "e2e-nonexistent", "-n", "default"}

	t.Run("guarded command is refused before any grant", func(t *testing.T) {
		body, code := execWithBody(t, userTok, map[string]any{"command": command})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["approval_required"] != true {
			t.Errorf("approval_required = %v, want true", body["approval_required"])
		}
		if body["guardrail"] != "e2e-writes-need-approval" {
			t.Errorf("guardrail = %v, want e2e-writes-need-approval", body["guardrail"])
		}
	})

	g := requestGrantAPI(t, userTok, *clusterName, "", "2h", "INC-4521 e2e approval flow")

	t.Run("a new request is pending with no expiry", func(t *testing.T) {
		if g.DisplayStatus != "pending" {
			t.Errorf("display_status = %q, want pending", g.DisplayStatus)
		}
		if g.ExpiresAt != nil {
			t.Error("a pending grant must not carry an expiry: the clock starts at approval")
		}
	})

	t.Run("pending grants nothing", func(t *testing.T) {
		_, code := execWithBody(t, userTok, map[string]any{"command": command})
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 while the request is pending", code)
		}
	})

	t.Run("approval admits the command", func(t *testing.T) {
		approved, code := decideGrantAPI(t, g.ID, "approve", map[string]any{"note": "e2e go ahead"})
		if code != http.StatusOK {
			t.Fatalf("approve: status %d", code)
		}
		if approved.DisplayStatus != "approved" || approved.ExpiresAt == nil {
			t.Fatalf("grant not activated: %+v", approved)
		}
		if approved.DecidedBy != "admin@e2e.test" {
			t.Errorf("decided_by = %q, want the approver", approved.DecidedBy)
		}

		_, code = execWithBody(t, userTok, map[string]any{"command": command})
		if code == http.StatusForbidden {
			t.Fatal("an approved grant should admit the command")
		}
	})

	t.Run("the command is audited against the grant", func(t *testing.T) {
		entries := auditQuery(t, "per_page=100")
		if !hasAudit(entries, func(e auditEntry) bool {
			return e.GrantID == g.ID && strings.Contains(e.Command, "delete pod")
		}) {
			t.Errorf("no audit entry ties the command to grant %s", g.ID)
		}
		if !hasAudit(entries, func(e auditEntry) bool {
			return e.GrantID == g.ID && e.Status == "grant-approved"
		}) {
			t.Error("the approval itself should be audited")
		}
	})

	t.Run("self-approval is refused", func(t *testing.T) {
		// The requester is an ordinary user, so they cannot reach the admin
		// endpoint at all; approving one's own request is separately refused
		// for an admin, which the unit tests cover.
		_, code := httpPostAuthMethod(t, http.MethodPost,
			grantURL("/admin/grants/"+g.ID+"/approve"), userTok, nil)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: a requester must not decide their own grant", code)
		}
	})

	t.Run("revocation stops it again", func(t *testing.T) {
		if _, code := decideGrantAPI(t, g.ID, "revoke", nil); code != http.StatusOK {
			t.Fatalf("revoke: status %d", code)
		}
		_, code := execWithBody(t, userTok, map[string]any{"command": command})
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403 after revocation", code)
		}
	})
}

func TestGrantsAPIEdgeCases(t *testing.T) {
	email, userTok := jitRequester(t, "edge-jit-edge@e2e.test")
	installApprovalPolicy(t, email)

	t.Run("a short reason is refused", func(t *testing.T) {
		_, code := httpPostAuth(t, grantURL("/grants"), userTok,
			map[string]any{"cluster": *clusterName, "reason": "oops"})
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("a duration past the ceiling is refused", func(t *testing.T) {
		_, code := httpPostAuth(t, grantURL("/grants"), userTok,
			map[string]any{"cluster": *clusterName, "reason": "INC-4521 too long", "duration": "1000h"})
		if code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", code)
		}
	})

	t.Run("deciding an unknown grant is a 404", func(t *testing.T) {
		if _, code := decideGrantAPI(t, "no-such-grant", "approve", nil); code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})

	t.Run("a denied grant admits nothing and cannot be re-decided", func(t *testing.T) {
		g := requestGrantAPI(t, userTok, *clusterName, "", "1h", "INC-4521 will be denied")
		denied, code := decideGrantAPI(t, g.ID, "deny", map[string]any{"note": "use the runbook"})
		if code != http.StatusOK {
			t.Fatalf("deny: status %d", code)
		}
		if denied.DisplayStatus != "denied" {
			t.Errorf("display_status = %q, want denied", denied.DisplayStatus)
		}
		_, code = execWithBody(t, userTok, map[string]any{
			"command": []string{"delete", "pod", "e2e-nonexistent", "-n", "default"}})
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: a denied grant admits nothing", code)
		}
		if _, code := decideGrantAPI(t, g.ID, "approve", nil); code != http.StatusConflict {
			t.Errorf("status = %d, want 409 when re-deciding", code)
		}
	})

	t.Run("a non-admin cannot approve", func(t *testing.T) {
		g := requestGrantAPI(t, userTok, *clusterName, "", "1h", "INC-4521 by a plain user")
		raw, code := httpPostAuth(t, grantURL("/admin/grants/"+g.ID+"/approve"), userTok, nil)
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want 403: %s", code, string(raw))
		}
	})

	t.Run("a user sees only their own grants", func(t *testing.T) {
		email := "edge-grant-scope@e2e.test"
		ensureUser(t, email)
		userTok := loginAs(t, email, edgePassword)
		requestGrantAPI(t, userTok, *clusterName, "", "1h", "INC-4521 mine only")

		raw, code := doJSON(t, http.MethodGet, grantURL("/grants"), userTok, nil)
		if code != http.StatusOK {
			t.Fatalf("list grants: status %d", code)
		}
		var resp struct {
			Grants []grantAPI `json:"grants"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Grants) == 0 {
			t.Fatal("expected the user's own grant to be listed")
		}
		for _, g := range resp.Grants {
			if g.Subject != email {
				t.Errorf("listing leaked a grant belonging to %q", g.Subject)
			}
		}
	})
}

// cliHome writes a CLI config for one identity into a temp directory and
// returns an env map that points the kb binary at it. The harness config always
// holds the admin's token, so a test needing a second identity needs its own
// HOME.
func cliHome(t *testing.T, token string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".kbridge"), 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	cfg := fmt.Sprintf("control_plane_url: %q\ncurrent_cluster: %q\ntoken: %q\n",
		*controlPlaneURL, *clusterName, token)
	if err := os.WriteFile(filepath.Join(dir, ".kbridge", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return map[string]string{"HOME": dir}
}

// TestGrantsCLI drives the kb binary through the whole two-person flow: an
// ordinary user asks, the admin decides, and the user's command is admitted in
// between. This is where users actually meet the feature.
func TestGrantsCLI(t *testing.T) {
	email, userTok := jitRequester(t, "edge-jit-cli@e2e.test")
	installApprovalPolicy(t, email)
	userEnv := cliHome(t, userTok)

	// The admin drives through the harness config; make sure it targets the
	// e2e cluster.
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: exit %d, stderr=%s", code, stderr)
	}

	deletePod := []string{"delete", "pod", "e2e-nonexistent"}

	t.Run("a guarded command points the user at kb request", func(t *testing.T) {
		_, stderr, code := runCLIWithEnv(t, userEnv, deletePod...)
		if code == 0 {
			t.Fatal("expected a non-zero exit without a grant")
		}
		for _, want := range []string{"e2e approval required", "kb request", "kb grants"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q: %s", want, stderr)
			}
		}
	})

	var id string
	t.Run("kb request creates a pending grant", func(t *testing.T) {
		stdout, stderr, code := runCLIWithEnv(t, userEnv, "request", *clusterName,
			"--duration", "2h", "--reason", "INC-4521 cli approval flow")
		if code != 0 {
			t.Fatalf("request: exit %d, stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, "pending") {
			t.Errorf("output should say the request is pending: %s", stdout)
		}
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(line, "Grant ID:") {
				id = strings.TrimSpace(strings.TrimPrefix(line, "Grant ID:"))
			}
		}
		if id == "" {
			t.Fatalf("could not parse a grant ID from: %s", stdout)
		}
	})

	t.Run("kb grants lists it for the requester", func(t *testing.T) {
		stdout, stderr, code := runCLIWithEnv(t, userEnv, "grants")
		if code != 0 {
			t.Fatalf("grants: exit %d, stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, id) || !strings.Contains(stdout, "pending") {
			t.Errorf("listing missing the pending grant %s: %s", id, stdout)
		}
	})

	t.Run("kb request rejects a short reason", func(t *testing.T) {
		_, _, code := runCLIWithEnv(t, userEnv, "request", *clusterName, "--reason", "oops")
		if code == 0 {
			t.Error("expected a non-zero exit for a too-short reason")
		}
	})

	t.Run("the requester cannot decide their own grant", func(t *testing.T) {
		_, stderr, code := runCLIWithEnv(t, userEnv, "admin", "grants", "approve", id)
		if code == 0 {
			t.Fatal("a non-admin must not be able to approve")
		}
		if !strings.Contains(stderr, "admin") && !strings.Contains(stderr, "denied") {
			t.Errorf("stderr should explain the refusal: %s", stderr)
		}
	})

	t.Run("the admin sees it pending", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, "admin", "grants", "list", "--status", "pending")
		if code != 0 {
			t.Fatalf("admin grants list: exit %d, stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, id) || !strings.Contains(stdout, email) {
			t.Errorf("pending listing missing grant %s for %s: %s", id, email, stdout)
		}
	})

	t.Run("approval admits the user's command", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, "admin", "grants", "approve", id, "--note", "e2e go ahead")
		if code != 0 {
			t.Fatalf("approve: exit %d, stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, "approved") {
			t.Errorf("output should confirm the approval: %s", stdout)
		}

		// The pod does not exist, so kubectl reports NotFound. Reaching kubectl
		// at all is the proof the guardrail let the command through.
		out, errOut, _ := runCLIWithEnv(t, userEnv, deletePod...)
		combined := out + errOut
		if strings.Contains(combined, "approval required") {
			t.Errorf("command still refused after approval: %s", combined)
		}
		if !strings.Contains(combined, "NotFound") && !strings.Contains(combined, "not found") {
			t.Errorf("expected kubectl to report the missing pod, got: %s", combined)
		}
	})

	t.Run("revocation stops it", func(t *testing.T) {
		if _, stderr, code := runCLI(t, "admin", "grants", "revoke", id); code != 0 {
			t.Fatalf("revoke: exit %d, stderr=%s", code, stderr)
		}
		_, stderr, code := runCLIWithEnv(t, userEnv, deletePod...)
		if code == 0 {
			t.Fatal("expected a non-zero exit after revocation")
		}
		if !strings.Contains(stderr, "approval required") {
			t.Errorf("stderr should say approval is required again: %s", stderr)
		}
	})

	t.Run("revoking twice is refused", func(t *testing.T) {
		if _, _, code := runCLI(t, "admin", "grants", "revoke", id); code == 0 {
			t.Error("expected a non-zero exit when revoking an already revoked grant")
		}
	})
}

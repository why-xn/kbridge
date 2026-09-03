//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// guardrailPolicy grants the given subject full access, then guards it. Cluster
// patterns are "*" because the e2e cluster name comes from a flag. The e2e admin
// is bound too, since runCLI drives the CLI with the admin token.
func guardrailPolicy(subjects ...string) string {
	var bindings strings.Builder
	for _, s := range subjects {
		fmt.Fprintf(&bindings, "  - subject: %s\n    roles: [\"admin\"]\n", s)
	}
	return fmt.Sprintf(`default: viewer
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
%s
guardrails:
  - name: e2e-no-namespace-delete
    match:
      resources: ["namespaces", "ns"]
      verbs: ["delete"]
    action: deny
    message: "e2e guardrail blocked this"

  - name: e2e-no-bulk-delete
    match:
      verbs: ["delete"]
      args: ["--all"]
    action: deny
    message: "e2e bulk delete blocked"

  - name: e2e-delete-needs-reason
    match:
      verbs: ["delete"]
      args_not: ["--dry-run*"]
    action: require-reason
`, bindings.String())
}

// installPolicy writes a policy file, triggers a reload, and registers a cleanup
// that restores the original. It waits for each swap to be observable so the
// test never races the control plane's reload.
func installPolicy(t *testing.T, doc string, probe []string, live func(int) bool) {
	t.Helper()
	original, err := os.ReadFile(*rbacPolicyPath)
	if err != nil {
		t.Fatalf("read policy %s: %v (set -rbac-policy)", *rbacPolicyPath, err)
	}

	t.Cleanup(func() {
		os.WriteFile(*rbacPolicyPath, original, 0o644)
		signalControlPlaneReload()
		// Wait for the original policy to be back, so later tests are not
		// evaluated against this one.
		pollExec(t, authToken, probe, func(c int) bool { return !live(c) }, 10*time.Second)
	})

	if err := os.WriteFile(*rbacPolicyPath, []byte(doc), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := signalControlPlaneReload(); err != nil {
		t.Fatalf("signal reload: %v", err)
	}
	if last, ok := pollExec(t, authToken, probe, live, 10*time.Second); !ok {
		t.Fatalf("policy reload did not take effect within timeout; last status=%d", last)
	}
}

// execWithBody posts an exec request and returns the decoded body and status.
func execWithBody(t *testing.T, token string, payload map[string]any) (map[string]any, int) {
	t.Helper()
	raw, code := httpPostAuth(t, execURL(), token, payload)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return body, code
}

// nsDelete is blocked by the deny guardrail, which makes it a good probe for
// "is the guardrail policy live yet?".
var nsDelete = []string{"delete", "ns", "e2e-nonexistent"}

// installGuardrails puts the guardrail policy in place for one test.
func installGuardrails(t *testing.T) {
	t.Helper()
	installPolicy(t, guardrailPolicy(), nsDelete,
		func(c int) bool { return c == http.StatusForbidden })
}

// TestGuardrailsEnforced checks the HTTP surface: a deny blocks, a
// require-reason guardrail refuses a bare command and admits one carrying a
// reason, arg matching fires, and both outcomes reach the audit log.
func TestGuardrailsEnforced(t *testing.T) {
	installGuardrails(t)
	podDelete := []string{"delete", "pod", "e2e-nonexistent", "-n", "default"}

	t.Run("deny guardrail blocks", func(t *testing.T) {
		body, code := execWithBody(t, authToken, map[string]any{"command": nsDelete})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["guardrail"] != "e2e-no-namespace-delete" {
			t.Errorf("guardrail = %v, want e2e-no-namespace-delete", body["guardrail"])
		}
		if body["error"] != "e2e guardrail blocked this" {
			t.Errorf("error = %v, want the guardrail's message", body["error"])
		}
		if _, ok := body["reason_required"]; ok {
			t.Error("a deny guardrail must not suggest that a reason would help")
		}
	})

	t.Run("arg matching blocks a bulk delete", func(t *testing.T) {
		body, code := execWithBody(t, authToken, map[string]any{
			"command": []string{"delete", "pods", "--all", "-n", "default"},
			"reason":  "INC-4521 this reason must not help",
		})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["guardrail"] != "e2e-no-bulk-delete" {
			t.Errorf("guardrail = %v, want e2e-no-bulk-delete", body["guardrail"])
		}
	})

	t.Run("require-reason refuses without one", func(t *testing.T) {
		body, code := execWithBody(t, authToken, map[string]any{"command": podDelete})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["reason_required"] != true {
			t.Errorf("reason_required = %v, want true", body["reason_required"])
		}
		if body["guardrail"] != "e2e-delete-needs-reason" {
			t.Errorf("guardrail = %v, want e2e-delete-needs-reason", body["guardrail"])
		}
	})

	t.Run("a too-short reason does not satisfy it", func(t *testing.T) {
		body, code := execWithBody(t, authToken, map[string]any{
			"command": podDelete,
			"reason":  "oops",
		})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["reason_required"] != true {
			t.Errorf("reason_required = %v, want true", body["reason_required"])
		}
	})

	t.Run("require-reason admits with one", func(t *testing.T) {
		_, code := execWithBody(t, authToken, map[string]any{
			"command": podDelete,
			"reason":  "INC-4521 e2e check",
		})
		if code == http.StatusForbidden {
			t.Fatalf("a command carrying a reason was refused with 403")
		}
	})

	t.Run("args_not exempts a dry run", func(t *testing.T) {
		_, code := execWithBody(t, authToken, map[string]any{
			"command": []string{"delete", "pod", "e2e-nonexistent", "-n", "default", "--dry-run=client"},
		})
		if code == http.StatusForbidden {
			t.Fatalf("a dry run should skip the require-reason guardrail, got 403")
		}
	})

	t.Run("blocked and reasoned commands are audited", func(t *testing.T) {
		blocked := auditQuery(t, "status=blocked&per_page=100")
		if !hasAudit(blocked, func(e auditEntry) bool {
			return e.Status == "blocked" && strings.Contains(e.Command, "delete ns")
		}) {
			t.Errorf("no blocked audit entry for the namespace delete among %d entries", len(blocked))
		}

		all := auditQuery(t, "per_page=100")
		if !hasAudit(all, func(e auditEntry) bool { return e.Reason == "INC-4521 e2e check" }) {
			t.Errorf("no audit entry carries the accepted reason among %d entries", len(all))
		}
	})
}

// TestGuardrailsCLI drives the real kb binary, which is where users meet this
// feature: the guardrail message must reach the terminal, and --reason must be
// consumed by kbridge rather than forwarded to kubectl.
func TestGuardrailsCLI(t *testing.T) {
	installGuardrails(t)
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: exit %d, stderr=%s", code, stderr)
	}

	t.Run("deny guardrail message reaches the terminal", func(t *testing.T) {
		_, stderr, code := runCLI(t, "delete", "ns", "e2e-nonexistent")
		if code == 0 {
			t.Fatal("expected a non-zero exit for a blocked command")
		}
		if !strings.Contains(stderr, "e2e guardrail blocked this") {
			t.Errorf("stderr missing the guardrail message: %s", stderr)
		}
		if !strings.Contains(stderr, "e2e-no-namespace-delete") {
			t.Errorf("stderr missing the guardrail name: %s", stderr)
		}
	})

	t.Run("require-reason prints the retry hint", func(t *testing.T) {
		_, stderr, code := runCLI(t, "delete", "pod", "e2e-nonexistent")
		if code == 0 {
			t.Fatal("expected a non-zero exit without a reason")
		}
		if !strings.Contains(stderr, "--reason") {
			t.Errorf("stderr should tell the user how to retry: %s", stderr)
		}
	})

	// The pod does not exist, so kubectl reports NotFound. That is the point:
	// a "unknown flag: --reason" error here would mean the flag leaked through
	// to kubectl instead of being consumed by kbridge.
	assertReasonConsumed := func(t *testing.T, stdout, stderr string) {
		t.Helper()
		combined := stdout + stderr
		if strings.Contains(combined, "unknown flag") || strings.Contains(combined, "--reason") {
			t.Errorf("--reason leaked through to kubectl: %s", combined)
		}
		if !strings.Contains(combined, "NotFound") && !strings.Contains(combined, "not found") {
			t.Errorf("expected kubectl to report the missing pod, got: %s", combined)
		}
	}

	t.Run("--reason value admits the command and is stripped", func(t *testing.T) {
		stdout, stderr, _ := runCLI(t, "delete", "pod", "e2e-nonexistent", "--reason", "INC-4521 cli space form")
		assertReasonConsumed(t, stdout, stderr)
	})

	t.Run("--reason=value form works too", func(t *testing.T) {
		stdout, stderr, _ := runCLI(t, "delete", "pod", "e2e-nonexistent", "--reason=INC-4521 cli inline form")
		assertReasonConsumed(t, stdout, stderr)
	})

	t.Run("CLI reasons reach the audit log", func(t *testing.T) {
		all := auditQuery(t, "per_page=100")
		for _, want := range []string{"INC-4521 cli space form", "INC-4521 cli inline form"} {
			if !hasAudit(all, func(e auditEntry) bool { return e.Reason == want }) {
				t.Errorf("no audit entry carries reason %q", want)
			}
		}
	})
}

// TestPolicyCLICommands covers kb policy, which reads a file directly and needs
// no control plane, so it can gate a CI job on a proposed policy.
func TestPolicyCLICommands(t *testing.T) {
	dir := t.TempDir()
	good := dir + "/good.yaml"
	if err := os.WriteFile(good, []byte(guardrailPolicy()), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	bad := dir + "/bad.yaml"
	if err := os.WriteFile(bad, []byte("bindings:\n  - subject: x\n    roles: [\"nope\"]\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	t.Run("validate accepts a good policy", func(t *testing.T) {
		stdout, stderr, code := runCLI(t, "policy", "validate", "-f", good)
		if code != 0 {
			t.Fatalf("exit %d, stderr=%s", code, stderr)
		}
		if !strings.Contains(stdout, "guardrail") {
			t.Errorf("output should summarize the guardrails: %s", stdout)
		}
	})

	t.Run("validate rejects an undefined role", func(t *testing.T) {
		_, stderr, code := runCLI(t, "policy", "validate", "-f", bad)
		if code == 0 {
			t.Fatal("expected a non-zero exit for an invalid policy")
		}
		if !strings.Contains(stderr, "nope") {
			t.Errorf("stderr should name the unknown role: %s", stderr)
		}
	})

	tests := []struct {
		name     string
		args     []string
		wantExit int
		contains string
	}{
		{
			name:     "blocked by a deny guardrail",
			args:     []string{"-u", "admin@e2e.test", "-c", "prod", "--", "delete", "ns", "payments"},
			wantExit: 1, contains: "blocked",
		},
		{
			name:     "reason required",
			args:     []string{"-u", "admin@e2e.test", "-c", "prod", "--", "delete", "pod", "api-0"},
			wantExit: 1, contains: "reason-required",
		},
		{
			name:     "allowed once a reason is supplied",
			args:     []string{"-u", "admin@e2e.test", "-c", "prod", "--reason", "INC-4521 rollback", "--", "delete", "pod", "api-0"},
			wantExit: 0, contains: "allowed",
		},
		{
			name:     "denied for a subject with no matching role",
			args:     []string{"-u", "stranger@nowhere.test", "-c", "prod", "--", "delete", "pod", "api-0"},
			wantExit: 1, contains: "denied",
		},
		{
			name:     "allowed for an unguarded read",
			args:     []string{"-u", "admin@e2e.test", "-c", "prod", "--", "get", "pods"},
			wantExit: 0, contains: "allowed",
		},
	}
	for _, tt := range tests {
		t.Run("test: "+tt.name, func(t *testing.T) {
			args := append([]string{"policy", "test", "-f", good}, tt.args...)
			stdout, stderr, code := runCLI(t, args...)
			if code != tt.wantExit {
				t.Fatalf("exit = %d, want %d (stdout=%s stderr=%s)", code, tt.wantExit, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.contains) {
				t.Errorf("output missing %q: %s", tt.contains, stdout)
			}
		})
	}
}

// auditEntry is the subset of an audit record these tests assert on.
type auditEntry struct {
	UserEmail string `json:"user_email"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

// auditQuery fetches audit entries matching a raw query string.
func auditQuery(t *testing.T, query string) []auditEntry {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/admin/audit?%s", *controlPlaneURL, query)
	raw, code := doJSON(t, http.MethodGet, url, authToken, nil)
	if code != http.StatusOK {
		t.Fatalf("audit query %q: status %d", query, code)
	}
	var resp struct {
		Logs []auditEntry `json:"logs"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	return resp.Logs
}

// hasAudit reports whether any entry satisfies match.
func hasAudit(entries []auditEntry, match func(auditEntry) bool) bool {
	for _, e := range entries {
		if match(e) {
			return true
		}
	}
	return false
}

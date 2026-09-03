//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

// guardrailPolicy grants the given subject full access, then guards it. The
// cluster patterns are "*" because the e2e cluster name is supplied by a flag.
func guardrailPolicy(email string) string {
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
  - subject: %s
    roles: ["admin"]
guardrails:
  - name: e2e-no-namespace-delete
    match:
      resources: ["namespaces", "ns"]
      verbs: ["delete"]
    action: deny
    message: "e2e guardrail blocked this"
  - name: e2e-delete-needs-reason
    match:
      verbs: ["delete"]
    action: require-reason
`, email)
}

// execWithBody posts an exec request and returns the decoded body and status.
func execWithBody(t *testing.T, token string, payload map[string]any) (map[string]any, int) {
	t.Helper()
	raw, code := httpPostAuth(t, execURL(), token, payload)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return body, code
}

// TestGuardrailsEnforced installs a policy carrying guardrails, then checks that
// a deny blocks, that a require-reason guardrail refuses a bare command and
// admits one carrying --reason, and that the block reaches the audit log.
func TestGuardrailsEnforced(t *testing.T) {
	original, err := os.ReadFile(*rbacPolicyPath)
	if err != nil {
		t.Fatalf("read policy %s: %v (set -rbac-policy)", *rbacPolicyPath, err)
	}
	email := "edge-guardrail@e2e.test"
	ensureUser(t, email)
	user := loginAs(t, email, edgePassword)

	defer func() {
		os.WriteFile(*rbacPolicyPath, original, 0o644)
		signalControlPlaneReload()
		// Wait for the restored policy to take effect so later tests are not
		// evaluated against the guardrail policy.
		pollExec(t, user, []string{"get", "pods", "-n", "default"},
			func(c int) bool { return c != http.StatusForbidden }, 8*time.Second)
	}()

	if err := os.WriteFile(*rbacPolicyPath, []byte(guardrailPolicy(email)), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := signalControlPlaneReload(); err != nil {
		t.Fatalf("signal reload: %v", err)
	}

	// Wait for the guardrail policy to be live: the user now has admin, so a
	// namespace delete flips from "denied" to "blocked" but stays 403. Poll on
	// an ordinary read becoming allowed instead.
	nsDelete := []string{"delete", "ns", "e2e-nonexistent"}
	if last, ok := pollExec(t, user, []string{"get", "pods", "-n", "default"},
		func(c int) bool { return c != http.StatusForbidden }, 8*time.Second); !ok {
		t.Fatalf("policy reload did not take effect; last status=%d", last)
	}

	t.Run("deny guardrail blocks", func(t *testing.T) {
		body, code := execWithBody(t, user, map[string]any{"command": nsDelete})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["guardrail"] != "e2e-no-namespace-delete" {
			t.Errorf("guardrail = %v, want e2e-no-namespace-delete", body["guardrail"])
		}
		if body["error"] != "e2e guardrail blocked this" {
			t.Errorf("error = %v, want the guardrail's message", body["error"])
		}
	})

	podDelete := []string{"delete", "pod", "e2e-nonexistent", "-n", "default"}

	t.Run("require-reason refuses without one", func(t *testing.T) {
		body, code := execWithBody(t, user, map[string]any{"command": podDelete})
		if code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", code)
		}
		if body["reason_required"] != true {
			t.Errorf("reason_required = %v, want true", body["reason_required"])
		}
	})

	t.Run("require-reason admits with one", func(t *testing.T) {
		_, code := execWithBody(t, user, map[string]any{
			"command": podDelete,
			"reason":  "INC-4521 e2e check",
		})
		if code == http.StatusForbidden {
			t.Fatalf("a command carrying a reason was refused with 403")
		}
	})

	t.Run("blocked command is audited", func(t *testing.T) {
		url := fmt.Sprintf("%s/api/v1/admin/audit?status=blocked&per_page=50", *controlPlaneURL)
		raw, code := doJSON(t, http.MethodGet, url, authToken, nil)
		if code != http.StatusOK {
			t.Fatalf("audit query status = %d, want 200", code)
		}
		var resp struct {
			Logs []struct {
				UserEmail string `json:"user_email"`
				Command   string `json:"command"`
				Status    string `json:"status"`
			} `json:"logs"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode audit response: %v", err)
		}
		for _, l := range resp.Logs {
			if l.UserEmail == email && l.Status == "blocked" {
				return
			}
		}
		t.Errorf("no blocked audit entry for %s among %d entries", email, len(resp.Logs))
	})
}

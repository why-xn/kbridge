//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// clusterStatus returns the status of a named cluster and whether it is present
// in central's cluster list at all.
func clusterStatus(t *testing.T, name string) (string, bool) {
	t.Helper()
	body, code := httpGetAuth(t, fmt.Sprintf("%s/api/v1/clusters", *centralURL), authToken)
	if code != http.StatusOK {
		t.Fatalf("list clusters: want 200, got %d", code)
	}
	var resp struct {
		Clusters []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("parse clusters: %v", err)
	}
	for _, c := range resp.Clusters {
		if c.Name == name {
			return c.Status, true
		}
	}
	return "", false
}

// startAgentWithToken launches a real kbridge-agent process configured with the
// given token and cluster, pointed at central's gRPC address. It returns a stop
// function that kills the process. The agent uses a minimal config (the same
// shape the harness writes for the main agent).
func startAgentWithToken(t *testing.T, cluster, token string) func() {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	cfg := fmt.Sprintf("central:\n  url: %q\n  token: %q\ncluster:\n  name: %q\n",
		*grpcAddr, token, cluster)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write agent config: %v", err)
	}

	cmd := exec.Command(filepath.Join(*binDir, "kbridge-agent"), "--config", cfgPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Logf("unauthorized agent output:\n%s", out.String())
	}
}

// assertNeverConnects polls the cluster's status for the given window and fails
// if it ever reaches "connected" — an unauthorized agent must never register.
func assertNeverConnects(t *testing.T, cluster string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if status, found := clusterStatus(t, cluster); found && status == "connected" {
			t.Fatalf("cluster %q reached %q with an unauthorized agent", cluster, status)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestAgentAuthRejectsBadTokens proves the negative agent-auth path end-to-end:
// an agent presenting an invalid or revoked token is refused at the gRPC layer
// and its cluster never transitions to "connected". The positive path is
// covered by TestAgentRegistration.
func TestAgentAuthRejectsBadTokens(t *testing.T) {
	t.Run("garbage token never connects", func(t *testing.T) {
		cluster := "edge-badtoken-cluster"
		stop := startAgentWithToken(t, cluster, "kbat_this-is-not-a-valid-token")
		defer stop()
		assertNeverConnects(t, cluster, 6*time.Second)
	})

	t.Run("revoked token never connects", func(t *testing.T) {
		cluster := "edge-revoked-agent-cluster"
		tokensURL := fmt.Sprintf("%s/api/v1/admin/agent-tokens", *centralURL)

		// Create a real token (this also registers the cluster) then revoke it.
		body, code := httpPostAuth(t, tokensURL, authToken, map[string]any{"cluster_name": cluster})
		if code != http.StatusCreated {
			t.Fatalf("create token: want 201, got %d %s", code, body)
		}
		var created struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &created); err != nil || created.Token == "" {
			t.Fatalf("create response missing token: %v %s", err, body)
		}
		if _, c := doJSON(t, http.MethodDelete, tokensURL+"/"+created.ID, authToken, nil); c != http.StatusOK {
			t.Fatalf("revoke token: want 200, got %d", c)
		}

		stop := startAgentWithToken(t, cluster, created.Token)
		defer stop()
		assertNeverConnects(t, cluster, 6*time.Second)
	})
}

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
	"strings"
	"testing"
	"time"
)

// clusterStatus returns the status of a named cluster and whether it is present
// in control plane's cluster list at all.
func clusterStatus(t *testing.T, name string) (string, bool) {
	t.Helper()
	body, code := httpGetAuth(t, fmt.Sprintf("%s/api/v1/clusters", *controlPlaneURL), authToken)
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

// cliClusterStatus returns the STATUS `kb clusters list` prints for name, or ""
// when the cluster is not listed.
func cliClusterStatus(t *testing.T, name string) string {
	t.Helper()
	stdout, stderr, code := runCLI(t, "clusters", "list")
	if code != 0 {
		t.Fatalf("kb clusters list: exit %d: %s", code, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		// CURRENT may be blank, so read NAME and STATUS from the end.
		if f := strings.Fields(line); len(f) >= 2 && f[len(f)-2] == name {
			return f[len(f)-1]
		}
	}
	return ""
}

// waitCLIClusterStatus polls `kb clusters list` until name shows want.
func waitCLIClusterStatus(t *testing.T, name, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		got := cliClusterStatus(t, name)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("kb clusters list shows %q for %s after %s, want %q", got, name, within, want)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// agentProcess is a kbridge-agent running on the host.
type agentProcess struct {
	cmd  *exec.Cmd
	out  bytes.Buffer
	done chan struct{} // closed when the process has exited; out is then safe to read
}

// stop kills the agent if it is still running and returns its output.
func (p *agentProcess) stop() string {
	_ = p.cmd.Process.Kill()
	<-p.done
	return p.out.String()
}

// startAgentWithToken launches a real kbridge-agent process configured with the
// given token and cluster, pointed at control plane's gRPC address. It returns a stop
// function that kills the process. The agent uses a minimal config (the same
// shape the harness writes for the main agent).
func startAgentWithToken(t *testing.T, cluster, token string) func() {
	t.Helper()
	p := startAgentProcess(t, cluster, token)
	return func() { t.Logf("unauthorized agent output:\n%s", p.stop()) }
}

// startAgentProcess starts the host agent and returns it running.
func startAgentProcess(t *testing.T, cluster, token string) *agentProcess {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	cfg := fmt.Sprintf("control_plane:\n  url: %q\n  token: %q\ncluster:\n  name: %q\nkubernetes:\n  kubeconfig: %q\n",
		*grpcAddr, token, cluster, hostKubeconfig())
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write agent config: %v", err)
	}

	p := &agentProcess{
		cmd:  exec.Command(filepath.Join(*binDir, "kbridge-agent"), "--config", cfgPath),
		done: make(chan struct{}),
	}
	p.cmd.Stdout = &p.out
	p.cmd.Stderr = &p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	go func() {
		_ = p.cmd.Wait()
		close(p.done)
	}()
	return p
}

// createAgentToken creates an agent token for cluster through the admin API
// and returns its id and plaintext.
func createAgentToken(t *testing.T, cluster string) (id, token string) {
	t.Helper()
	tokensURL := fmt.Sprintf("%s/api/v1/admin/agent-tokens", *controlPlaneURL)
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
	return created.ID, created.Token
}

// TestAgentTokenRevoke_DisconnectsAgent revokes the token of a connected
// agent: the cluster must show disconnected at once, commands to it must fail
// at once, and the agent must exit on its next re-registration.
func TestAgentTokenRevoke_DisconnectsAgent(t *testing.T) {
	const cluster = "e2e-revoke"
	id, token := createAgentToken(t, cluster)
	p := startAgentProcess(t, cluster, token)
	t.Cleanup(func() { t.Logf("agent output:\n%s", p.stop()) })
	waitCLIClusterStatus(t, cluster, "connected", 30*time.Second)

	if _, stderr, code := runCLI(t, "clusters", "use", cluster); code != 0 {
		t.Fatalf("clusters use %s: %s", cluster, stderr)
	}
	t.Cleanup(func() { runCLI(t, "clusters", "use", *clusterName) })

	tokenURL := fmt.Sprintf("%s/api/v1/admin/agent-tokens/%s", *controlPlaneURL, id)
	if body, code := doJSON(t, http.MethodDelete, tokenURL, authToken, nil); code != http.StatusOK {
		t.Fatalf("revoke token: want 200, got %d %s", code, body)
	}
	waitCLIClusterStatus(t, cluster, "disconnected", 5*time.Second)

	start := time.Now()
	_, stderr, code := runCLI(t, "get", "pods")
	want := `cluster "e2e-revoke": agent is disconnected`
	if took := time.Since(start); code == 0 || !strings.Contains(stderr, want) || took > 5*time.Second {
		t.Errorf("kb get pods: exit %d in %s, stderr %q; want a failure at once with %q", code, took, stderr, want)
	}

	select {
	case <-p.done:
	case <-time.After(60 * time.Second):
		t.Fatal("agent still running 60 s after its token was revoked")
	}
	if code := p.cmd.ProcessState.ExitCode(); code == 0 {
		t.Errorf("agent exit code = 0, want non-zero")
	}
	if out := p.out.String(); !strings.Contains(out, "agent token revoked") {
		t.Errorf("agent output does not say %q:\n%s", "agent token revoked", out)
	}
}

// hostKubeconfig is the kubeconfig the host-process agent validates against.
func hostKubeconfig() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return filepath.SplitList(p)[0]
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kube", "config")
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
		tokensURL := fmt.Sprintf("%s/api/v1/admin/agent-tokens", *controlPlaneURL)

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

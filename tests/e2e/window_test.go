//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	agentNamespace  = "kbridge"
	agentDeployment = "deploy/kbridge-agent"
	agentTokenPath  = "/var/run/kbridge/sa/token"
	reservedCopy    = `namespace "kbridge" is reserved for the kbridge agent`
)

// TestWindow drives the attacks from the external review through the real kb
// binary and checks that ordinary commands still work.
func TestWindow(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	t.Run("refused", testWindowRefuses)
	t.Run("agent token never reaches kb", testWindowHidesAgentToken)
	t.Run("apply stops at the reserved document", testWindowApplyReservedDocument)
	t.Run("allowed", testWindowAllows)
}

func testWindowRefuses(t *testing.T) {
	node, err := exec.Command("kubectl", "get", "nodes", "-o", "name").Output()
	if err != nil {
		t.Fatalf("list nodes with admin kubectl: %v", err)
	}
	nodeName := strings.TrimPrefix(strings.Fields(string(node))[0], "node/")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"connection flag", []string{"get", "pods", "--server=https://example.com"},
			"flag --server is not allowed: kbridge manages the cluster connection"},
		{"token for the agent account", []string{"create", "token", "kbridge-agent", "-n", agentNamespace}, reservedCopy},
		{"secrets across namespaces", []string{"get", "secrets", "-A"},
			"reading secrets across all namespaces is not allowed through kbridge"},
		{"node proxy", []string{"get", "--raw", "/api/v1/nodes/" + nodeName + "/proxy/healthz"},
			"node proxy is not allowed through kbridge"},
		{"cp", []string{"cp", "/etc/hosts", "default/x:/tmp/h"}, "kubectl cp is not supported through kbridge"},
		{"local file read", []string{"create", "configmap", "t", "--from-file=/etc/hosts"},
			"flag --from-file is not allowed: the agent does not read local files; build the object locally with 'kubectl create ... --dry-run=client -o yaml' and send it with 'kb apply -f -'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLI(t, tc.args...)
			assertRefused(t, stdout+stderr, code, tc.want)
		})
	}
}

func assertRefused(t *testing.T, out string, code int, want string) {
	t.Helper()
	if code == 0 {
		t.Errorf("exit 0, want non-zero; output: %s", out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q; got: %s", want, out)
	}
}

func testWindowHidesAgentToken(t *testing.T) {
	token := agentToken(t)
	stdout, stderr, code := runCLI(t, "exec", "-n"+agentNamespace, agentDeployment, "--", "cat", agentTokenPath)
	out := stdout + stderr
	assertRefused(t, out, code, reservedCopy)
	if strings.Contains(out, token) {
		t.Fatal("kb output contains the agent token")
	}
}

// agentToken reads the agent's token with the harness's admin kubectl.
func agentToken(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", agentNamespace, "exec", agentDeployment,
		"--", "cat", agentTokenPath).Output()
	token := strings.TrimSpace(string(out))
	if err != nil || len(token) < 20 {
		t.Fatalf("read agent token with admin kubectl: %v", err)
	}
	return token
}

const twoConfigMaps = `apiVersion: v1
kind: ConfigMap
metadata:
  name: window-allowed
  namespace: default
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: window-reserved
  namespace: kbridge
`

func testWindowApplyReservedDocument(t *testing.T) {
	t.Cleanup(func() {
		_ = exec.Command("kubectl", "-n", "default", "delete", "configmap", "window-allowed", "--ignore-not-found").Run()
		_ = exec.Command("kubectl", "-n", agentNamespace, "delete", "configmap", "window-reserved", "--ignore-not-found").Run()
	})
	out, code := runCLIStdin(t, twoConfigMaps, "apply", "-f", "-")
	assertRefused(t, out, code, reservedCopy)
	if err := exec.Command("kubectl", "-n", "default", "get", "configmap", "window-allowed").Run(); err != nil {
		t.Errorf("first document was not created: %v", err)
	}
	if err := exec.Command("kubectl", "-n", agentNamespace, "get", "configmap", "window-reserved").Run(); err == nil {
		t.Error("second document reached the reserved namespace")
	}
}

// runCLIStdin runs kb with stdin and returns combined output and exit code.
func runCLIStdin(t *testing.T, stdin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(getCLIPath(), args...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("run kb: %v", err)
	}
	return string(out), cmd.ProcessState.ExitCode()
}

// windowPodScript serves /tmp over HTTP on 8080 and prints a line every 200ms.
const windowPodScript = "echo window-ok > /tmp/index.html; httpd -p 8080 -h /tmp; " +
	"i=0; while true; do echo tick-$i; i=$((i+1)); sleep 0.2; done"

func testWindowAllows(t *testing.T) {
	ensurePod(t, "window-web", windowPodScript)
	t.Run("flags after -- reach the pod", testWindowExecPassesFlags)
	t.Run("logs -f streams", testWindowLogsFollow)
	t.Run("port-forward serves http", testWindowPortForward)
}

func testWindowExecPassesFlags(t *testing.T) {
	stdout, stderr, code := runCLI(t, "exec", "-n", "default", "window-web", "--", "sh", "-c", "echo --server -n kbridge")
	if code != 0 || !strings.Contains(stdout, "--server -n kbridge") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func testWindowLogsFollow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, getCLIPath(), "logs", "-f", "window-web")
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run() // killed after 2s by design
	if !strings.Contains(out.String(), "tick-") {
		t.Fatalf("no log lines within 2s; got %q", out.String())
	}
}

func testWindowPortForward(t *testing.T) {
	// 18082: 18080 is the control plane and 18081 is TestPortForwardHTTP.
	stop := startPortForward(t, "window-web", "18082:8080")
	defer stop()
	resp, err := http.Get("http://127.0.0.1:18082/index.html")
	if err != nil {
		t.Fatalf("GET via forward: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "window-ok") {
		t.Fatalf("status %d, body %q", resp.StatusCode, body)
	}
}

//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	developerEmail = "developer@e2e.test"
	developerUser  = "kbridge:" + developerEmail
	devNamespace   = "e2e-dev"
)

// TestImpersonation runs kb as the developer, whom the agent binds (from the
// harness policy) to `view` cluster-wide and `edit` in e2e-dev, and checks the
// cluster sees and authorizes that user.
func TestImpersonation(t *testing.T) {
	useDeveloperConfig(t)
	ensureDevPod(t, "imp-web")
	t.Run("reads where bound", testImpersonationReadsBound)
	t.Run("forbidden beyond the bound role", testImpersonationForbiddenUnbound)
	t.Run("exec", testImpersonationExec)
	t.Run("port-forward", testImpersonationPortForward)
	t.Run("edit", testImpersonationEdit)
	t.Run("whoami", testImpersonationWhoami)
	t.Run("pod security applies", testImpersonationPodSecurity)
}

// useDeveloperConfig points kb (every child process) at a CLI config logged in
// as the developer, via a temporary HOME.
func useDeveloperConfig(t *testing.T) {
	t.Helper()
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	token := loginAs(t, developerEmail, edgePassword)
	home := t.TempDir()
	cfg := fmt.Sprintf("control_plane_url: %q\ncurrent_cluster: %q\ntoken: %q\n", *controlPlaneURL, *clusterName, token)
	if err := os.MkdirAll(filepath.Join(home, ".kbridge"), 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kbridge", "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write developer config: %v", err)
	}
	t.Setenv("HOME", home)
}

// ensureDevPod creates, as the developer, a busybox web pod in e2e-dev.
func ensureDevPod(t *testing.T, name string) {
	t.Helper()
	runCLI(t, "delete", "pod", name, "-n", devNamespace, "--ignore-not-found")
	if _, stderr, code := runCLI(t, "run", name, "-n", devNamespace, "--image=busybox:1.36", "--restart=Never",
		"--command", "--", "sh", "-c", windowPodScript); code != 0 {
		t.Fatalf("create pod as developer: %s", stderr)
	}
	t.Cleanup(func() { runCLI(t, "delete", "pod", name, "-n", devNamespace, "--ignore-not-found") })
	if _, stderr, code := runCLI(t, "wait", "--for=condition=Ready", "pod/"+name, "-n", devNamespace, "--timeout=60s"); code != 0 {
		t.Fatalf("pod not ready: %s", stderr)
	}
}

func testImpersonationReadsBound(t *testing.T) {
	stdout, stderr, code := runCLI(t, "get", "pods", "-n", devNamespace)
	if code != 0 || !strings.Contains(stdout, "imp-web") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// kbridge policy lets the developer read secrets here; `view` does not.
func testImpersonationForbiddenUnbound(t *testing.T) {
	stdout, stderr, code := runCLI(t, "get", "secrets", "-n", "e2e-nobind")
	assertRefused(t, stdout+stderr, code, fmt.Sprintf(`User %q cannot list resource "secrets"`, developerUser))
}

func testImpersonationExec(t *testing.T) {
	stdout, stderr, code := runCLI(t, "exec", "-n", devNamespace, "imp-web", "--", "echo", "exec-as-developer")
	if code != 0 || !strings.Contains(stdout, "exec-as-developer") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func testImpersonationPortForward(t *testing.T) {
	// 18083: 18080 is the control plane, 18081 and 18082 are other forwards.
	stop := startPortForward(t, "imp-web", "-n", devNamespace, "18083:8080")
	defer stop()
	resp, err := http.Get("http://127.0.0.1:18083/index.html")
	if err != nil {
		t.Fatalf("GET via forward: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "window-ok") {
		t.Fatalf("status %d, body %q", resp.StatusCode, body)
	}
}

func testImpersonationEdit(t *testing.T) {
	runCLI(t, "delete", "configmap", "x", "-n", devNamespace, "--ignore-not-found")
	if _, stderr, code := runCLI(t, "create", "configmap", "x", "-n", devNamespace, "--from-literal=k=before"); code != 0 {
		t.Fatalf("create configmap: %s", stderr)
	}
	t.Cleanup(func() { runCLI(t, "delete", "configmap", "x", "-n", devNamespace, "--ignore-not-found") })
	editor := createEditorScript(t, "s/before/after/g")
	defer os.Remove(editor)
	env := map[string]string{"KUBE_EDITOR": editor}
	if stdout, stderr, code := runCLIWithEnv(t, env, "edit", "configmap/x", "-n", devNamespace); code != 0 {
		t.Fatalf("edit: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	stdout, _, _ := runCLI(t, "get", "configmap", "x", "-n", devNamespace, "-o", "jsonpath={.data.k}")
	if stdout != "after" {
		t.Fatalf("configmap data k = %q, want after", stdout)
	}
}

func testImpersonationWhoami(t *testing.T) {
	stdout, stderr, code := runCLI(t, "auth", "whoami", "-n", devNamespace)
	if code != 0 || !strings.Contains(stdout, developerUser) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

const hostPathPod = `apiVersion: v1
kind: Pod
metadata:
  name: imp-hostpath
spec:
  containers:
    - name: c
      image: busybox:1.36
      command: ["sleep", "3600"]
      volumeMounts:
        - name: host
          mountPath: /host
  volumes:
    - name: host
      hostPath:
        path: /
`

func testImpersonationPodSecurity(t *testing.T) {
	t.Cleanup(func() { runCLI(t, "delete", "pod", "imp-hostpath", "-n", devNamespace, "--ignore-not-found") })
	out, code := runCLIStdin(t, hostPathPod, "apply", "-n", devNamespace, "-f", "-")
	assertRefused(t, out, code, `violates PodSecurity "baseline`)
}

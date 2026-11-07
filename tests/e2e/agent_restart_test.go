//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const agentRetryCopy = "agent shut down while the command was running; retry it"

// TestAgentRestart_FailsRunningCommand kills the agent pod under a command that
// is waiting on it. The command must fail once the replacement agent registers,
// not sit until the command timeout.
func TestAgentRestart_FailsRunningCommand(t *testing.T) {
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: %s", stderr)
	}
	// wait --for=delete on a missing pod returns at once; use a pod that stays.
	run := exec.Command("kubectl", "run", "restart-probe", "--image=busybox", "--restart=Never", "--", "sleep", "3600")
	if out, err := run.CombinedOutput(); err != nil && !strings.Contains(string(out), "already exists") {
		t.Fatalf("create pod: %v: %s", err, out)
	}
	defer exec.Command("kubectl", "delete", "pod", "restart-probe", "--ignore-not-found", "--force", "--grace-period=0").Run()
	waitPodReady(t, "restart-probe", 60*time.Second)

	cmd := exec.Command(getCLIPath(), "wait", "--for=delete", "pod/restart-probe", "--timeout=240s")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kb wait: %v", err)
	}
	time.Sleep(3 * time.Second) // let the agent pick the command up
	killAgentPod(t)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("kb wait still running 90 s after the agent pod was killed")
	}
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("exit code = %d, want 1; stderr: %s", code, stderr.String())
	}
	// The control plane sees the stream close first and fails the command as
	// disconnected; the retry copy appears only if the replacement registers
	// before that.
	if out := stderr.String(); !strings.Contains(out, agentRetryCopy) && !strings.Contains(out, "agent is disconnected") {
		t.Errorf("stderr = %q, want %q or an agent-disconnected error", out, agentRetryCopy)
	}
	waitAgentBack(t)
}

// killAgentPod SIGKILLs the agent process from the Kind node, so the agent
// cannot report the command itself; kubelet restarts the container.
func killAgentPod(t *testing.T) {
	t.Helper()
	out, err := exec.Command("docker", "exec", *clusterName+"-control-plane", "pkill", "-9", "-x", "kbridge-agent").CombinedOutput()
	if err != nil {
		t.Fatalf("kill agent: %v: %s", err, out)
	}
}

func waitAgentBack(t *testing.T) {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", agentNamespace, "rollout", "status",
		"deploy/kbridge-agent", "--timeout=90s").CombinedOutput()
	if err != nil {
		t.Fatalf("agent rollout: %v: %s", err, out)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, code := runCLI(t, "get", "pods"); code == 0 {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatal("cluster did not come back after the agent restart")
}

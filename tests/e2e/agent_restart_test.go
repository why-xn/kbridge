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

// TestAgentRollout_NoOutage runs commands throughout a rolling restart of the
// agent. The replaced pod must not take the cluster back from the new one, so
// at most the one request in flight as the old pod goes away may fail.
func TestAgentRollout_NoOutage(t *testing.T) {
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: %s", stderr)
	}
	rolledOut := make(chan time.Time, 1)
	go func() {
		defer func() { rolledOut <- time.Now() }()
		time.Sleep(3 * time.Second)
		if out, err := exec.Command("kubectl", "-n", agentNamespace, "rollout", "restart", agentDeployment).CombinedOutput(); err != nil {
			t.Errorf("rollout restart: %v: %s", err, out)
			return
		}
		if out, err := exec.Command("kubectl", "-n", agentNamespace, "rollout", "status", agentDeployment, "--timeout=90s").CombinedOutput(); err != nil {
			t.Errorf("rollout status: %v: %s", err, out)
		}
	}()

	failures := runThroughRollout(t, 75*time.Second, rolledOut)
	if failures > 1 {
		t.Errorf("%d failed calls during the rollout, want at most 1", failures)
	}
	if got := cliClusterStatus(t, *clusterName); got != "connected" {
		t.Errorf("kb clusters list shows %q after the rollout, want connected", got)
	}
	waitOneAgentPod(t, 90*time.Second)
}

// runThroughRollout runs `kb get pods` every 500 ms for d and returns how many
// calls failed. Two failures in a row, or a failure in a call started after
// the rollout finished (rolledOut fired), fail the test.
func runThroughRollout(t *testing.T, d time.Duration, rolledOut <-chan time.Time) int {
	t.Helper()
	begin := time.Now()
	var done time.Time
	failures, prevFailed := 0, false
	for time.Since(begin) < d {
		select {
		case done = <-rolledOut:
		default:
		}
		started := time.Now()
		_, stderr, code := runCLI(t, "get", "pods", "-n", "default")
		failed := code != 0
		if failed {
			failures++
			t.Logf("call at +%s failed (exit %d): %s", started.Sub(begin).Round(time.Millisecond), code, stderr)
			if prevFailed {
				t.Errorf("two consecutive failed calls")
			}
			if !done.IsZero() && started.After(done) {
				t.Errorf("call failed after rollout status returned")
			}
		}
		prevFailed = failed
		time.Sleep(500 * time.Millisecond)
	}
	if done.IsZero() {
		done = <-rolledOut // the rollout goroutine always sends; it must finish before the test ends
	}
	return failures
}

// waitOneAgentPod waits until exactly one agent pod exists: the replaced pod
// has terminated and no second agent is serving the cluster.
func waitOneAgentPod(t *testing.T, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		out, err := exec.Command("kubectl", "-n", agentNamespace, "get", "pods",
			"-l", "app.kubernetes.io/name=kbridge-agent", "-o", "name").CombinedOutput()
		pods := strings.Fields(string(out))
		if err == nil && len(pods) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent pods after %s: %v (err %v), want exactly one", within, pods, err)
		}
		time.Sleep(2 * time.Second)
	}
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

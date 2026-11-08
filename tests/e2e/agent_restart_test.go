//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
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
// at most the one request in flight as the old pod goes away may fail; a
// command already running on the old agent finishes there, and neither pod
// restarts.
func TestAgentRollout_NoOutage(t *testing.T) {
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: %s", stderr)
	}
	createRolloutWeb(t)
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

	time.Sleep(time.Second)
	execDone := startSleepExec(t) // 2 s before the rollout restart

	failures := runThroughRollout(t, 75*time.Second, rolledOut)
	if failures > 1 {
		t.Errorf("%d failed calls during the rollout, want at most 1", failures)
	}
	if res := <-execDone; res.code != 0 {
		t.Errorf("kb exec running across the rollout: exit %d, stderr %s", res.code, res.stderr)
	}
	if got := cliClusterStatus(t, *clusterName); got != "connected" {
		t.Errorf("kb clusters list shows %q after the rollout, want connected", got)
	}
	waitOneAgentPod(t, 90*time.Second)
	out, err := exec.Command("kubectl", "-n", agentNamespace, "get", "pod", "-l", "app.kubernetes.io/name=kbridge-agent",
		"-o", "jsonpath={.items[0].status.containerStatuses[0].restartCount}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "0" {
		t.Errorf("agent pod restart count = %q (err %v), want 0", out, err)
	}
}

// createRolloutWeb creates deploy/rollout-web in default for a command to run
// in across the rollout, and deletes it when the test ends.
func createRolloutWeb(t *testing.T) {
	t.Helper()
	if _, stderr, code := runCLI(t, "create", "deployment", "rollout-web", "--image=nginx:1.27-alpine", "-n", "default"); code != 0 {
		t.Fatalf("create deployment: %s", stderr)
	}
	t.Cleanup(func() { runCLI(t, "delete", "deployment", "rollout-web", "-n", "default", "--ignore-not-found") })
	if _, stderr, code := runCLI(t, "rollout", "status", "deployment/rollout-web", "-n", "default", "--timeout=120s"); code != 0 {
		t.Fatalf("rollout status rollout-web: %s", stderr)
	}
}

type rolloutExec struct {
	code   int
	stderr string
}

// startSleepExec runs a 20 s command through kb in the background.
func startSleepExec(t *testing.T) <-chan rolloutExec {
	done := make(chan rolloutExec, 1)
	go func() {
		_, stderr, code := runCLI(t, "exec", "deploy/rollout-web", "-n", "default", "--", "sh", "-c", "sleep 20")
		done <- rolloutExec{code, stderr}
	}()
	return done
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

// TestAgentFailover_StandingByAgentTakesOver replaces the in-cluster agent
// with a host agent for the same cluster, so the pod stands by, then kills the
// host agent. The pod must take the cluster back within one stand-by tick,
// without restarting.
func TestAgentFailover_StandingByAgentTakesOver(t *testing.T) {
	if _, stderr, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatalf("clusters use: %s", stderr)
	}
	standingBy := strings.Count(agentLog(t), "standing by") // lines from earlier tests
	tokenID, token := createAgentToken(t, *clusterName)
	t.Cleanup(func() {
		doJSON(t, http.MethodDelete, fmt.Sprintf("%s/api/v1/admin/agent-tokens/%s", *controlPlaneURL, tokenID), authToken, nil)
	})
	host := startAgentProcess(t, *clusterName, token)
	t.Cleanup(func() {
		t.Logf("host agent output:\n%s", host.stop())
		waitCLIClusterStatus(t, *clusterName, "connected", 60*time.Second) // the pod is current again
	})

	waitAgentLog(t, "standing by", standingBy+1, 30*time.Second) // the pod learned it was replaced
	if _, stderr, code := runCLI(t, "get", "pods"); code != 0 {
		t.Fatalf("kb get pods through the host agent: %s", stderr)
	}

	_ = host.cmd.Process.Kill() // SIGKILL: no clean shutdown
	deadline := time.Now().Add(45 * time.Second)
	for cliClusterStatus(t, *clusterName) != "connected" || !getPodsWorks(t) {
		if time.Now().After(deadline) {
			t.Fatalf("cluster not served 45 s after the host agent was killed; pod log:\n%s", agentLog(t))
		}
		time.Sleep(time.Second)
	}
	logs := agentLog(t)
	if i, j := strings.LastIndex(logs, "standing by"), strings.LastIndex(logs, "Registered successfully"); i < 0 || j < i {
		t.Errorf("pod log has no re-registration after standing by:\n%s", logs)
	}
	out, err := exec.Command("kubectl", "-n", agentNamespace, "get", "pod", "-l", "app.kubernetes.io/name=kbridge-agent",
		"-o", "jsonpath={.items[0].status.containerStatuses[0].restartCount}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "0" {
		t.Errorf("agent pod restart count = %q (err %v), want 0", out, err)
	}
}

func getPodsWorks(t *testing.T) bool {
	_, _, code := runCLI(t, "get", "pods")
	return code == 0
}

// agentLog returns the in-cluster agent's log.
func agentLog(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("kubectl", "-n", agentNamespace, "logs", agentDeployment).CombinedOutput()
	if err != nil {
		t.Fatalf("agent logs: %v: %s", err, out)
	}
	return string(out)
}

// waitAgentLog waits until the in-cluster agent's log contains want n times.
func waitAgentLog(t *testing.T, want string, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for strings.Count(agentLog(t), want) < n {
		if time.Now().After(deadline) {
			t.Fatalf("agent log has no %q after %s", want, within)
		}
		time.Sleep(time.Second)
	}
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

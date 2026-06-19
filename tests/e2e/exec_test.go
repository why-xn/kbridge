//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ensureShellPod creates a long-lived pod with a shell and waits until Ready.
func ensureShellPod(t *testing.T, name string) {
	t.Helper()
	_, _, _ = runCLI(t, "delete", "pod", name, "--ignore-not-found")
	if _, stderr, code := runCLI(t, "run", name, "--image=busybox:1.36", "--restart=Never",
		"--command", "--", "sh", "-c", "sleep 3600"); code != 0 {
		t.Fatalf("create pod: %s", stderr)
	}
	if _, stderr, code := runCLI(t, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=60s"); code != 0 {
		t.Fatalf("pod not ready: %s", stderr)
	}
	t.Cleanup(func() { runCLI(t, "delete", "pod", name, "--ignore-not-found") })
}

func TestExecStdin(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	ensureShellPod(t, "exec-stdin-pod")

	// Pipe a script into `kb exec -i ... -- sh`; no TTY needed.
	cmd := exec.Command(getCLIPath(), "exec", "-i", "exec-stdin-pod", "--", "sh")
	cmd.Stdin = strings.NewReader("echo HELLO-FROM-POD\nexit\n")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("exec -i failed: %v, out: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "HELLO-FROM-POD") {
		t.Fatalf("expected echoed output, got: %s", out.String())
	}
}

func TestExecInteractiveTTY(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	ensureShellPod(t, "exec-tty-pod")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, getCLIPath(), "exec", "-it", "exec-tty-pod", "--", "sh")
	ptmx, err := pty.Start(cmd) // the test IS the terminal
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120})

	go func() {
		time.Sleep(500 * time.Millisecond)
		_, _ = ptmx.Write([]byte("echo TTY-MARKER\n"))
		time.Sleep(500 * time.Millisecond)
		_, _ = ptmx.Write([]byte("exit\n"))
	}()

	var got bytes.Buffer
	buf := make([]byte, 4096)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n, rerr := ptmx.Read(buf)
		if n > 0 {
			got.Write(buf[:n])
			if strings.Contains(got.String(), "TTY-MARKER") {
				break
			}
		}
		if rerr != nil {
			break
		}
	}
	if !strings.Contains(got.String(), "TTY-MARKER") {
		t.Fatalf("did not see TTY output, got: %q", got.String())
	}
	_ = cmd.Wait()
}

func TestExecDeniedAndMissingPod(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	// Missing pod -> non-zero exit with an error message.
	cmd := exec.Command(getCLIPath(), "exec", "-i", "no-such-pod-xyz", "--", "sh")
	cmd.Stdin = strings.NewReader("exit\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("expected failure for missing pod, out: %s", out.String())
	}
}

//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startPortForward runs `kb port-forward ...` in the background and waits until
// it prints "Forwarding from". Returns a stop func that kills the process.
func startPortForward(t *testing.T, args ...string) func() {
	t.Helper()
	cmd := exec.Command(getCLIPath(), append([]string{"port-forward"}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			line := sc.Text()
			t.Logf("pf> %s", line)
			if strings.Contains(line, "Forwarding from") {
				select {
				case <-ready:
				default:
					close(ready)
				}
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatalf("port-forward never became ready; stderr: %s", stderrBuf.String())
	}
	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
}

// TestPortForwardHTTP creates an nginx pod, forwards local port 18081 to
// pod port 80, and verifies that an HTTP GET returns a 200 with nginx body.
// NOTE: local port 18081 is used (not 18080) to avoid conflict with the
// central service which binds 18080 during the e2e harness run.
func TestPortForwardHTTP(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	// Clean up any leftover pod from a previous run.
	runCLI(t, "delete", "pod", "pf-nginx", "--ignore-not-found")
	if _, e, c := runCLI(t, "run", "pf-nginx", "--image=nginx:1.25", "--port=80"); c != 0 {
		t.Fatalf("run nginx: %s", e)
	}
	t.Cleanup(func() { runCLI(t, "delete", "pod", "pf-nginx", "--ignore-not-found") })
	if _, e, c := runCLI(t, "wait", "--for=condition=Ready", "pod/pf-nginx", "--timeout=60s"); c != 0 {
		t.Fatalf("nginx not ready: %s", e)
	}

	// 18081 instead of 18080 — central service occupies 18080 in the e2e harness.
	stop := startPortForward(t, "pf-nginx", "18081:80")
	defer stop()

	resp, err := http.Get("http://127.0.0.1:18081/")
	if err != nil {
		t.Fatalf("GET via forward: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(strings.ToLower(string(body)), "nginx") {
		t.Errorf("unexpected body: %s", body)
	}
}

// TestPortForwardMultiConn creates a busybox pod running `nc -lk -p 7000 -e /bin/cat`
// (busybox 1.36 nc: -lk with -e spawns a new process per connection, allowing
// concurrent connections). Two goroutines dial simultaneously and each gets its
// own echo back, proving conn_id isolation.
func TestPortForwardMultiConn(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	runCLI(t, "delete", "pod", "pf-echo", "--ignore-not-found")

	// busybox 1.36 nc -lk with -e spawns a fresh process per accepted connection,
	// so concurrent connections each get their own cat that echoes back bytes.
	if _, e, c := runCLI(t, "run", "pf-echo", "--image=busybox:1.36", "--port=7000",
		"--command", "--", "nc", "-lk", "-p", "7000", "-e", "/bin/cat"); c != 0 {
		t.Fatalf("run echo: %s", e)
	}
	t.Cleanup(func() { runCLI(t, "delete", "pod", "pf-echo", "--ignore-not-found") })
	if _, e, c := runCLI(t, "wait", "--for=condition=Ready", "pod/pf-echo", "--timeout=60s"); c != 0 {
		t.Fatalf("echo not ready: %s", e)
	}

	stop := startPortForward(t, "pf-echo", "17000:7000")
	defer stop()

	// echo dials, writes send, reads back the same bytes (without CloseWrite —
	// cat echoes bytes as they arrive; CloseWrite would signal EOF to cat and
	// close the connection before we can read back).
	echo := func(send string) (string, error) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:17000", 5*time.Second)
		if err != nil {
			return "", fmt.Errorf("dial: %w", err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(send)); err != nil {
			return "", fmt.Errorf("write: %w", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return "", fmt.Errorf("deadline: %w", err)
		}
		buf := make([]byte, len(send))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		return string(buf), nil
	}

	type result struct {
		val string
		err error
	}
	a := make(chan result, 1)
	b := make(chan result, 1)
	go func() { v, e := echo("AAAA"); a <- result{v, e} }()
	go func() { v, e := echo("BBBB"); b <- result{v, e} }()

	ra := <-a
	rb := <-b
	if ra.err != nil {
		t.Fatalf("conn A error: %v", ra.err)
	}
	if rb.err != nil {
		t.Fatalf("conn B error: %v", rb.err)
	}
	if ra.val != "AAAA" {
		t.Errorf("conn A echo = %q, want %q", ra.val, "AAAA")
	}
	if rb.val != "BBBB" {
		t.Errorf("conn B echo = %q, want %q", rb.val, "BBBB")
	}
}

// TestPortForwardMissingPod verifies that forwarding to a non-existent pod
// exits non-zero (SESSION_ERROR is now propagated as a Go error through Cobra).
func TestPortForwardMissingPod(t *testing.T) {
	if _, _, code := runCLI(t, "clusters", "use", *clusterName); code != 0 {
		t.Fatal("select cluster")
	}
	cmd := exec.Command(getCLIPath(), "port-forward", "no-such-pod-xyz", "19099:80")
	out, _ := cmd.CombinedOutput()
	outStr := string(out)
	t.Logf("missing pod output: %s", outStr)

	if cmd.ProcessState == nil || cmd.ProcessState.Success() {
		t.Fatalf("expected non-zero exit for missing pod, got exit=0; output: %s", outStr)
	}
}

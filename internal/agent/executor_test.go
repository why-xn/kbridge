package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKubectlExecutor_Execute(t *testing.T) {
	// Use 'echo' instead of kubectl for testing
	e := &KubectlExecutor{
		kubectlPath: "echo",
	}

	ctx := context.Background()
	result := e.Execute(ctx, []string{"hello", "world"}, "", 5*time.Second)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	// Echo should output "hello world\n"
	expected := "hello world\n"
	if string(result.Stdout) != expected {
		t.Errorf("expected stdout %q, got %q", expected, string(result.Stdout))
	}
}

func TestKubectlExecutor_ExecuteWithStdin(t *testing.T) {
	// Use 'cat' to test stdin - it will echo back the stdin content
	e := &KubectlExecutor{
		kubectlPath: "cat",
	}

	ctx := context.Background()
	stdinContent := []byte("test input from stdin")
	result := e.ExecuteWithStdin(ctx, []string{}, "", 5*time.Second, stdinContent)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	// Cat should output exactly what was piped in
	if string(result.Stdout) != string(stdinContent) {
		t.Errorf("expected stdout %q, got %q", string(stdinContent), string(result.Stdout))
	}
}

func TestKubectlExecutor_ExecuteWithStdin_NilStdin(t *testing.T) {
	// Nil stdin should work the same as Execute
	e := &KubectlExecutor{
		kubectlPath: "echo",
	}

	ctx := context.Background()
	result := e.ExecuteWithStdin(ctx, []string{"no stdin"}, "", 5*time.Second, nil)

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	expected := "no stdin\n"
	if string(result.Stdout) != expected {
		t.Errorf("expected stdout %q, got %q", expected, string(result.Stdout))
	}
}

func TestKubectlExecutor_ExecuteWithStdin_EmptyStdin(t *testing.T) {
	// Empty stdin should also work
	e := &KubectlExecutor{
		kubectlPath: "cat",
	}

	ctx := context.Background()
	result := e.ExecuteWithStdin(ctx, []string{}, "", 5*time.Second, []byte{})

	if result.Error != nil {
		t.Fatalf("unexpected error: %v", result.Error)
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}

	// Empty input should produce empty output
	if len(result.Stdout) != 0 {
		t.Errorf("expected empty stdout, got %q", string(result.Stdout))
	}
}


func TestKubectlExecutor_Execute_Timeout(t *testing.T) {
	// Use 'sleep' to test timeout
	e := &KubectlExecutor{
		kubectlPath: "sleep",
	}

	ctx := context.Background()
	result := e.Execute(ctx, []string{"10"}, "", 100*time.Millisecond)

	// Should have non-zero exit code due to timeout
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code due to timeout")
	}
}

func TestKubectlExecutor_Execute_NonZeroExit(t *testing.T) {
	// Use 'false' command which always exits with 1
	e := &KubectlExecutor{
		kubectlPath: "false",
	}

	ctx := context.Background()
	result := e.Execute(ctx, []string{}, "", 5*time.Second)

	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

func TestNewKubectlExecutor(t *testing.T) {
	e := NewKubectlExecutor()
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
	if e.kubectlPath != "kubectl" {
		t.Errorf("expected kubectlPath 'kubectl', got %q", e.kubectlPath)
	}
}

func TestKubectlExecutor_ExecuteStream(t *testing.T) {
	// Point the executor at /bin/sh and pass a script via args to emit two lines.
	e := &KubectlExecutor{kubectlPath: "/bin/sh"}
	var got []byte
	code, err := e.ExecuteStream(context.Background(),
		[]string{"-c", "printf 'a\\nb\\n'"}, "",
		func(stdout bool, data []byte) { got = append(got, data...) })
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	if code != 0 {
		t.Errorf("want exit 0, got %d", code)
	}
	if string(got) != "a\nb\n" {
		t.Errorf("want a\\nb\\n, got %q", got)
	}
}

func TestKubectlExecutor_ExecuteStream_Namespace(t *testing.T) {
	// Write a tiny shell script that prints all its positional parameters
	// literally so that flag-like args such as -n are visible in the output.
	// This lets us assert that ExecuteStream prepends the namespace flag.
	dir := t.TempDir()
	script := filepath.Join(dir, "print_args.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s ' \"$@\"\n"), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}

	e := &KubectlExecutor{kubectlPath: script}
	var mu sync.Mutex
	var got []byte
	code, err := e.ExecuteStream(context.Background(),
		[]string{"get", "pods"}, "default",
		func(stdout bool, data []byte) {
			mu.Lock()
			got = append(got, data...)
			mu.Unlock()
		})
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	if code != 0 {
		t.Errorf("want exit 0, got %d", code)
	}
	output := string(got)
	if !contains(output, "-n default") {
		t.Errorf("expected output to contain \"-n default\", got %q", output)
	}
}

// contains is a small helper to avoid importing strings in this file.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestKubectlExecutor_ExecuteStream_Cancel(t *testing.T) {
	e := &KubectlExecutor{kubectlPath: "/bin/sh"}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _ = e.ExecuteStream(ctx, []string{"-c", "sleep 10"}, "", func(bool, []byte) {})
	if time.Since(start) > 3*time.Second {
		t.Error("expected cancel to kill the process quickly")
	}
}

func TestExecuteInteractiveNoTTY_EchoAndExit(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	e := &KubectlExecutor{kubectlPath: "cat"}
	stdin := make(chan []byte, 1)
	var mu sync.Mutex
	var out []byte
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = e.ExecuteInteractiveNoTTY(ctx, nil, "", stdin,
			func(_ bool, b []byte) { mu.Lock(); out = append(out, b...); mu.Unlock() })
		close(done)
	}()

	stdin <- []byte("ping\n")
	// cat echoes stdin to stdout; wait up to 2s for the echo to arrive.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := string(out)
		mu.Unlock()
		if strings.Contains(got, "ping") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not observe echoed stdin")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Cancel the context and assert the function returns promptly (no orphaned goroutine/process).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteInteractiveNoTTY did not return after cancel (orphaned process)")
	}
}

func TestExecuteInteractive_EchoAndExit(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	e := &KubectlExecutor{kubectlPath: "cat"} // stand-in for kubectl under a PTY
	stdin := make(chan []byte, 1)
	resize := make(chan [2]uint16, 1)
	var mu sync.Mutex
	var out []byte
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = e.ExecuteInteractive(ctx, nil, "", 24, 80, stdin, resize,
			func(b []byte) { mu.Lock(); out = append(out, b...); mu.Unlock() })
		close(done)
	}()

	stdin <- []byte("ping\n")
	// cat echoes via the PTY; give it a moment, then cancel to end the process.
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		got := string(out)
		mu.Unlock()
		if strings.Contains(got, "ping") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not observe echoed stdin")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ExecuteInteractive did not return after cancel (orphaned process)")
	}
}

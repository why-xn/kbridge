package agent

import (
	"context"
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

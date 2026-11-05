package agent

import (
	"context"
	"net/http"
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
	e := windowedExecutor(t, "echo")

	ctx := context.Background()
	result := e.Execute(ctx, testID, []string{"hello", "world"}, "", 5*time.Second)

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
	e := windowedExecutor(t, "cat")

	ctx := context.Background()
	stdinContent := []byte("test input from stdin")
	result := e.ExecuteWithStdin(ctx, testID, []string{}, "", 5*time.Second, stdinContent)

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
	e := windowedExecutor(t, "echo")

	ctx := context.Background()
	result := e.ExecuteWithStdin(ctx, testID, []string{"no stdin"}, "", 5*time.Second, nil)

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
	e := windowedExecutor(t, "cat")

	ctx := context.Background()
	result := e.ExecuteWithStdin(ctx, testID, []string{}, "", 5*time.Second, []byte{})

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
	e := windowedExecutor(t, "sleep")

	ctx := context.Background()
	result := e.Execute(ctx, testID, []string{"10"}, "", 100*time.Millisecond)

	// Should have non-zero exit code due to timeout
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code due to timeout")
	}
}

func TestKubectlExecutor_Execute_NonZeroExit(t *testing.T) {
	// Use 'false' command which always exits with 1
	e := windowedExecutor(t, "false")

	ctx := context.Background()
	result := e.Execute(ctx, testID, []string{}, "", 5*time.Second)

	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", result.ExitCode)
	}
}

func TestNewKubectlExecutor(t *testing.T) {
	w, _, _ := windowFixture(t)
	e := NewKubectlExecutor(w)
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
	if e.window != w {
		t.Error("expected the executor to use the given window")
	}
	if e.kubectlPath != "kubectl" {
		t.Errorf("expected kubectlPath 'kubectl', got %q", e.kubectlPath)
	}
}

func TestKubectlExecutor_ExecuteStream(t *testing.T) {
	// Point the executor at /bin/sh and pass a script via args to emit two lines.
	e := windowedExecutor(t, "/bin/sh")
	var got []byte
	code, err := e.ExecuteStream(context.Background(),
		testID, []string{"-c", "printf 'a\\nb\\n'"}, "",
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

	e := windowedExecutor(t, script)
	var mu sync.Mutex
	var got []byte
	code, err := e.ExecuteStream(context.Background(),
		testID, []string{"get", "pods"}, "default",
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
	e := windowedExecutor(t, "/bin/sh")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	_, _ = e.ExecuteStream(ctx, testID, []string{"-c", "sleep 10"}, "", func(bool, []byte) {})
	if time.Since(start) > 3*time.Second {
		t.Error("expected cancel to kill the process quickly")
	}
}

func TestExecuteInteractiveNoTTY_EchoAndExit(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	e := windowedExecutor(t, "cat")
	stdin := make(chan []byte, 1)
	var mu sync.Mutex
	var out []byte
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = e.ExecuteInteractiveNoTTY(ctx, testID, nil, "", stdin,
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
	e := windowedExecutor(t, "cat") // stand-in for kubectl under a PTY
	stdin := make(chan []byte, 1)
	resize := make(chan [2]uint16, 1)
	var mu sync.Mutex
	var out []byte
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = e.ExecuteInteractive(ctx, testID, nil, "", 24, 80, stdin, resize,
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

var testID = Identity{RequestID: "req-test"}

// windowedExecutor runs prog as kubectl behind a live window, dropping the
// window's three leading args so stand-ins like echo and cat see only theirs.
func windowedExecutor(t *testing.T, prog string) *KubectlExecutor {
	t.Helper()
	w, _, _ := windowFixture(t)
	script := filepath.Join(t.TempDir(), "kubectl")
	body := "#!/bin/sh\nshift 3\nexec " + prog + " \"$@\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("writing stand-in kubectl: %v", err)
	}
	return &KubectlExecutor{kubectlPath: script, window: w}
}

// recorder is a stand-in kubectl that records its argv and env under dir,
// then prints a port-forward line so every entry point can complete.
func recorder(t *testing.T, dir string) string {
	t.Helper()
	script := filepath.Join(dir, "kubectl")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + dir + "/argv\n" +
		"env > " + dir + "/env\n" +
		"[ -f \"$KUBECONFIG\" ] && [ ! -s \"$KUBECONFIG\" ] && touch " + dir + "/kubeconfig-empty\n" +
		"echo 'Forwarding from 127.0.0.1:1 -> 80'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("writing recorder: %v", err)
	}
	return script
}

// entryPoints runs args through each way the executor starts kubectl and
// returns the error it reported.
var entryPoints = []struct {
	name string
	run  func(e *KubectlExecutor, args []string) error
}{
	{"Execute", func(e *KubectlExecutor, args []string) error {
		return e.Execute(context.Background(), testID, args, "", 5*time.Second).Error
	}},
	{"ExecuteWithStdin", func(e *KubectlExecutor, args []string) error {
		return e.ExecuteWithStdin(context.Background(), testID, args, "", 5*time.Second, []byte("in")).Error
	}},
	{"ExecuteStream", func(e *KubectlExecutor, args []string) error {
		_, err := e.ExecuteStream(context.Background(), testID, args, "", func(bool, []byte) {})
		return err
	}},
	{"ExecuteStreaming", func(e *KubectlExecutor, args []string) error {
		return e.ExecuteStreaming(context.Background(), testID, args, "", 5*time.Second, nil, nil).Error
	}},
	{"ExecuteInteractiveNoTTY", func(e *KubectlExecutor, args []string) error {
		_, err := e.ExecuteInteractiveNoTTY(context.Background(), testID, args, "", nil, func(bool, []byte) {})
		return err
	}},
	{"ExecuteInteractive", func(e *KubectlExecutor, args []string) error {
		_, err := e.ExecuteInteractive(context.Background(), testID, args, "", 24, 80, nil, nil, func([]byte) {})
		return err
	}},
	{"startKubectlPortForward", func(e *KubectlExecutor, args []string) error {
		_, cmd, release, err := e.startKubectlPortForward(context.Background(), testID, args[0], "", []uint32{80})
		if err != nil {
			return err
		}
		_ = cmd.Wait()
		release()
		return nil
	}},
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestExecutor_LeadsWithWindowArgs(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("POD_NAMESPACE", "kbridge")
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			dir := t.TempDir()
			e := windowedExecutor(t, "true")
			e.kubectlPath = recorder(t, dir)
			if err := ep.run(e, []string{"get", "pods"}); err != nil {
				t.Fatalf("run: %v", err)
			}
			argv := readLines(t, filepath.Join(dir, "argv"))
			for i, prefix := range []string{"--server=https://127.0.0.1:", "--certificate-authority=", "--token="} {
				if !strings.HasPrefix(argv[i], prefix) {
					t.Errorf("argv[%d] = %q, want prefix %q (argv %v)", i, argv[i], prefix, argv)
				}
			}
			assertScrubbedEnv(t, dir)
		})
	}
}

func assertScrubbedEnv(t *testing.T, dir string) {
	t.Helper()
	env := map[string]string{}
	for _, kv := range readLines(t, filepath.Join(dir, "env")) {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	for _, k := range []string{"KUBERNETES_SERVICE_HOST", "POD_NAMESPACE"} {
		if _, ok := env[k]; ok {
			t.Errorf("env carries %s", k)
		}
	}
	home := env["HOME"]
	if home == "" || home == os.Getenv("HOME") {
		t.Errorf("HOME = %q, want a per-command dir", home)
	}
	if env["KUBECONFIG"] != filepath.Join(home, "empty") || env["PATH"] != os.Getenv("PATH") {
		t.Errorf("KUBECONFIG = %q, PATH = %q", env["KUBECONFIG"], env["PATH"])
	}
	if _, err := os.Stat(filepath.Join(dir, "kubeconfig-empty")); err != nil {
		t.Error("KUBECONFIG was not an existing empty file")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("per-command HOME %s was not removed", home)
	}
}

func TestExecutor_RefusesUnsafeArgs(t *testing.T) {
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			dir := t.TempDir()
			e := windowedExecutor(t, "true")
			e.kubectlPath = recorder(t, dir)
			err := ep.run(e, []string{"--server=x", "get", "pods"})
			if err == nil || err.Error() != "flag --server is not allowed: kbridge manages the cluster connection" {
				t.Fatalf("err = %v, want the kubeargs refusal", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "argv")); !os.IsNotExist(err) {
				t.Error("kubectl was started for refused args")
			}
			e.window.mu.Lock()
			defer e.window.mu.Unlock()
			if n := len(e.window.nonces); n != 0 {
				t.Errorf("%d nonces registered after a refusal", n)
			}
		})
	}
}

func TestExecutor_RefusalExitCode(t *testing.T) {
	e := windowedExecutor(t, "true")
	if got := e.Execute(context.Background(), testID, []string{"--token=x", "get"}, "", time.Second); got.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", got.ExitCode)
	}
}

func TestExecutor_ReleasesNonce(t *testing.T) {
	w, client, _ := windowFixture(t)
	for _, ep := range entryPoints {
		t.Run(ep.name, func(t *testing.T) {
			dir := t.TempDir()
			e := &KubectlExecutor{kubectlPath: recorder(t, dir), window: w}
			if err := ep.run(e, []string{"get", "pods"}); err != nil {
				t.Fatalf("run: %v", err)
			}
			nonce := strings.TrimPrefix(readLines(t, filepath.Join(dir, "argv"))[2], "--token=")
			if r := do(t, client, http.MethodGet, serverOf(t, w, nonce)+"/api", nonce, nil); r.status != http.StatusUnauthorized {
				t.Errorf("status after run = %d, want 401", r.status)
			}
		})
	}
}

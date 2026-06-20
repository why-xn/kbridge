//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLoginRateLimited verifies that repeated failed login attempts on the same
// email bucket eventually return HTTP 429. It uses a unique email address so
// only its own bucket is throttled, not the shared admin bucket used by other
// tests. The harness rate limiter is configured at burst=5; we attempt up to 15
// times to be safe against transient scheduling delays.
func TestLoginRateLimited(t *testing.T) {
	url := *centralURL + "/auth/login"
	got429 := false
	for i := 0; i < 15; i++ {
		_, code := doJSON(t, http.MethodPost, url, "", map[string]string{
			"email":    "ratelimit@x",
			"password": "wrong",
		})
		if code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("expected a 429 after repeated bad logins, but none arrived in 15 attempts")
	}
}

// centralBinaryPath returns the path to the pre-built kbridge-central binary.
// It prefers *binDir (the flag used by the harness), falling back to the
// project-root relative bin/ directory.
func centralBinaryPath() string {
	if *binDir != "" {
		p := filepath.Join(*binDir, "kbridge-central")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Walk up to the project root from tests/e2e/
	p := filepath.Join("..", "..", "bin", "kbridge-central")
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// freePort asks the OS for a free TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// writeCentralConfig writes a minimal central.yaml to dir and returns its path.
func writeCentralConfig(t *testing.T, dir string, httpPort, grpcPort int, dbPath, jwtSecret, jwtSecretFile string) string {
	t.Helper()
	secretLine := ""
	switch {
	case jwtSecretFile != "":
		secretLine = fmt.Sprintf("  jwt_secret_file: %q", jwtSecretFile)
	default:
		secretLine = fmt.Sprintf("  jwt_secret: %q", jwtSecret)
	}

	cfg := fmt.Sprintf(`server:
  http_port: %d
  grpc_port: %d
database:
  driver: sqlite
  path: %q
auth:
%s
  access_token_expiry: 1h
  refresh_token_expiry: 168h
  admin_email: admin@test.local
  admin_password: test-admin-password
  admin_name: Admin
audit:
  retention_days: 1
  cleanup_interval: 24h
`, httpPort, grpcPort, dbPath, secretLine)

	cfgPath := filepath.Join(dir, "central.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// waitForHealth polls the /health endpoint until it returns 200 or the timeout expires.
func waitForHealth(url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("health endpoint %s did not become ready within %s", url, timeout)
}

// TestCentralSecretFromFile starts a real kbridge-central subprocess with
// KBRIDGE_JWT_SECRET_FILE pointing at a temp file containing a valid secret,
// and asserts that /health responds 200. No Kind cluster is needed.
func TestCentralSecretFromFile(t *testing.T) {
	binary := centralBinaryPath()
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("kbridge-central binary not found at %s; run make build first", binary)
	}

	dir := t.TempDir()

	// Write the secret to a file (>=32 chars).
	secret := "hardening-test-jwt-secret-for-file-boot-32chars+"
	secretFile := filepath.Join(dir, "jwt.secret")
	if err := os.WriteFile(secretFile, []byte(secret), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	httpPort := freePort(t)
	grpcPort := freePort(t)
	dbPath := filepath.Join(dir, "test.db")

	// Config has jwt_secret intentionally left empty; the secret comes from the file env var.
	cfgPath := writeCentralConfig(t, dir, httpPort, grpcPort, dbPath, "", "")
	// Overwrite: write config WITHOUT a jwt_secret so the env var is the only source.
	cfg := fmt.Sprintf(`server:
  http_port: %d
  grpc_port: %d
database:
  driver: sqlite
  path: %q
auth:
  access_token_expiry: 1h
  refresh_token_expiry: 168h
  admin_email: admin@test.local
  admin_password: test-admin-password
  admin_name: Admin
audit:
  retention_days: 1
  cleanup_interval: 24h
`, httpPort, grpcPort, dbPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("overwrite config: %v", err)
	}

	cmd := exec.Command(binary, "--config", cfgPath)
	cmd.Env = append(os.Environ(), "KBRIDGE_JWT_SECRET_FILE="+secretFile)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start central: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", httpPort)
	if err := waitForHealth(healthURL, 10*time.Second); err != nil {
		t.Fatalf("secret-from-file boot: %v", err)
	}
}

// TestCentralFailClosedShortSecret starts a kbridge-central subprocess with a
// too-short jwt_secret and asserts that the process exits with a non-zero code,
// i.e. the service refuses to start with a weak secret.
func TestCentralFailClosedShortSecret(t *testing.T) {
	binary := centralBinaryPath()
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("kbridge-central binary not found at %s; run make build first", binary)
	}

	dir := t.TempDir()

	httpPort := freePort(t)
	grpcPort := freePort(t)
	dbPath := filepath.Join(dir, "test.db")

	// "short" is only 5 characters — well below the required 32.
	cfgPath := writeCentralConfig(t, dir, httpPort, grpcPort, dbPath, "short", "")

	cmd := exec.Command(binary, "--config", cfgPath)
	// Ensure no env var overrides sneak in from the harness environment.
	env := make([]string, 0)
	for _, e := range os.Environ() {
		if len(e) >= 21 && e[:21] == "KBRIDGE_JWT_SECRET_FI" {
			continue
		}
		if len(e) >= 19 && e[:19] == "KBRIDGE_JWT_SECRET=" {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env

	err := cmd.Run()
	if err == nil {
		t.Fatal("expected kbridge-central to exit non-zero with a too-short jwt_secret, but it exited 0")
	}
	// Any non-zero exit (exit error or killed) is the correct fail-closed behaviour.
}

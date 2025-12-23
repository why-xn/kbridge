//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	centralURL  = flag.String("central-url", "http://localhost:8080", "Central service URL")
	clusterName = flag.String("cluster-name", "mk8s-e2e-test", "Kind cluster name")
	binDir      = flag.String("bin-dir", "../../bin", "Directory containing binaries")
)

// TestMain handles flag parsing
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// Helper functions

func getCLIPath() string {
	return filepath.Join(*binDir, "mk8s")
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.Command(getCLIPath(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if exitError, ok := err.(*exec.ExitError); ok {
		exitCode = exitError.ExitCode()
	} else if err != nil {
		t.Logf("Command error: %v", err)
		exitCode = -1
	}

	return stdout.String(), stderr.String(), exitCode
}

func httpGet(t *testing.T, url string) ([]byte, int) {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("HTTP GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response: %v", err)
	}

	return body, resp.StatusCode
}

// Test: Central service health check
func TestCentralServiceHealth(t *testing.T) {
	url := fmt.Sprintf("%s/health", *centralURL)
	body, statusCode := httpGet(t, url)

	if statusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", statusCode)
	}

	var resp map[string]string
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %q", resp["status"])
	}
}

// Test: Agent registers with central
func TestAgentRegistration(t *testing.T) {
	url := fmt.Sprintf("%s/api/v1/clusters", *centralURL)
	body, statusCode := httpGet(t, url)

	if statusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", statusCode)
	}

	var resp struct {
		Clusters []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"clusters"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Find the test cluster
	found := false
	for _, cluster := range resp.Clusters {
		if cluster.Name == *clusterName {
			found = true
			if cluster.Status != "connected" {
				t.Errorf("Expected cluster status 'connected', got %q", cluster.Status)
			}
			break
		}
	}

	if !found {
		t.Errorf("Cluster %q not found in registered clusters", *clusterName)
	}
}

// Test: Agent heartbeat keeps connection alive
func TestAgentHeartbeat(t *testing.T) {
	// Wait a bit to allow heartbeat to occur
	time.Sleep(5 * time.Second)

	url := fmt.Sprintf("%s/api/v1/clusters", *centralURL)
	body, statusCode := httpGet(t, url)

	if statusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", statusCode)
	}

	var resp struct {
		Clusters []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"clusters"`
	}

	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify cluster is still connected (heartbeat is working)
	for _, cluster := range resp.Clusters {
		if cluster.Name == *clusterName {
			if cluster.Status != "connected" {
				t.Errorf("Cluster should still be connected after heartbeat, got %q", cluster.Status)
			}
			return
		}
	}

	t.Errorf("Cluster %q not found after heartbeat check", *clusterName)
}

// Test: CLI clusters list
func TestCLIClustersList(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "clusters", "list")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	// Check output contains expected headers
	if !strings.Contains(stdout, "NAME") {
		t.Error("Expected output to contain 'NAME' header")
	}

	if !strings.Contains(stdout, "STATUS") {
		t.Error("Expected output to contain 'STATUS' header")
	}

	// Check cluster appears in list
	if !strings.Contains(stdout, *clusterName) {
		t.Errorf("Expected output to contain cluster name %q", *clusterName)
	}

	if !strings.Contains(stdout, "connected") {
		t.Error("Expected output to show 'connected' status")
	}
}

// Test: CLI clusters use
func TestCLIClustersUse(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "clusters", "use", *clusterName)

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	if !strings.Contains(stdout, "Switched to cluster") {
		t.Errorf("Expected 'Switched to cluster' message, got: %s", stdout)
	}

	if !strings.Contains(stdout, *clusterName) {
		t.Errorf("Expected cluster name %q in output", *clusterName)
	}
}

// Test: CLI status shows current cluster
func TestCLIStatus(t *testing.T) {
	// First set the cluster
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	stdout, stderr, exitCode := runCLI(t, "status")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	if !strings.Contains(stdout, "mk8s Status") {
		t.Error("Expected 'mk8s Status' header")
	}

	if !strings.Contains(stdout, *clusterName) {
		t.Errorf("Expected current cluster %q in status output", *clusterName)
	}

	if !strings.Contains(stdout, "Connected") {
		t.Error("Expected 'Connected' status")
	}
}

// Test: kubectl get pods -A
func TestKubectlGetPodsAllNamespaces(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	stdout, stderr, exitCode := runCLI(t, "kubectl", "get", "pods", "-A")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	// Should contain namespace header
	if !strings.Contains(stdout, "NAMESPACE") {
		t.Error("Expected output to contain 'NAMESPACE' header")
	}

	// Should show kube-system pods (these always exist in Kind)
	if !strings.Contains(stdout, "kube-system") {
		t.Error("Expected output to contain 'kube-system' namespace")
	}
}

// Test: kubectl get nodes
func TestKubectlGetNodes(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	stdout, stderr, exitCode := runCLI(t, "kubectl", "get", "nodes")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	// Should contain NAME header
	if !strings.Contains(stdout, "NAME") {
		t.Error("Expected output to contain 'NAME' header")
	}

	// Should contain STATUS header
	if !strings.Contains(stdout, "STATUS") {
		t.Error("Expected output to contain 'STATUS' header")
	}

	// Kind node name contains control-plane
	if !strings.Contains(stdout, "control-plane") {
		t.Error("Expected output to contain 'control-plane' node")
	}

	// Node should be Ready
	if !strings.Contains(stdout, "Ready") {
		t.Error("Expected node to be in 'Ready' status")
	}
}

// Test: kubectl alias (mk8s k)
func TestKubectlAlias(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	stdout, stderr, exitCode := runCLI(t, "k", "get", "namespaces")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	// Should contain default namespace
	if !strings.Contains(stdout, "default") {
		t.Error("Expected output to contain 'default' namespace")
	}

	// Should contain kube-system
	if !strings.Contains(stdout, "kube-system") {
		t.Error("Expected output to contain 'kube-system' namespace")
	}
}

// Test: kubectl with non-zero exit code
func TestKubectlNonZeroExit(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	// Try to get a non-existent resource
	_, _, exitCode = runCLI(t, "kubectl", "get", "pod", "nonexistent-pod-12345")

	if exitCode == 0 {
		t.Error("Expected non-zero exit code for non-existent resource")
	}
}

// Test: clusters list alias
func TestClustersListAlias(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "clusters", "ls")

	if exitCode != 0 {
		t.Fatalf("Expected exit code 0, got %d. Stderr: %s", exitCode, stderr)
	}

	// Same output as list
	if !strings.Contains(stdout, *clusterName) {
		t.Errorf("Expected output to contain cluster name %q", *clusterName)
	}
}

// Test: help commands work
func TestHelpCommands(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "mk8s is a command-line interface"},
		{[]string{"clusters", "--help"}, "Manage cluster connections"},
		{[]string{"clusters", "list", "--help"}, "List available clusters"},
		{[]string{"kubectl", "--help"}, "Execute kubectl commands"},
		{[]string{"status", "--help"}, "Show current connection status"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			stdout, _, exitCode := runCLI(t, tt.args...)

			if exitCode != 0 {
				t.Errorf("Expected exit code 0, got %d", exitCode)
			}

			if !strings.Contains(stdout, tt.want) {
				t.Errorf("Expected output to contain %q", tt.want)
			}
		})
	}
}

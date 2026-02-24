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
	clusterName = flag.String("cluster-name", "kbridge-e2e-test", "Kind cluster name")
	binDir      = flag.String("bin-dir", "../../bin", "Directory containing binaries")
)

var authToken string

// TestMain handles flag parsing
func TestMain(m *testing.M) {
	flag.Parse()
	authToken = loginForTests()
	os.Exit(m.Run())
}

func loginForTests() string {
	body, _ := json.Marshal(map[string]string{
		"email":    "admin@e2e.test",
		"password": "e2e-password",
	})

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(fmt.Sprintf("%s/auth/login", *centralURL), "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to login for tests: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Login failed with status %d: %s\n", resp.StatusCode, string(respBody))
		os.Exit(1)
	}

	var loginResp struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to decode login response: %v\n", err)
		os.Exit(1)
	}

	return loginResp.AccessToken
}

// Helper functions

func getCLIPath() string {
	return filepath.Join(*binDir, "kbridge")
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

func httpGetAuth(t *testing.T, url, token string) ([]byte, int) {
	t.Helper()

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
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
	body, statusCode := httpGetAuth(t, url, authToken)

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
	body, statusCode := httpGetAuth(t, url, authToken)

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

	if !strings.Contains(stdout, "kbridge Status") {
		t.Error("Expected 'kbridge Status' header")
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

// Test: kubectl alias (kbridge k)
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
		{[]string{"--help"}, "kbridge is a command-line interface"},
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

// =============================================================================
// kubectl edit E2E Tests
// =============================================================================

// runCLIWithEnv runs the CLI with additional environment variables.
func runCLIWithEnv(t *testing.T, env map[string]string, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.Command(getCLIPath(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Copy current environment and add custom vars
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

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

// createEditorScript creates a temporary script that acts as an editor.
// The script modifies the file according to the provided sed-like expression.
func createEditorScript(t *testing.T, sedExpr string) string {
	t.Helper()

	// Create a shell script that modifies the file
	script := fmt.Sprintf(`#!/bin/bash
sed -i '%s' "$1"
`, sedExpr)

	tmpFile, err := os.CreateTemp("", "mock-editor-*.sh")
	if err != nil {
		t.Fatalf("Failed to create editor script: %v", err)
	}

	if _, err := tmpFile.WriteString(script); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write editor script: %v", err)
	}
	tmpFile.Close()

	// Make the script executable
	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to make editor script executable: %v", err)
	}

	return tmpFile.Name()
}

// createNoOpEditorScript creates a script that doesn't modify the file (simulates cancel).
func createNoOpEditorScript(t *testing.T) string {
	t.Helper()

	script := `#!/bin/bash
# No-op editor - exits without modifying the file
exit 0
`
	tmpFile, err := os.CreateTemp("", "noop-editor-*.sh")
	if err != nil {
		t.Fatalf("Failed to create noop editor script: %v", err)
	}

	if _, err := tmpFile.WriteString(script); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write noop editor script: %v", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to make noop editor script executable: %v", err)
	}

	return tmpFile.Name()
}

// createInvalidYAMLEditorScript creates a script that introduces invalid YAML.
func createInvalidYAMLEditorScript(t *testing.T) string {
	t.Helper()

	// This script introduces invalid YAML by adding unmatched braces
	script := `#!/bin/bash
echo "invalid: yaml: {{{{" >> "$1"
`
	tmpFile, err := os.CreateTemp("", "invalid-yaml-editor-*.sh")
	if err != nil {
		t.Fatalf("Failed to create invalid yaml editor script: %v", err)
	}

	if _, err := tmpFile.WriteString(script); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to write invalid yaml editor script: %v", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to make invalid yaml editor script executable: %v", err)
	}

	return tmpFile.Name()
}

// Test: kubectl edit configmap - basic edit workflow
func TestKubectlEditConfigMap(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	configMapName := "test-edit-cm"
	namespace := "default"

	// Step 1: Create a configmap to edit
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  key1: original-value
  key2: another-value
`, configMapName, namespace)

	// Create temp file for apply
	tmpFile, err := os.CreateTemp("", "test-cm-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	// Apply the configmap
	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create configmap: %s, stderr: %s", stdout, stderr)
	}

	// Cleanup at end of test
	defer func() {
		runCLI(t, "kubectl", "delete", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
	}()

	// Step 2: Create editor script that changes "original-value" to "edited-value"
	editorScript := createEditorScript(t, "s/original-value/edited-value/g")
	defer os.Remove(editorScript)

	// Step 3: Run kubectl edit with our mock editor
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "configmap", configMapName, "-n", namespace)

	if exitCode != 0 {
		t.Fatalf("kubectl edit failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Step 4: Verify the change was applied
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get configmap: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "edited-value") {
		t.Errorf("Expected configmap to contain 'edited-value', got: %s", stdout)
	}

	if strings.Contains(stdout, "original-value") {
		t.Errorf("Expected 'original-value' to be replaced, but it still exists")
	}
}

// Test: kubectl edit deployment
func TestKubectlEditDeployment(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	deploymentName := "test-edit-deploy"
	namespace := "default"

	// Step 1: Create a simple deployment
	createYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: test-edit
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test-edit
  template:
    metadata:
      labels:
        app: test-edit
    spec:
      containers:
      - name: nginx
        image: nginx:1.19
        ports:
        - containerPort: 80
`, deploymentName, namespace)

	tmpFile, err := os.CreateTemp("", "test-deploy-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create deployment: %s, stderr: %s", stdout, stderr)
	}

	// Cleanup at end of test
	defer func() {
		runCLI(t, "kubectl", "delete", "deployment", deploymentName, "-n", namespace, "--ignore-not-found")
	}()

	// Wait a moment for the deployment to be ready
	time.Sleep(2 * time.Second)

	// Step 2: Create editor script that changes nginx:1.19 to nginx:1.21
	editorScript := createEditorScript(t, "s/nginx:1.19/nginx:1.21/g")
	defer os.Remove(editorScript)

	// Step 3: Run kubectl edit
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "deployment", deploymentName, "-n", namespace)

	if exitCode != 0 {
		t.Fatalf("kubectl edit failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Step 4: Verify the change was applied
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "deployment", deploymentName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get deployment: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "nginx:1.21") {
		t.Errorf("Expected deployment to contain 'nginx:1.21', got: %s", stdout)
	}
}

// Test: kubectl edit service
func TestKubectlEditService(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	serviceName := "test-edit-svc"
	namespace := "default"

	// Step 1: Create a service
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    app: test-edit-svc
spec:
  selector:
    app: test-edit-svc
  ports:
  - port: 80
    targetPort: 8080
  type: ClusterIP
`, serviceName, namespace)

	tmpFile, err := os.CreateTemp("", "test-svc-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create service: %s, stderr: %s", stdout, stderr)
	}

	// Cleanup at end of test
	defer func() {
		runCLI(t, "kubectl", "delete", "service", serviceName, "-n", namespace, "--ignore-not-found")
	}()

	// Step 2: Create editor script that changes targetPort from 8080 to 9090
	editorScript := createEditorScript(t, "s/targetPort: 8080/targetPort: 9090/g")
	defer os.Remove(editorScript)

	// Step 3: Run kubectl edit
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "service", serviceName, "-n", namespace)

	if exitCode != 0 {
		t.Fatalf("kubectl edit failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Step 4: Verify the change was applied
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "service", serviceName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get service: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "targetPort: 9090") {
		t.Errorf("Expected service to contain 'targetPort: 9090', got: %s", stdout)
	}
}

// Test: kubectl edit with type/name format
func TestKubectlEditWithSlashFormat(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	configMapName := "test-edit-slash-cm"
	namespace := "default"

	// Create a configmap
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  mykey: myvalue
`, configMapName, namespace)

	tmpFile, err := os.CreateTemp("", "test-slash-cm-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create configmap: %s, stderr: %s", stdout, stderr)
	}

	defer func() {
		runCLI(t, "kubectl", "delete", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
	}()

	// Create editor script
	editorScript := createEditorScript(t, "s/myvalue/newvalue/g")
	defer os.Remove(editorScript)

	// Run kubectl edit with type/name format
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", fmt.Sprintf("configmap/%s", configMapName), "-n", namespace)

	if exitCode != 0 {
		t.Fatalf("kubectl edit with slash format failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Verify the change was applied
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get configmap: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "newvalue") {
		t.Errorf("Expected configmap to contain 'newvalue', got: %s", stdout)
	}
}

// Test: kubectl edit cancel (no changes)
func TestKubectlEditCancel(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	configMapName := "test-edit-cancel-cm"
	namespace := "default"

	// Create a configmap
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  preserved: original
`, configMapName, namespace)

	tmpFile, err := os.CreateTemp("", "test-cancel-cm-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create configmap: %s, stderr: %s", stdout, stderr)
	}

	defer func() {
		runCLI(t, "kubectl", "delete", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
	}()

	// Get resource version before edit
	stdout, _, _ = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.metadata.resourceVersion}")
	resourceVersionBefore := strings.TrimSpace(stdout)

	// Create no-op editor that doesn't modify the file
	editorScript := createNoOpEditorScript(t)
	defer os.Remove(editorScript)

	// Run kubectl edit with no-op editor
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "configmap", configMapName, "-n", namespace)

	if exitCode != 0 {
		t.Fatalf("kubectl edit failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Should see "Edit cancelled" message
	if !strings.Contains(stdout, "Edit cancelled") {
		t.Errorf("Expected 'Edit cancelled' message, got: %s", stdout)
	}

	// Get resource version after edit - should be unchanged
	stdout, _, _ = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.metadata.resourceVersion}")
	resourceVersionAfter := strings.TrimSpace(stdout)

	if resourceVersionBefore != resourceVersionAfter {
		t.Errorf("Resource version changed despite no modifications. Before: %s, After: %s", resourceVersionBefore, resourceVersionAfter)
	}
}

// Test: kubectl edit non-existent resource
func TestKubectlEditResourceNotFound(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	// Create a dummy editor (won't be used since resource doesn't exist)
	editorScript := createNoOpEditorScript(t)
	defer os.Remove(editorScript)

	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode := runCLIWithEnv(t, env, "kubectl", "edit", "configmap", "nonexistent-resource-xyz123", "-n", "default")

	if exitCode == 0 {
		t.Errorf("Expected non-zero exit code for non-existent resource")
	}

	// Should contain error message about not found
	output := stdout + stderr
	if !strings.Contains(strings.ToLower(output), "not found") && !strings.Contains(strings.ToLower(output), "notfound") {
		t.Errorf("Expected 'not found' error message, got: %s", output)
	}
}

// Test: kubectl edit with invalid YAML should fail
func TestKubectlEditInvalidYAML(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	configMapName := "test-edit-invalid-yaml-cm"
	namespace := "default"

	// Create a configmap
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  valid: data
`, configMapName, namespace)

	tmpFile, err := os.CreateTemp("", "test-invalid-yaml-cm-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create configmap: %s, stderr: %s", stdout, stderr)
	}

	defer func() {
		runCLI(t, "kubectl", "delete", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
	}()

	// Create editor script that introduces invalid YAML
	editorScript := createInvalidYAMLEditorScript(t)
	defer os.Remove(editorScript)

	// Run kubectl edit with invalid YAML editor
	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "configmap", configMapName, "-n", namespace)

	// The edit should fail because the YAML is invalid
	if exitCode == 0 {
		t.Errorf("Expected non-zero exit code for invalid YAML, but got 0")
	}

	// Verify the original resource is unchanged
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get configmap: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "valid: data") {
		t.Errorf("Expected original data to be preserved, got: %s", stdout)
	}
}

// Test: kubectl edit with namespace flag variations
func TestKubectlEditNamespaceFlags(t *testing.T) {
	// Ensure cluster is selected
	_, _, exitCode := runCLI(t, "clusters", "use", *clusterName)
	if exitCode != 0 {
		t.Fatal("Failed to set cluster")
	}

	configMapName := "test-edit-ns-flag-cm"
	namespace := "default"

	// Create a configmap
	createYAML := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s
  namespace: %s
data:
  test: value1
`, configMapName, namespace)

	tmpFile, err := os.CreateTemp("", "test-ns-flag-cm-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(createYAML)
	tmpFile.Close()

	stdout, stderr, exitCode := runCLI(t, "kubectl", "apply", "-f", tmpFile.Name())
	if exitCode != 0 {
		t.Fatalf("Failed to create configmap: %s, stderr: %s", stdout, stderr)
	}

	defer func() {
		runCLI(t, "kubectl", "delete", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
	}()

	// Test with --namespace= format
	editorScript := createEditorScript(t, "s/value1/value2/g")
	defer os.Remove(editorScript)

	env := map[string]string{"KUBE_EDITOR": editorScript}
	stdout, stderr, exitCode = runCLIWithEnv(t, env, "kubectl", "edit", "configmap", configMapName, fmt.Sprintf("--namespace=%s", namespace))

	if exitCode != 0 {
		t.Fatalf("kubectl edit with --namespace= failed with exit code %d. Stdout: %s, Stderr: %s", exitCode, stdout, stderr)
	}

	// Verify the change was applied
	stdout, stderr, exitCode = runCLI(t, "kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	if exitCode != 0 {
		t.Fatalf("Failed to get configmap: %s, stderr: %s", stdout, stderr)
	}

	if !strings.Contains(stdout, "value2") {
		t.Errorf("Expected configmap to contain 'value2', got: %s", stdout)
	}
}

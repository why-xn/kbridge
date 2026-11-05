//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	developerBinding = "kbridge-developer-edit"
	// syncWait covers one heartbeat (30s) plus a reconcile.
	syncWait = 60 * time.Second
	// developerAccess is the developer's clusterAccess entry in the harness
	// policy; sync tests add entries after it.
	developerAccess = "        clusterRole: edit\n        namespaces: [\"e2e-dev\"]\n"
)

// TestRBACSync_BindsFromPolicy checks the agent binds the developer as the
// harness policy's clusterAccess says, and that the user can then act.
func TestRBACSync_BindsFromPolicy(t *testing.T) {
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	waitInSync(t)
	stdout, stderr, code := runCLI(t, "admin", "rbac", "status")
	if code != 0 || !strings.Contains(stdout, "in sync") {
		t.Fatalf("rbac status: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	useDeveloperConfig(t)
	if stdout, stderr, code := runCLI(t, "get", "pods", "-n", devNamespace); code != 0 {
		t.Fatalf("get pods as developer: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// TestRBACSync_RevertsDrift adds a stranger to a synced binding by hand; the
// agent's watch must put the binding back.
func TestRBACSync_RevertsDrift(t *testing.T) {
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	patch := `[{"op":"add","path":"/subjects/-","value":{"kind":"User","name":"stranger","apiGroup":"rbac.authorization.k8s.io"}}]`
	if out, err := exec.Command("kubectl", "patch", "rolebinding", developerBinding, "-n", devNamespace,
		"--type=json", "-p", patch).CombinedOutput(); err != nil {
		t.Fatalf("patch binding: %v: %s", err, out)
	}
	if !strings.Contains(bindingUsers(t, devNamespace, developerBinding), "stranger") {
		t.Fatal("precondition: patch did not add the stranger")
	}
	ok := poll(10*time.Second, func() bool {
		return !strings.Contains(bindingUsers(t, devNamespace, developerBinding), "stranger")
	})
	if !ok {
		t.Fatalf("stranger still bound after 10s: %s", bindingUsers(t, devNamespace, developerBinding))
	}
}

// TestRBACSync_ReportsCeiling maps the developer to `admin`, which the e2e
// ceiling leaves out: the cluster refuses that binding, the status shows the
// 403, and the other bindings stay.
func TestRBACSync_ReportsCeiling(t *testing.T) {
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	withPolicyEntry(t, `      - clusters: ["*"]
        clusterRole: admin
        namespaces: ["e2e-dev"]
`)
	want := "binding kbridge-developer-admin:"
	errLine := waitSyncError(t, want)
	if !strings.Contains(errLine, "403") {
		t.Errorf("error %q does not carry the 403", errLine)
	}
	stdout, _, _ := runCLI(t, "admin", "rbac", "status")
	if !strings.Contains(stdout, "drift") || !strings.Contains(stdout, want) {
		t.Errorf("rbac status lacks the drift and the error: %s", stdout)
	}
	if exec.Command("kubectl", "get", "rolebinding", "kbridge-developer-admin", "-n", devNamespace).Run() == nil {
		t.Error("binding to admin exists; the ceiling should have refused it")
	}
	assertOtherBindingsKept(t)
}

// TestRBACSync_NeverProtectedNamespace names the agent's own namespace in
// policy; the agent refuses it and writes nothing there.
func TestRBACSync_NeverProtectedNamespace(t *testing.T) {
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	withPolicyEntry(t, fmt.Sprintf(`      - clusters: ["*"]
        clusterRole: view
        namespaces: [%q]
`, agentNamespace))
	waitSyncError(t, fmt.Sprintf(`binding kbridge-developer-view: namespace %q is reserved for the kbridge agent`, agentNamespace))
	if exec.Command("kubectl", "get", "rolebinding", "kbridge-developer-view", "-n", agentNamespace).Run() == nil {
		t.Error("binding written in the agent's namespace")
	}
	assertOtherBindingsKept(t)
}

func assertOtherBindingsKept(t *testing.T) {
	t.Helper()
	if users := bindingUsers(t, devNamespace, developerBinding); !strings.Contains(users, developerUser) {
		t.Errorf("%s lost the developer: %q", developerBinding, users)
	}
	if users := bindingUsers(t, "", "kbridge-admin-cluster-admin"); !strings.Contains(users, "kbridge:admin@e2e.test") {
		t.Errorf("kbridge-admin-cluster-admin lost the admin: %q", users)
	}
}

// withPolicyEntry adds a clusterAccess entry to the developer role in the
// watched policy file, and on cleanup restores the file and waits for sync.
func withPolicyEntry(t *testing.T, entry string) {
	t.Helper()
	original, err := os.ReadFile(*rbacPolicyPath)
	if err != nil {
		t.Fatalf("read policy %s: %v (set -rbac-policy)", *rbacPolicyPath, err)
	}
	if !strings.Contains(string(original), developerAccess) {
		t.Fatalf("policy has no developer clusterAccess entry to extend:\n%s", original)
	}
	t.Cleanup(func() { restorePolicy(t, original) })
	writePolicy(t, strings.Replace(string(original), developerAccess, developerAccess+entry, 1))
}

// restorePolicy writes the original policy back and waits until the cluster
// is in sync with it, so later tests see the harness bindings.
func restorePolicy(t *testing.T, original []byte) {
	t.Helper()
	writePolicy(t, string(original))
	waitInSync(t)
}

func writePolicy(t *testing.T, policy string) {
	t.Helper()
	if err := os.WriteFile(*rbacPolicyPath, []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	if err := signalControlPlaneReload(); err != nil {
		t.Fatalf("signal reload: %v", err)
	}
}

// waitDeveloperBound waits until the agent has bound the developer in e2e-dev.
func waitDeveloperBound(t *testing.T) {
	t.Helper()
	if !poll(syncWait, func() bool { return strings.Contains(bindingUsers(t, devNamespace, developerBinding), developerUser) }) {
		t.Fatalf("%s in %s does not list %s after %s", developerBinding, devNamespace, developerUser, syncWait)
	}
}

// bindingUsers returns the subject names of a RoleBinding, or of a
// ClusterRoleBinding when namespace is empty; "" when it does not exist.
func bindingUsers(t *testing.T, namespace, name string) string {
	t.Helper()
	args := []string{"get", "clusterrolebinding", name, "-o", "jsonpath={.subjects[*].name}"}
	if namespace != "" {
		args = []string{"get", "rolebinding", name, "-n", namespace, "-o", "jsonpath={.subjects[*].name}"}
	}
	out, _ := exec.Command("kubectl", args...).Output()
	return string(out)
}

type syncRow struct {
	Cluster string   `json:"cluster"`
	State   string   `json:"state"`
	Errors  []string `json:"errors"`
}

// clusterSync returns this cluster's row from the admin RBAC status.
func clusterSync(t *testing.T) syncRow {
	t.Helper()
	body, code := httpGetAuth(t, *controlPlaneURL+"/api/v1/admin/rbac/status", authToken)
	if code != http.StatusOK {
		t.Fatalf("rbac status: %d %s", code, body)
	}
	var resp struct {
		Clusters []syncRow `json:"clusters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode rbac status: %v", err)
	}
	for _, r := range resp.Clusters {
		if r.Cluster == *clusterName {
			return r
		}
	}
	return syncRow{}
}

// waitInSync waits for three consecutive in-sync reads, so a report for a
// policy that was just replaced cannot pass for the current one.
func waitInSync(t *testing.T) {
	t.Helper()
	streak := 0
	ok := poll(2*syncWait, func() bool {
		if clusterSync(t).State == "in sync" {
			streak++
		} else {
			streak = 0
		}
		return streak >= 3
	})
	if !ok {
		t.Fatalf("cluster not in sync after %s: %+v", 2*syncWait, clusterSync(t))
	}
}

// waitSyncError waits for a reported sync error containing want and returns it.
func waitSyncError(t *testing.T, want string) string {
	t.Helper()
	var found string
	poll(syncWait, func() bool {
		for _, e := range clusterSync(t).Errors {
			if strings.Contains(e, want) {
				found = e
				return true
			}
		}
		return false
	})
	if found == "" {
		t.Fatalf("no sync error containing %q after %s: %+v", want, syncWait, clusterSync(t))
	}
	return found
}

// poll calls cond every second until it is true or timeout elapses.
func poll(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Second)
	}
}

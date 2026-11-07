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

// TestRBACSync_RequiresPodSecurity grants the developer edit in e2e-nopsa,
// which does not enforce Pod Security: the agent refuses until the namespace
// is labelled baseline, then binds, and revokes once the label is removed.
func TestRBACSync_RequiresPodSecurity(t *testing.T) {
	const ns = "e2e-nopsa"
	// useDeveloperConfig moves HOME; pin kubectl to the harness kubeconfig first.
	t.Setenv("KUBECONFIG", hostKubeconfig())
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	withNopsaEdit(t, ns)
	want := `binding kbridge-developer-edit: namespace "e2e-nopsa" must enforce pod-security baseline or restricted before edit can be granted there (kubectl label ns e2e-nopsa pod-security.kubernetes.io/enforce=baseline)`
	waitSyncError(t, want)
	if stdout, _, _ := runCLI(t, "admin", "rbac", "status"); !strings.Contains(stdout, want) {
		t.Errorf("rbac status lacks the refusal: %s", stdout)
	}
	if bindingUsers(t, ns, developerBinding) != "" {
		t.Fatalf("%s exists in %s before the namespace enforces pod security", developerBinding, ns)
	}
	t.Cleanup(func() { _ = exec.Command("kubectl", "label", "ns", ns, podSecurityLabel+"-").Run() })
	if out, err := exec.Command("kubectl", "label", "ns", ns, podSecurityLabel+"=baseline").CombinedOutput(); err != nil {
		t.Fatalf("label namespace: %v: %s", err, out)
	}
	if !poll(30*time.Second, func() bool { return strings.Contains(bindingUsers(t, ns, developerBinding), developerUser) }) {
		t.Fatalf("%s not bound in %s 30s after labelling: %+v", developerBinding, ns, clusterSync(t))
	}
	t.Cleanup(func() { _ = exec.Command("kubectl", "delete", "configmap", "x", "-n", ns, "--ignore-not-found").Run() })
	useDeveloperConfig(t)
	if stdout, stderr, code := runCLI(t, "create", "configmap", "x", "-n", ns); code != 0 {
		t.Fatalf("create configmap as developer: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if out, err := exec.Command("kubectl", "label", "ns", ns, podSecurityLabel+"-").CombinedOutput(); err != nil {
		t.Fatalf("unlabel namespace: %v: %s", err, out)
	}
	if !poll(30*time.Second, func() bool { return bindingUsers(t, ns, developerBinding) == "" }) {
		t.Fatalf("%s still in %s 30s after the label was removed", developerBinding, ns)
	}
	if stdout, stderr, code := runCLI(t, "create", "configmap", "y", "-n", ns); code == 0 {
		t.Fatalf("create configmap as developer after revoke succeeded: stdout %q, stderr %q", stdout, stderr)
	}
}

const podSecurityLabel = "pod-security.kubernetes.io/enforce"

// withNopsaEdit lets the developer act in ns and binds it edit there; cleanup
// restores the policy and waits for sync.
func withNopsaEdit(t *testing.T, ns string) {
	t.Helper()
	const devRule = `        namespaces: ["e2e-dev"]
        resources: ["*"]
`
	withPolicyEntry(t, fmt.Sprintf("      - clusters: [\"*\"]\n        clusterRole: edit\n        namespaces: [%q]\n", ns))
	policy, err := os.ReadFile(*rbacPolicyPath)
	if err != nil || !strings.Contains(string(policy), devRule) {
		t.Fatalf("policy has no developer rule for e2e-dev (err %v):\n%s", err, policy)
	}
	writePolicy(t, strings.Replace(string(policy), devRule, fmt.Sprintf("        namespaces: [\"e2e-dev\", %q]\n        resources: [\"*\"]\n", ns), 1))
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

const (
	grantNamespace = "e2e-grant"
	grantBinding   = "kbridge-prodlike-edit-grant"
	// grantGuardrail makes writes in e2e-grant need an approved grant, so the
	// same grant opens both kbridge and, through the gated binding, the cluster.
	grantGuardrail = `guardrails:
  - name: e2e-grant-writes-need-approval
    match:
      namespaces: ["e2e-grant"]
      verbs: ["create", "delete", "apply"]
    action: require-approval
    message: "e2e-grant needs an approved grant"
`
)

// TestRBACSync_GrantAddsAndRemovesUser approves a one-minute grant for the
// developer on e2e-grant: the agent binds them, the write works, and once the
// grant expires the binding empties again and kbridge refuses the write.
func TestRBACSync_GrantAddsAndRemovesUser(t *testing.T) {
	ensureUser(t, developerEmail)
	waitDeveloperBound(t)
	probe := []string{"delete", "configmap", "e2e-nonexistent", "-n", grantNamespace}
	installPolicy(t, harnessRoles+grantGuardrail, probe, func(c int) bool { return c == http.StatusForbidden })
	if users := bindingUsers(t, grantNamespace, grantBinding); strings.Contains(users, developerUser) {
		t.Fatalf("%s already lists %s before any grant", grantBinding, developerUser)
	}

	devToken := loginAs(t, developerEmail, edgePassword)
	g := requestGrantAPI(t, devToken, *clusterName, grantNamespace, "1m", "e2e grant-gated binding")
	if _, code := decideGrantAPI(t, g.ID, "approve", nil); code != http.StatusOK {
		t.Fatalf("approve grant: status %d", code)
	}
	approved := time.Now()
	if !poll(syncWait, func() bool { return strings.Contains(bindingUsers(t, grantNamespace, grantBinding), developerUser) }) {
		t.Fatalf("%s does not list %s within %s of approval", grantBinding, developerUser, syncWait)
	}

	useDeveloperConfig(t)
	stdout, stderr, code := runCLI(t, "create", "configmap", "e2e-grant-cm", "-n", grantNamespace)
	if code != 0 {
		t.Fatalf("create configmap with a live grant: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	defer exec.Command("kubectl", "delete", "configmap", "e2e-grant-cm", "-n", grantNamespace, "--ignore-not-found").Run()

	time.Sleep(time.Until(approved.Add(time.Minute)))
	if !poll(syncWait, func() bool { return !strings.Contains(bindingUsers(t, grantNamespace, grantBinding), developerUser) }) {
		t.Fatalf("%s still lists %s %s after the grant expired", grantBinding, developerUser, syncWait)
	}
	_, stderr, code = runCLI(t, "delete", "configmap", "e2e-grant-cm", "-n", grantNamespace)
	if code == 0 || !strings.Contains(stderr, "kb request") {
		t.Fatalf("delete after expiry: exit %d, stderr %q; want a refusal asking for an approved grant", code, stderr)
	}
}

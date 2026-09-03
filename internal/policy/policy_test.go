package policy

import (
	"os"
	"path/filepath"
	"testing"
)

const samplePolicy = `
default: viewer
roles:
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
  - name: developer
    rules:
      - clusters: ["dev-*", "staging"]
        namespaces: ["*"]
        resources: ["pods", "deployments"]
        verbs: ["get", "list", "logs", "apply"]
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get", "list"]
bindings:
  - subject: alice@corp.com
    roles: ["admin"]
  - subject: "*@dev.corp.com"
    roles: ["developer"]
`

func mustParse(t *testing.T, data string) *Policy {
	t.Helper()
	p, err := Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p
}

func TestParsePolicy(t *testing.T) {
	p := mustParse(t, samplePolicy)
	if p.Default != "viewer" {
		t.Errorf("default = %q, want viewer", p.Default)
	}
	if len(p.Roles) != 3 || len(p.Bindings) != 2 {
		t.Fatalf("got %d roles, %d bindings", len(p.Roles), len(p.Bindings))
	}
}

func TestParsePolicy_RejectsUnknownRoleRefs(t *testing.T) {
	bad := `
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
bindings:
  - subject: bob@corp.com
    roles: ["ghost"]
`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for binding referencing unknown role")
	}
}

func TestParsePolicy_RejectsUnknownDefault(t *testing.T) {
	bad := `
default: nope
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for unknown default role")
	}
}

func TestPolicy_Allows(t *testing.T) {
	p := mustParse(t, samplePolicy)

	tests := []struct {
		name    string
		subject string
		req     AccessRequest
		want    bool
	}{
		{"admin can delete anywhere", "alice@corp.com", AccessRequest{Cluster: "prod-1", Namespace: "kube-system", Resource: "pods", Verb: "delete"}, true},
		{"dev wildcard subject gets developer", "carol@dev.corp.com", AccessRequest{Cluster: "dev-2", Namespace: "default", Resource: "pods", Verb: "logs"}, true},
		{"developer denied on prod", "carol@dev.corp.com", AccessRequest{Cluster: "prod-1", Namespace: "default", Resource: "pods", Verb: "logs"}, false},
		{"developer denied delete verb", "carol@dev.corp.com", AccessRequest{Cluster: "dev-2", Namespace: "default", Resource: "pods", Verb: "delete"}, false},
		{"unbound user falls back to viewer (read allowed)", "stranger@x.com", AccessRequest{Cluster: "prod-1", Namespace: "default", Resource: "pods", Verb: "get"}, true},
		{"unbound user denied writes via viewer default", "stranger@x.com", AccessRequest{Cluster: "prod-1", Namespace: "default", Resource: "pods", Verb: "delete"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.allows(tt.subject, tt.req); got != tt.want {
				t.Errorf("allows(%q, %+v) = %v, want %v", tt.subject, tt.req, got, tt.want)
			}
		})
	}
}

func TestPolicy_NoDefault_UnboundDenied(t *testing.T) {
	noDefault := `
roles:
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
bindings:
  - subject: alice@corp.com
    roles: ["admin"]
`
	p := mustParse(t, noDefault)
	if p.allows("nobody@x.com", AccessRequest{Cluster: "c", Namespace: "default", Resource: "pods", Verb: "get"}) {
		t.Error("expected deny for unbound user when no default role")
	}
}

func TestPolicyEngine_ReloadHotSwaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rbac.yaml")
	// Start restrictive: viewer default only.
	restrictive := `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
`
	if err := os.WriteFile(path, []byte(restrictive), 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngineFromFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	req := AccessRequest{Cluster: "prod", Namespace: "default", Resource: "pods", Verb: "delete"}
	if engine.Allows("u@x.com", req) {
		t.Fatal("delete should be denied under restrictive policy")
	}

	// Rewrite the file to grant admin to everyone, then reload.
	permissive := `
default: admin
roles:
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
`
	if err := os.WriteFile(path, []byte(permissive), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !engine.Allows("u@x.com", req) {
		t.Error("delete should be allowed after hot-reload to permissive policy")
	}
}

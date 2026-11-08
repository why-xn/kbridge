package policy

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const accessPolicy = `
default: viewer
roles:
  - name: developer
    clusterAccess:
      - clusters: ["*"]
        clusterRole: edit
        namespaces: [dev, staging]
      - clusters: ["prod-*"]
        clusterRole: view
        namespaces: [prod]
      - clusters: ["prod-*"]
        clusterRole: edit
        namespaces: [prod]
        requiresGrant: true
  - name: viewer
    clusterAccess:
      - clusters: ["*"]
        clusterRole: view
        clusterWide: true
bindings:
  - subject: alice
    roles: [developer]
`

func engineFor(t *testing.T, doc string) *Engine {
	t.Helper()
	e := &Engine{}
	e.current.Store(mustParse(t, doc))
	return e
}

func TestDesiredBindings(t *testing.T) {
	alice := []string{"kbridge:alice"}
	// bound alice does not inherit the default viewer role; only unbound bob does
	viewer := DesiredBinding{Name: "kbridge-viewer-view", ClusterRole: "view", Users: []string{"kbridge:bob"}}
	edit := func(ns string) DesiredBinding {
		return DesiredBinding{Name: "kbridge-developer-edit", Namespace: ns, ClusterRole: "edit", Users: alice}
	}
	prod := DesiredBinding{Name: "kbridge-developer-view", Namespace: "prod", ClusterRole: "view", Users: alice}
	gated := DesiredBinding{Name: "kbridge-developer-edit-grant", Namespace: "prod", ClusterRole: "edit", Users: []string{}}
	tests := []struct {
		name, cluster string
		want          []DesiredBinding
	}{
		{"dev", "dev-1", []DesiredBinding{viewer, edit("dev"), edit("staging")}},
		{"prod", "prod-1", []DesiredBinding{viewer, edit("dev"), gated, prod, edit("staging")}},
	}
	e := engineFor(t, accessPolicy)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.DesiredBindings(tt.cluster, []string{"alice", "bob"}, nil); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDesiredBindings_MergesDuplicatesAndNilSafe(t *testing.T) {
	doc := `
roles:
  - name: r
    clusterAccess:
      - {clusters: ["*"], clusterRole: view, namespaces: [a]}
      - {clusters: ["*"], clusterRole: view, namespaces: [a]}
bindings:
  - {subject: "*", roles: [r]}
`
	got := engineFor(t, doc).DesiredBindings("c", []string{"x", "x"}, nil)
	if len(got) != 1 || len(got[0].Users) != 1 {
		t.Errorf("want one merged binding, got %+v", got)
	}
	for _, e := range []*Engine{nil, {}} {
		if got := e.DesiredBindings("c", []string{"x"}, nil); got == nil || len(got) != 0 {
			t.Errorf("want empty non-nil, got %#v", got)
		}
	}
}

func TestBindingsVersion(t *testing.T) {
	e := engineFor(t, accessPolicy)
	v := func(s ...string) string { return BindingsVersion(e.DesiredBindings("dev-1", s, nil)) }
	if v("alice", "bob") != v("bob", "alice") {
		t.Error("version depends on subject order")
	}
	if v("alice", "bob") == v("alice", "bob", "carol") {
		t.Error("version unchanged after adding a user")
	}
}

func TestParse_RejectsBadClusterAccess(t *testing.T) {
	tests := []struct{ name, entry, want string }{
		{"no clusterRole", `{clusters: ["*"], clusterWide: true}`, "role r: clusterAccess entry needs a clusterRole"},
		{"neither", `{clusters: ["*"], clusterRole: view}`, "role r: clusterAccess entry needs namespaces or clusterWide, not both"},
		{"both", `{clusters: ["*"], clusterRole: view, clusterWide: true, namespaces: [a]}`, "role r: clusterAccess entry needs namespaces or clusterWide, not both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte("roles:\n  - name: r\n    clusterAccess:\n      - " + tt.entry + "\n"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want %q", err, tt.want)
			}
		})
	}
}

// TestDesiredBindings_GrantGated: a requiresGrant entry binds the grant
// holders who hold the role, and exists empty when nobody does.
func TestDesiredBindings_GrantGated(t *testing.T) {
	e := engineFor(t, accessPolicy)
	find := func(bs []DesiredBinding) []string {
		for _, b := range bs {
			if b.Name == "kbridge-developer-edit-grant" && b.Namespace == "prod" {
				return b.Users
			}
		}
		t.Fatalf("no grant binding in %+v", bs)
		return nil
	}
	tests := []struct {
		name   string
		grants GrantLookup
		want   []string
	}{
		{"no lookup", nil, []string{}},
		{"no holders", func(string, string) []string { return nil }, []string{}},
		{"alice holds a grant", func(c, ns string) []string {
			if c == "prod-1" && ns == "prod" {
				return []string{"alice"}
			}
			return nil
		}, []string{"kbridge:alice"}},
		{"a holder outside the role is not bound", func(string, string) []string { return []string{"bob", "alice"} }, []string{"kbridge:alice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := find(e.DesiredBindings("prod-1", []string{"alice", "bob"}, tt.grants))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("users = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParse_RejectsBindingNameCollisions(t *testing.T) {
	entry := func(role, clusterRole string, gated bool) string {
		return fmt.Sprintf("  - name: %s\n    clusterAccess:\n      - {clusters: [\"*\"], clusterRole: %s, namespaces: [prod], requiresGrant: %v}\n", role, clusterRole, gated)
	}
	tests := []struct {
		name string
		doc  string
		want string
	}{
		{"gated and ungated with the same name", "roles:\n" + entry("oncall", "edit", true) + entry("oncall-edit", "grant", false), "roles oncall and oncall-edit would both bind kbridge-oncall-edit-grant"},
		{"two ungated roles with the same name", "roles:\n" + entry("a-b", "c", false) + entry("a", "b-c", false), "roles a-b and a would both bind kbridge-a-b-c"},
		{"same role bound twice is fine", "roles:\n  - name: r\n    clusterAccess:\n      - {clusters: [\"*\"], clusterRole: view, namespaces: [a]}\n      - {clusters: [\"*\"], clusterRole: view, namespaces: [b]}\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if tt.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Errorf("got %v, want %q", err, tt.want)
			}
		})
	}
}

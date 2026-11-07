package policy

import (
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
	both := []string{"kbridge:alice", "kbridge:bob"}
	alice := []string{"kbridge:alice"}
	viewer := DesiredBinding{Name: "kbridge-viewer-view", ClusterRole: "view", Users: both}
	edit := func(ns string) DesiredBinding {
		return DesiredBinding{Name: "kbridge-developer-edit", Namespace: ns, ClusterRole: "edit", Users: alice}
	}
	prod := DesiredBinding{Name: "kbridge-developer-view", Namespace: "prod", ClusterRole: "view", Users: alice}
	tests := []struct {
		name, cluster string
		want          []DesiredBinding
	}{
		{"dev", "dev-1", []DesiredBinding{viewer, edit("dev"), edit("staging")}},
		{"prod", "prod-1", []DesiredBinding{viewer, edit("dev"), prod, edit("staging")}},
	}
	e := engineFor(t, accessPolicy)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.DesiredBindings(tt.cluster, []string{"alice", "bob"}); !reflect.DeepEqual(got, tt.want) {
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
	got := engineFor(t, doc).DesiredBindings("c", []string{"x", "x"})
	if len(got) != 1 || len(got[0].Users) != 1 {
		t.Errorf("want one merged binding, got %+v", got)
	}
	for _, e := range []*Engine{nil, {}} {
		if got := e.DesiredBindings("c", []string{"x"}); got == nil || len(got) != 0 {
			t.Errorf("want empty non-nil, got %#v", got)
		}
	}
}

func TestBindingsVersion(t *testing.T) {
	e := engineFor(t, accessPolicy)
	v := func(s ...string) string { return BindingsVersion(e.DesiredBindings("dev-1", s)) }
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

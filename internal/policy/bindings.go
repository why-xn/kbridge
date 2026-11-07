package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// ClusterAccess grants a role a Kubernetes ClusterRole on matching clusters.
type ClusterAccess struct {
	Clusters    []string `yaml:"clusters"`
	ClusterRole string   `yaml:"clusterRole"`
	// Namespaces are literal names, not globs.
	Namespaces  []string `yaml:"namespaces"`
	ClusterWide bool     `yaml:"clusterWide"`
}

// DesiredBinding is a RoleBinding (or ClusterRoleBinding when Namespace is
// empty) a cluster should carry.
type DesiredBinding struct {
	Name        string
	Namespace   string
	ClusterRole string
	Users       []string // sorted, "kbridge:" prefixed
}

func validateClusterAccess(r Role) error {
	for _, ca := range r.ClusterAccess {
		if ca.ClusterRole == "" {
			return fmt.Errorf("role %s: clusterAccess entry needs a clusterRole", r.Name)
		}
		if ca.ClusterWide == (len(ca.Namespaces) > 0) {
			return fmt.Errorf("role %s: clusterAccess entry needs namespaces or clusterWide, not both", r.Name)
		}
	}
	return nil
}

// DesiredBindings computes the bindings cluster should carry for subjects
// (bare emails), sorted by namespace then name.
func (e *Engine) DesiredBindings(cluster string, subjects []string) []DesiredBinding {
	out := []DesiredBinding{}
	if e == nil {
		return out
	}
	p := e.current.Load()
	if p == nil {
		return out
	}
	return p.desiredBindings(cluster, subjects)
}

func (p *Policy) desiredBindings(cluster string, subjects []string) []DesiredBinding {
	members := p.roleMembers(subjects)
	acc := map[[2]string]*DesiredBinding{}
	for _, role := range p.Roles {
		for _, ca := range role.ClusterAccess {
			if len(members[role.Name]) == 0 || !anyMatch(ca.Clusters, cluster) {
				continue
			}
			for _, ns := range ca.bindingNamespaces() {
				mergeBinding(acc, role.Name, ns, ca.ClusterRole, members[role.Name])
			}
		}
	}
	return sortedBindings(acc)
}

// roleMembers maps role name to the sorted, deduplicated kbridge users in it.
func (p *Policy) roleMembers(subjects []string) map[string][]string {
	sets := map[string]map[string]bool{}
	for _, s := range subjects {
		for _, r := range p.rolesFor(s) {
			if sets[r] == nil {
				sets[r] = map[string]bool{}
			}
			sets[r]["kbridge:"+s] = true
		}
	}
	out := map[string][]string{}
	for r, set := range sets {
		out[r] = sortedKeys(set)
	}
	return out
}

func (ca ClusterAccess) bindingNamespaces() []string {
	if ca.ClusterWide {
		return []string{""}
	}
	return ca.Namespaces
}

func mergeBinding(acc map[[2]string]*DesiredBinding, role, ns, clusterRole string, users []string) {
	name := "kbridge-" + role + "-" + clusterRole
	b := acc[[2]string{ns, name}]
	if b == nil {
		b = &DesiredBinding{Name: name, Namespace: ns, ClusterRole: clusterRole}
		acc[[2]string{ns, name}] = b
	}
	set := map[string]bool{}
	for _, u := range append(b.Users, users...) {
		set[u] = true
	}
	b.Users = sortedKeys(set)
}

func sortedBindings(acc map[[2]string]*DesiredBinding) []DesiredBinding {
	out := make([]DesiredBinding, 0, len(acc))
	for _, b := range acc {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BindingsVersion is the sha256 hex of the canonical JSON of a sorted list.
func BindingsVersion(b []DesiredBinding) string {
	data, _ := json.Marshal(b) // plain structs and strings cannot fail to marshal
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

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
	// RequiresGrant binds only role members who hold an approved, unexpired
	// grant for the cluster and namespace, in a binding named "...-grant".
	RequiresGrant bool `yaml:"requiresGrant"`
}

// GrantLookup answers which subjects (bare emails) hold an approved, unexpired
// grant covering cluster and namespace; "" is the cluster-wide namespace.
type GrantLookup func(cluster, namespace string) []string

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

// validateBindingNames refuses two entries that would share a binding name
// but mean different things ("oncall" + edit gated by a grant collides with
// "oncall-edit" + grant). Merged, the ungated members would get the gated
// ClusterRole without a grant.
func validateBindingNames(p *Policy) error {
	type meaning struct {
		role, clusterRole string
		gated             bool
	}
	seen := map[string]meaning{}
	for _, r := range p.Roles {
		for _, ca := range r.ClusterAccess {
			name, m := "kbridge-"+r.Name+ca.nameSuffix(), meaning{r.Name, ca.ClusterRole, ca.RequiresGrant}
			if prev, ok := seen[name]; ok && prev != m {
				return fmt.Errorf("roles %s and %s would both bind %s: rename one", prev.role, r.Name, name)
			}
			seen[name] = m
		}
	}
	return nil
}

// DesiredBindings computes the bindings cluster should carry for subjects
// (bare emails), sorted by namespace then name. grants may be nil, in which
// case requiresGrant entries bind nobody.
func (e *Engine) DesiredBindings(cluster string, subjects []string, grants GrantLookup) []DesiredBinding {
	out := []DesiredBinding{}
	if e == nil {
		return out
	}
	p := e.current.Load()
	if p == nil {
		return out
	}
	return p.desiredBindings(cluster, subjects, grants)
}

func (p *Policy) desiredBindings(cluster string, subjects []string, grants GrantLookup) []DesiredBinding {
	members := p.roleMembers(subjects)
	acc := map[[2]string]*DesiredBinding{}
	for _, role := range p.Roles {
		for _, ca := range role.ClusterAccess {
			if len(members[role.Name]) == 0 || !anyMatch(ca.Clusters, cluster) {
				continue
			}
			for _, ns := range ca.bindingNamespaces() {
				users := ca.boundUsers(members[role.Name], cluster, ns, grants)
				mergeBinding(acc, role.Name+ca.nameSuffix(), ns, ca.ClusterRole, users)
			}
		}
	}
	return sortedBindings(acc)
}

// boundUsers is the role's members, narrowed to grant holders for a gated
// entry. A gated binding with no holders still exists, empty, so the cluster
// shows what a grant would unlock.
func (ca ClusterAccess) boundUsers(members []string, cluster, ns string, grants GrantLookup) []string {
	if !ca.RequiresGrant {
		return members
	}
	holders := map[string]bool{}
	if grants != nil {
		for _, h := range grants(cluster, ns) {
			holders["kbridge:"+h] = true
		}
	}
	out := []string{}
	for _, m := range members {
		if holders[m] {
			out = append(out, m)
		}
	}
	return out
}

func (ca ClusterAccess) nameSuffix() string {
	if ca.RequiresGrant {
		return "-" + ca.ClusterRole + "-grant"
	}
	return "-" + ca.ClusterRole
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

func mergeBinding(acc map[[2]string]*DesiredBinding, name, ns, clusterRole string, users []string) {
	name = "kbridge-" + name
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

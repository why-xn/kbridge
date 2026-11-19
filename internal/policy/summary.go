package policy

import (
	"sort"
	"strings"
)

// RolesFor returns the roles that apply to subject and whether they come from
// the default role. A nil engine (RBAC off) has none.
func (e *Engine) RolesFor(subject string) (roles []string, isDefault bool) {
	if e == nil {
		return nil, false
	}
	p := e.current.Load()
	roles = p.rolesFor(subject)
	if len(roles) == 1 && p.Default == roles[0] {
		isDefault = true
		for _, b := range p.Bindings {
			if MatchPattern(b.Subject, subject) {
				isDefault = false
			}
		}
	}
	return roles, isDefault
}

// AccessSummary is one cluster-level line of what a subject may do.
type AccessSummary struct {
	Cluster    string   `json:"cluster"`    // known cluster name, or "*" for every cluster
	Namespaces []string `json:"namespaces"` // ["*"] means all namespaces
	Level      string   `json:"level"`      // "read-only" | "read-write"
}

// readOnlyVerbs leaves out "auth": kubectl auth reconcile writes RBAC objects.
var readOnlyVerbs = map[string]bool{"get": true, "list": true, "watch": true, "describe": true,
	"logs": true, "top": true, "explain": true, "version": true, "api-resources": true}

func ruleLevel(r Rule) string {
	for _, v := range r.Verbs {
		if !readOnlyVerbs[strings.ToLower(strings.TrimSpace(v))] {
			return "read-write"
		}
	}
	return "read-only"
}

// AccessFor summarises what subject's roles allow, resolved against the known
// cluster names. A nil engine returns nil; no matching role returns empty.
func (e *Engine) AccessFor(subject string, clusters []string) []AccessSummary {
	if e == nil {
		return nil
	}
	p := e.current.Load()
	ns := accessSet{}
	for _, name := range p.rolesFor(subject) {
		for _, role := range p.Roles {
			if role.Name != name {
				continue
			}
			for _, r := range role.Rules {
				for _, c := range ruleClusters(r, clusters) {
					ns.add(c, r)
				}
			}
		}
	}
	return sortedAccess(ns)
}

// ruleClusters returns "*" for a rule on every cluster, else the known
// clusters it matches. A rule that can allow nothing yields none.
func ruleClusters(r Rule, clusters []string) []string {
	if len(r.Namespaces) == 0 || len(r.Verbs) == 0 || len(r.Resources) == 0 {
		return nil
	}
	if anyEqual(r.Clusters, "*") {
		return []string{"*"}
	}
	var out []string
	for _, c := range clusters {
		if anyMatch(r.Clusters, c) {
			out = append(out, c)
		}
	}
	return out
}

type accessSet map[accessKey]map[string]bool

func (s accessSet) add(cluster string, r Rule) {
	k := accessKey{cluster, ruleLevel(r)}
	if s[k] == nil {
		s[k] = map[string]bool{}
	}
	for _, n := range r.Namespaces {
		s[k][n] = true
	}
}

type accessKey struct{ cluster, level string }

func anyEqual(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// sortedAccess merges namespaces per (cluster, level) and orders the result:
// read-write first, then "*" before names, then by name.
func sortedAccess(ns accessSet) []AccessSummary {
	out := []AccessSummary{}
	for k, set := range ns {
		names := []string{}
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		if set["*"] {
			names = []string{"*"}
		}
		out = append(out, AccessSummary{k.cluster, names, k.level})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Level != b.Level {
			return a.Level == "read-write"
		}
		if (a.Cluster == "*") != (b.Cluster == "*") {
			return a.Cluster == "*"
		}
		return a.Cluster < b.Cluster
	})
	return out
}

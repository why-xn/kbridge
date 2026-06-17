package central

import (
	"strings"
)

// AccessRequest describes a single kubectl action a user wants to perform.
type AccessRequest struct {
	Cluster   string
	Namespace string
	Resource  string
	Verb      string
}

// matchPattern reports whether value matches pattern, where '*' is a wildcard
// matching any (possibly empty) sequence of characters. A plain pattern with no
// '*' must match value exactly.
func matchPattern(pattern, value string) bool {
	// Two-pointer wildcard match with backtracking on the last '*'.
	var p, v int
	star, mark := -1, 0
	for v < len(value) {
		if p < len(pattern) && (pattern[p] == value[v]) {
			p++
			v++
		} else if p < len(pattern) && pattern[p] == '*' {
			star = p
			mark = v
			p++
		} else if star != -1 {
			p = star + 1
			mark++
			v = mark
		} else {
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// verbAllowed reports whether verb is contained in a comma-separated verbs
// spec. A "*" entry allows any verb. Matching is case-insensitive.
func verbAllowed(verbs, verb string) bool {
	for _, v := range strings.Split(verbs, ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.EqualFold(v, verb) {
			return true
		}
	}
	return false
}

// podScopedVerbs are kubectl verbs that operate on pods implicitly rather than
// naming a resource type as their first argument.
var podScopedVerbs = map[string]bool{
	"logs": true, "exec": true, "attach": true, "port-forward": true, "cp": true,
}

// parseAccessRequest derives an AccessRequest from a kubectl command (the args
// after "kubectl"). The verb is the first token; the resource is the first
// non-flag token after it (with any "/name" suffix stripped), or "pods" for
// pod-scoped verbs. The namespace comes from -n/--namespace, "*" for
// --all-namespaces/-A, else fallbackNamespace, else "default".
func parseAccessRequest(cluster string, command []string, fallbackNamespace string) AccessRequest {
	req := AccessRequest{Cluster: cluster, Namespace: fallbackNamespace}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if len(command) == 0 {
		return req
	}
	req.Verb = command[0]

	args := command[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-A" || a == "--all-namespaces":
			req.Namespace = "*"
		case a == "-n" || a == "--namespace":
			if i+1 < len(args) {
				req.Namespace = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--namespace="):
			req.Namespace = strings.TrimPrefix(a, "--namespace=")
		case strings.HasPrefix(a, "-n="):
			req.Namespace = strings.TrimPrefix(a, "-n=")
		}
	}

	if podScopedVerbs[req.Verb] {
		req.Resource = "pods"
	} else {
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				req.Resource = a
				break
			}
		}
	}
	if idx := strings.IndexByte(req.Resource, '/'); idx >= 0 {
		req.Resource = req.Resource[:idx]
	}
	return req
}

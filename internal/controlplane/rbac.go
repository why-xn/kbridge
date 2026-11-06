package controlplane

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/why-xn/kbridge/internal/kubeargs"
	"gopkg.in/yaml.v3"
)

// AccessRequest describes a single kubectl action a user wants to perform.
type AccessRequest struct {
	Cluster   string
	Namespace string
	Resource  string
	Verb      string
	// FlagHidResource is set when an unknown long flag before the resource
	// took the next token as its value, so the resource may be missing.
	FlagHidResource bool
}

// DenialMessage tells email what was refused and who can unblock it. It names
// only the verb, resource, namespace and cluster, never the whole command.
func (a AccessRequest) DenialMessage(email string) string {
	what := a.Verb
	if a.Resource != "" {
		what += " " + a.Resource
	}
	where := fmt.Sprintf("in namespace %q", a.Namespace)
	if a.Namespace == "*" {
		where = "in all namespaces"
	}
	msg := fmt.Sprintf("permission denied: %s %s on cluster %q is not allowed for %s; ask a kbridge admin to extend your role",
		what, where, a.Cluster, email)
	if a.Resource == "" && a.FlagHidResource {
		msg += " (write flags as --flag=value or put them after the resource)"
	}
	return msg
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
// positional token after it, skipping flag values (with any "/name" suffix
// stripped), or "pods" for pod-scoped verbs. The namespace comes from the last
// -n/--namespace, "*" for --all-namespaces/-A, else fallbackNamespace, else
// "default".
func parseAccessRequest(cluster string, command []string, fallbackNamespace string) AccessRequest {
	req := AccessRequest{Cluster: cluster, Namespace: fallbackNamespace}
	if req.Namespace == "" {
		req.Namespace = "default"
	}
	if len(command) == 0 {
		return req
	}
	req.Verb = command[0]
	positional, all := scanArgs(&req, command[1:])
	if all { // kubectl's -A wins over any -n
		req.Namespace = "*"
	}
	req.Resource = positional
	if podScopedVerbs[req.Verb] {
		req.Resource = "pods"
	}
	if idx := strings.IndexByte(req.Resource, '/'); idx >= 0 {
		req.Resource = req.Resource[:idx]
	}
	return req
}

// scanArgs sets req.Namespace from the namespace flags and returns the first
// positional token and whether -A was given. A flag value is never taken for
// the resource: "get -l pods secrets" reads secrets.
func scanArgs(req *AccessRequest, args []string) (positional string, all bool) {
	for i := 0; i < len(args); i++ {
		a, next := args[i], ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case !strings.HasPrefix(a, "-"):
			if positional == "" {
				positional = a
			}
		case a == "--all-namespaces":
			all = true
		case a == "--namespace":
			req.Namespace = next
			i++
		case strings.HasPrefix(a, "--namespace="):
			req.Namespace = strings.TrimPrefix(a, "--namespace=")
		case strings.HasPrefix(a, "--"):
			if kubeargs.TakesValue(a) {
				i++
			} else if positional == "" && unknownTakesNext(a, next) {
				req.FlagHidResource = true
				i++
			}
		default:
			ns, clusterAll, takesNext := shortFlag(a, next, req.Verb)
			all = all || clusterAll
			if ns != "" {
				req.Namespace = ns
			}
			if takesNext {
				i++
			}
		}
	}
	return positional, all
}

// unknownTakesNext fails closed on a long flag kbridge does not know: before
// the resource it is read as taking a value, so "get --sort-by pods secrets"
// reads secrets. It never takes a flag, so "--no-headers -n dev" keeps -n.
func unknownTakesNext(flag, next string) bool {
	return !strings.Contains(flag, "=") && !kubeargs.IsBoolFlag(flag) && next != "" && !strings.HasPrefix(next, "-")
}

// shortFlag reads a single-dash cluster such as -n, -nteam-b, -n=x or -itn:
// the namespace it sets, whether it holds -A, and whether its value letter
// takes the next token.
func shortFlag(tok, next, verb string) (ns string, all, takesNext bool) {
	for i := 1; i < len(tok); i++ {
		c := tok[i]
		all = all || c == 'A'
		if (c == 'f' && verb == "logs") || !kubeargs.TakesValue("-"+string(c)) {
			continue // logs -f is --follow
		}
		value := strings.TrimPrefix(tok[i+1:], "=")
		takesNext = tok[i+1:] == ""
		if takesNext {
			value = next
		}
		if c == 'n' {
			ns = value
		}
		return ns, all, takesNext
	}
	return "", all, false
}

// manifestVerbs take objects from stdin manifests, which name their own
// namespaces.
var manifestVerbs = map[string]bool{
	"apply": true, "create": true, "replace": true, "delete": true, "patch": true,
}

// accessRequests lists every check a command needs: the flag namespace, plus
// each namespace its stdin manifests write to. The flag namespace is skipped
// only when every manifest names its own, since kubectl then never uses it.
func accessRequests(cluster string, req ExecRequest) []AccessRequest {
	base := parseAccessRequest(cluster, req.Command, req.Namespace)
	if !manifestVerbs[base.Verb] || req.Stdin == "" {
		return []AccessRequest{base}
	}
	namespaces, unnamed := manifestNamespaces([]byte(req.Stdin))
	var out []AccessRequest
	if unnamed {
		out = append(out, base)
	}
	for _, ns := range namespaces {
		r := base
		r.Namespace = ns
		out = append(out, r)
	}
	return out
}

// manifestNamespaces returns the distinct metadata.namespace values of the
// YAML documents in stdin, and whether any document (or none at all) lacks
// one. It stops at the first decode error: kubectl rejects bad YAML itself.
// shortcut: List kinds are not unpacked, add items[] if users send them.
func manifestNamespaces(stdin []byte) (namespaces []string, unnamed bool) {
	dec := yaml.NewDecoder(bytes.NewReader(stdin))
	seen := map[string]bool{}
	docs := 0
	for {
		var doc struct {
			Metadata struct{ Namespace string } `yaml:"metadata"`
		}
		if dec.Decode(&doc) != nil {
			return namespaces, unnamed || docs == 0
		}
		docs++
		ns := doc.Metadata.Namespace
		if ns == "" {
			unnamed = true
		} else if !seen[ns] {
			seen[ns] = true
			namespaces = append(namespaces, ns)
		}
	}
}

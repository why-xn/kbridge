package policy

import (
	"fmt"
	"strings"
)

// Action is what a guardrail does to a command it matches.
type Action string

const (
	// ActionDeny rejects the command outright.
	ActionDeny Action = "deny"
	// ActionRequireReason rejects the command unless the caller supplied a
	// justification, which is then recorded in the audit log.
	ActionRequireReason Action = "require-reason"
	// ActionRequireApproval rejects the command unless someone else has already
	// approved a time-boxed grant covering it. A reason is self-asserted; an
	// approval is not, which is what makes this the stronger control.
	ActionRequireApproval Action = "require-approval"
)

// Reason length bounds. A reason must be substantial enough to be useful in an
// audit trail ("INC-4521" is exactly the minimum) and short enough to store.
const (
	MinReasonLength = 8
	MaxReasonLength = 512
)

// valid reports whether the action is one this engine knows how to apply.
func (a Action) valid() bool {
	switch a {
	case ActionDeny, ActionRequireReason, ActionRequireApproval:
		return true
	}
	return false
}

// Match narrows a guardrail to a subset of commands. Unlike a Rule, an omitted
// or empty list means "any value" rather than "no value", so a guardrail that
// specifies only verbs applies across every cluster, namespace, and resource.
type Match struct {
	Clusters   []string `yaml:"clusters"`
	Namespaces []string `yaml:"namespaces"`
	Resources  []string `yaml:"resources"`
	Verbs      []string `yaml:"verbs"`
	// Args matches raw command tokens: every pattern here must match at least
	// one token, so ["--all"] fires only on a command carrying that flag.
	Args []string `yaml:"args"`
	// ArgsNot is the negation: the guardrail is skipped when any pattern here
	// matches a token, which expresses "unless --dry-run was passed".
	ArgsNot []string `yaml:"args_not"`
}

// Guardrail blocks or gates a command that the role rules would otherwise
// allow. Guardrails are evaluated in file order and the first match wins.
type Guardrail struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Match       Match    `yaml:"match"`
	Action      Action   `yaml:"action"`
	Message     string   `yaml:"message"`
	Exempt      []string `yaml:"exempt"`
}

// matches reports whether the guardrail applies to req.
func (m Match) matches(req AccessRequest) bool {
	return matchesAny(m.Clusters, req.Cluster) &&
		matchesAny(m.Namespaces, req.Namespace) &&
		resourceMatchesAny(m.Resources, req.Resource) &&
		verbMatchesAny(m.Verbs, req.Verb) &&
		allArgsPresent(m.Args, req.Args) &&
		noArgPresent(m.ArgsNot, req.Args)
}

// exempts reports whether subject is excused from this guardrail.
func (g Guardrail) exempts(subject string) bool {
	for _, pattern := range g.Exempt {
		if MatchPattern(pattern, subject) {
			return true
		}
	}
	return false
}

// explain returns the guardrail's message, falling back to a generic one.
func (g Guardrail) explain() string {
	if g.Message != "" {
		return g.Message
	}
	switch g.Action {
	case ActionRequireReason:
		return "this command requires a reason"
	case ActionRequireApproval:
		return "this command requires an approved access grant"
	default:
		return "this command is blocked by policy"
	}
}

// matchesAny reports whether value matches any pattern. An empty pattern list
// matches everything.
func matchesAny(patterns []string, value string) bool {
	return len(patterns) == 0 || anyMatch(patterns, value)
}

// resourceMatchesAny is matchesAny for resources, additionally tolerating the
// singular/plural spellings kubectl treats as the same thing, so a guardrail
// written for "deployments" also catches "delete deployment api".
//
// This widening is deliberately confined to guardrails, which only take access
// away. Role rules stay exact: there, a spelling that matched more than the
// author meant would grant more than they intended.
func resourceMatchesAny(patterns []string, resource string) bool {
	if len(patterns) == 0 {
		return true
	}
	if anyMatch(patterns, resource) {
		return true
	}
	for _, alias := range resourceAliases(resource) {
		if anyMatch(patterns, alias) {
			return true
		}
	}
	return false
}

// resourceAliases returns the other spellings of a resource name. It covers the
// regular English forms kubectl uses; an irregular resource can always be listed
// explicitly in the guardrail.
func resourceAliases(r string) []string {
	switch {
	case r == "":
		return nil
	case strings.HasSuffix(r, "ies"):
		return []string{strings.TrimSuffix(r, "ies") + "y"}
	case strings.HasSuffix(r, "es"):
		return []string{strings.TrimSuffix(r, "es"), strings.TrimSuffix(r, "s")}
	case strings.HasSuffix(r, "s"):
		return []string{strings.TrimSuffix(r, "s")}
	case strings.HasSuffix(r, "y"):
		return []string{strings.TrimSuffix(r, "y") + "ies"}
	default:
		return []string{r + "s", r + "es"}
	}
}

// verbMatchesAny is matchesAny for verbs, which compare case-insensitively.
func verbMatchesAny(verbs []string, verb string) bool {
	return len(verbs) == 0 || anyVerb(verbs, verb)
}

// allArgsPresent reports whether every pattern matches at least one arg.
func allArgsPresent(patterns, args []string) bool {
	for _, pattern := range patterns {
		if !anyArgMatches(pattern, args) {
			return false
		}
	}
	return true
}

// noArgPresent reports whether no pattern matches any arg.
func noArgPresent(patterns, args []string) bool {
	for _, pattern := range patterns {
		if anyArgMatches(pattern, args) {
			return false
		}
	}
	return true
}

// anyArgMatches reports whether pattern matches any single token. A flag
// written as "--namespace=kube-system" also matches the bare "--namespace".
func anyArgMatches(pattern string, args []string) bool {
	for _, arg := range args {
		if MatchPattern(pattern, arg) {
			return true
		}
		if name, _, ok := strings.Cut(arg, "="); ok && MatchPattern(pattern, name) {
			return true
		}
	}
	return false
}

// NormalizeReason trims a caller-supplied reason and caps its length so an
// oversized value cannot bloat the audit log.
func NormalizeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if len(reason) > MaxReasonLength {
		reason = reason[:MaxReasonLength]
	}
	return reason
}

// reasonSatisfied reports whether a reason is substantial enough to accept.
func reasonSatisfied(reason string) bool {
	return len(NormalizeReason(reason)) >= MinReasonLength
}

// validateGuardrails checks that names are present and unique and that every
// action is one this engine knows how to apply.
func (p *Policy) validateGuardrails() error {
	seen := make(map[string]bool, len(p.Guardrails))
	for _, g := range p.Guardrails {
		if g.Name == "" {
			return fmt.Errorf("guardrail missing name")
		}
		if seen[g.Name] {
			return fmt.Errorf("duplicate guardrail %q", g.Name)
		}
		seen[g.Name] = true
		if !g.Action.valid() {
			return fmt.Errorf("guardrail %q has unknown action %q (want one of %q, %q, %q)",
				g.Name, g.Action, ActionDeny, ActionRequireReason, ActionRequireApproval)
		}
	}
	return nil
}

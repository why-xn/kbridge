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
)

// Reason length bounds. A reason must be substantial enough to be useful in an
// audit trail ("INC-4521" is exactly the minimum) and short enough to store.
const (
	MinReasonLength = 8
	MaxReasonLength = 512
)

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
		matchesAny(m.Resources, req.Resource) &&
		verbMatchesAny(m.Verbs, req.Verb) &&
		allArgsPresent(m.Args, req.Args) &&
		noArgPresent(m.ArgsNot, req.Args)
}

// exempts reports whether subject is excused from this guardrail.
func (g Guardrail) exempts(subject string) bool {
	for _, pattern := range g.Exempt {
		if matchPattern(pattern, subject) {
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
	if g.Action == ActionRequireReason {
		return "this command requires a reason"
	}
	return "this command is blocked by policy"
}

// matchesAny reports whether value matches any pattern. An empty pattern list
// matches everything.
func matchesAny(patterns []string, value string) bool {
	return len(patterns) == 0 || anyMatch(patterns, value)
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
		if matchPattern(pattern, arg) {
			return true
		}
		if name, _, ok := strings.Cut(arg, "="); ok && matchPattern(pattern, name) {
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
		if g.Action != ActionDeny && g.Action != ActionRequireReason {
			return fmt.Errorf("guardrail %q has unknown action %q (want %q or %q)",
				g.Name, g.Action, ActionDeny, ActionRequireReason)
		}
	}
	return nil
}

package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestRolesFor(t *testing.T) {
	withDefault := engineFor(t, samplePolicy)
	noDefault := engineFor(t, strings.Replace(samplePolicy, "default: viewer", "", 1))
	tests := []struct {
		name      string
		e         *Engine
		subject   string
		want      []string
		isDefault bool
	}{
		{"bound", withDefault, "x@dev.corp.com", []string{"developer"}, false},
		{"unbound with default", withDefault, "stranger@x.com", []string{"viewer"}, true},
		{"unbound no default", noDefault, "stranger@x.com", nil, false},
		{"nil engine", nil, "a@x.com", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isDef := tt.e.RolesFor(tt.subject)
			if !reflect.DeepEqual(got, tt.want) || isDef != tt.isDefault {
				t.Errorf("RolesFor = %v, %v; want %v, %v", got, isDef, tt.want, tt.isDefault)
			}
		})
	}
}

func TestAccessFor(t *testing.T) {
	known := []string{"prod-us-east", "staging", "dev-1"}
	policy := func(rules string) string {
		return "roles:\n  - name: r\n    rules:\n" + rules + "bindings:\n  - subject: u@x.com\n    roles: [r]\n"
	}
	rule := func(c, n, v string) string {
		return "      - clusters: " + c + "\n        namespaces: " + n + "\n        resources: [\"*\"]\n        verbs: " + v + "\n"
	}
	twoRoles := `roles:
  - name: dev
    rules:
      - clusters: ["prod-*"]
        namespaces: [payments]
        resources: ["*"]
        verbs: [get, delete]
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: [get, list, logs]
bindings:
  - subject: u@x.com
    roles: [viewer, dev]
`
	tests := []struct {
		name string
		e    *Engine
		user string
		want []AccessSummary
	}{
		{"all", engineFor(t, policy(rule(`["*"]`, `["*"]`, `["*"]`))), "u@x.com",
			[]AccessSummary{{"*", []string{"*"}, "read-write"}}},
		{"globs", engineFor(t, policy(rule(`["dev-*","staging"]`, `[team-a]`, `[get, apply]`))), "u@x.com",
			[]AccessSummary{{"dev-1", []string{"team-a"}, "read-write"}, {"staging", []string{"team-a"}, "read-write"}}},
		{"two roles ordered", engineFor(t, twoRoles), "u@x.com",
			[]AccessSummary{{"prod-us-east", []string{"payments"}, "read-write"}, {"*", []string{"*"}, "read-only"}}},
		{"no match", engineFor(t, policy(rule(`["eu-*"]`, `["*"]`, `[get]`))), "u@x.com", []AccessSummary{}},
		{"unbound", engineFor(t, policy(rule(`["*"]`, `["*"]`, `[get]`))), "z@x.com", []AccessSummary{}},
		{"nil engine", nil, "u@x.com", nil},
		{"auth writes rbac", engineFor(t, policy(rule(`["*"]`, `["*"]`, `[get, auth]`))), "u@x.com",
			[]AccessSummary{{"*", []string{"*"}, "read-write"}}},
		{"read-only verbs", engineFor(t, policy(rule(`["*"]`, `["*"]`, `[get, list, logs, top]`))), "u@x.com",
			[]AccessSummary{{"*", []string{"*"}, "read-only"}}},
		{"empty namespaces allow nothing", engineFor(t, policy(rule(`["*"]`, `[]`, `[get]`))), "u@x.com", []AccessSummary{}},
		{"empty verbs allow nothing", engineFor(t, policy(rule(`["*"]`, `["*"]`, `[]`))), "u@x.com", []AccessSummary{}},
		{"star namespace collapses", engineFor(t, policy(rule(`[staging]`, `[team-a]`, `[apply]`)+rule(`[staging]`, `["*"]`, `[apply]`))), "u@x.com",
			[]AccessSummary{{"staging", []string{"*"}, "read-write"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.AccessFor(tt.user, known); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AccessFor = %+v, want %+v", got, tt.want)
			}
		})
	}
}

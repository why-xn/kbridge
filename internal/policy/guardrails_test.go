package policy

import "testing"

// guardedPolicy grants developers broad access, then carves dangerous commands
// back out with guardrails.
const guardedPolicy = `
default: developer
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
guardrails:
  - name: no-prod-namespace-delete
    match:
      clusters: ["prod-*"]
      resources: ["namespaces", "ns"]
      verbs: ["delete"]
    action: deny
    message: "deleting namespaces in production is not allowed"
    exempt: ["breakglass@corp.com"]

  - name: no-bulk-delete
    match:
      clusters: ["prod-*"]
      verbs: ["delete"]
      args: ["--all"]
    action: deny

  - name: prod-apply-needs-reason
    match:
      clusters: ["prod-*"]
      verbs: ["apply"]
      args_not: ["--dry-run*"]
    action: require-reason
    message: "applying to production requires a reason"
`

func TestPolicy_Evaluate(t *testing.T) {
	p := mustParse(t, guardedPolicy)

	tests := []struct {
		name      string
		subject   string
		cluster   string
		command   []string
		reason    string
		want      Outcome
		guardrail string
	}{
		{
			name: "unguarded command is allowed", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"get", "pods"}, want: OutcomeAllowed,
		},
		{
			name: "guardrail blocks prod namespace delete", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"delete", "ns", "payments"},
			want: OutcomeBlocked, guardrail: "no-prod-namespace-delete",
		},
		{
			name: "same command is fine outside prod", subject: "dev@corp.com",
			cluster: "dev-1", command: []string{"delete", "ns", "payments"}, want: OutcomeAllowed,
		},
		{
			name: "exempt subject bypasses the guardrail", subject: "breakglass@corp.com",
			cluster: "prod-eu", command: []string{"delete", "ns", "payments"}, want: OutcomeAllowed,
		},
		{
			name: "arg match catches bulk delete", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"delete", "pods", "--all"},
			want: OutcomeBlocked, guardrail: "no-bulk-delete",
		},
		{
			name: "bulk delete without the flag is allowed", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"delete", "pods", "api-0"}, want: OutcomeAllowed,
		},
		{
			name: "prod apply demands a reason", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"apply", "-f", "app.yaml"},
			want: OutcomeReasonRequired, guardrail: "prod-apply-needs-reason",
		},
		{
			name: "prod apply proceeds with a reason", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"apply", "-f", "app.yaml"},
			reason: "INC-4521 hotfix", want: OutcomeAllowed,
		},
		{
			name: "a too-short reason does not satisfy the guardrail", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"apply", "-f", "app.yaml"}, reason: "fix",
			want: OutcomeReasonRequired, guardrail: "prod-apply-needs-reason",
		},
		{
			name: "args_not exempts a dry run", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"apply", "-f", "app.yaml", "--dry-run=server"},
			want: OutcomeAllowed,
		},
		{
			name: "first matching guardrail wins", subject: "dev@corp.com",
			cluster: "prod-eu", command: []string{"delete", "ns", "--all"},
			want: OutcomeBlocked, guardrail: "no-prod-namespace-delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ParseAccessRequest(tt.cluster, tt.command, "")
			got := p.Evaluate(tt.subject, req, tt.reason)
			if got.Outcome != tt.want {
				t.Errorf("Outcome = %q, want %q (message %q)", got.Outcome, tt.want, got.Message)
			}
			if got.Guardrail != tt.guardrail {
				t.Errorf("Guardrail = %q, want %q", got.Guardrail, tt.guardrail)
			}
		})
	}
}

func TestPolicy_Evaluate_RBACDenialSkipsGuardrails(t *testing.T) {
	p := mustParse(t, `
default: viewer
roles:
  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get"]
guardrails:
  - name: never-reached
    match:
      verbs: ["delete"]
    action: require-reason
`)
	req := ParseAccessRequest("prod", []string{"delete", "pods"}, "")
	got := p.Evaluate("viewer@corp.com", req, "INC-4521 cleanup")
	if got.Outcome != OutcomeDenied {
		t.Fatalf("Outcome = %q, want %q", got.Outcome, OutcomeDenied)
	}
	if got.Guardrail != "" {
		t.Errorf("Guardrail = %q, want empty: role rules decide before guardrails", got.Guardrail)
	}
}

func TestPolicy_Evaluate_NormalizesAcceptedReason(t *testing.T) {
	p := mustParse(t, guardedPolicy)
	req := ParseAccessRequest("prod-eu", []string{"apply", "-f", "app.yaml"}, "")
	got := p.Evaluate("dev@corp.com", req, "   INC-4521 hotfix   ")
	if !got.Allowed() {
		t.Fatalf("Outcome = %q, want allowed", got.Outcome)
	}
	if got.Reason != "INC-4521 hotfix" {
		t.Errorf("Reason = %q, want the trimmed justification", got.Reason)
	}
}

func TestNormalizeReason_CapsLength(t *testing.T) {
	long := make([]byte, MaxReasonLength+50)
	for i := range long {
		long[i] = 'x'
	}
	if got := len(NormalizeReason(string(long))); got != MaxReasonLength {
		t.Errorf("length = %d, want %d", got, MaxReasonLength)
	}
}

func TestGuardrailMatch_EmptyListMatchesAnything(t *testing.T) {
	// A guardrail naming only a verb must apply across every cluster and
	// namespace, unlike a role rule where an empty list grants nothing.
	m := Match{Verbs: []string{"delete"}}
	tests := []struct {
		name string
		req  AccessRequest
		want bool
	}{
		{"matching verb anywhere", AccessRequest{Cluster: "any", Namespace: "any", Resource: "pods", Verb: "delete"}, true},
		{"other verb", AccessRequest{Cluster: "any", Namespace: "any", Resource: "pods", Verb: "get"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.matches(tt.req); got != tt.want {
				t.Errorf("matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnyArgMatches(t *testing.T) {
	args := []string{"apply", "-f", "app.yaml", "--dry-run=server", "--namespace=prod"}
	tests := []struct {
		pattern string
		want    bool
	}{
		{"--dry-run", true},        // flag=value matches the bare flag name
		{"--dry-run=server", true}, // and the whole token
		{"--dry-run*", true},       // and a wildcard over it
		{"--namespace", true},
		{"-f", true},
		{"--all", false},
		{"app.yaml", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			if got := anyArgMatches(tt.pattern, args); got != tt.want {
				t.Errorf("anyArgMatches(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestParse_RejectsMalformedGuardrails(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"missing name", `
guardrails:
  - action: deny
`},
		{"unknown action", `
guardrails:
  - name: g1
    action: warn
`},
		{"duplicate name", `
guardrails:
  - name: g1
    action: deny
  - name: g1
    action: deny
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.doc)); err == nil {
				t.Fatal("expected a validation error, got nil")
			}
		})
	}
}

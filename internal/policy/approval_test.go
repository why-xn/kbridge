package policy

import "testing"

const approvalPolicy = `
default: developer
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
guardrails:
  - name: prod-writes-need-approval
    match:
      clusters: ["prod-*"]
      verbs: ["delete", "apply"]
      args_not: ["--dry-run*"]
    action: require-approval
    message: "changing production requires an approved grant"

  - name: prod-ns-delete-denied
    match:
      clusters: ["prod-*"]
      resources: ["namespaces", "ns"]
      verbs: ["delete"]
    action: deny
`

func TestPolicy_Evaluate_RequireApproval(t *testing.T) {
	p := mustParse(t, approvalPolicy)

	tests := []struct {
		name      string
		cluster   string
		command   []string
		reason    string
		want      Outcome
		guardrail string
	}{
		{
			name: "prod write needs approval", cluster: "prod-eu",
			command: []string{"delete", "pod", "api-0"},
			want:    OutcomeApprovalRequired, guardrail: "prod-writes-need-approval",
		},
		{
			name: "a reason does not substitute for approval", cluster: "prod-eu",
			command: []string{"delete", "pod", "api-0"}, reason: "INC-4521 rollback",
			want: OutcomeApprovalRequired, guardrail: "prod-writes-need-approval",
		},
		{
			name: "dry run is exempt", cluster: "prod-eu",
			command: []string{"apply", "-f", "app.yaml", "--dry-run=server"},
			want:    OutcomeAllowed,
		},
		{
			name: "non-prod is unaffected", cluster: "dev-1",
			command: []string{"delete", "pod", "api-0"}, want: OutcomeAllowed,
		},
		{
			name: "reads are unaffected", cluster: "prod-eu",
			command: []string{"get", "pods"}, want: OutcomeAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ParseAccessRequest(tt.cluster, tt.command, "")
			got := p.Evaluate("dev@corp.com", req, tt.reason)
			if got.Outcome != tt.want {
				t.Errorf("Outcome = %q, want %q (message %q)", got.Outcome, tt.want, got.Message)
			}
			if got.Guardrail != tt.guardrail {
				t.Errorf("Guardrail = %q, want %q", got.Guardrail, tt.guardrail)
			}
		})
	}
}

// TestPolicy_Evaluate_ApprovalCarriesReason pins that a supplied reason survives
// onto an approval-required decision, so the control plane can audit what the
// caller said even though the reason alone did not admit the command.
func TestPolicy_Evaluate_ApprovalCarriesReason(t *testing.T) {
	p := mustParse(t, approvalPolicy)
	req := ParseAccessRequest("prod-eu", []string{"delete", "pod", "api-0"}, "")
	got := p.Evaluate("dev@corp.com", req, "  INC-4521 rollback  ")
	if got.Outcome != OutcomeApprovalRequired {
		t.Fatalf("Outcome = %q, want %q", got.Outcome, OutcomeApprovalRequired)
	}
	if got.Reason != "INC-4521 rollback" {
		t.Errorf("Reason = %q, want the trimmed justification", got.Reason)
	}
}

func TestPolicy_Evaluate_DenyStillWinsByOrder(t *testing.T) {
	// The approval guardrail is listed first, so it decides even for a command
	// the later deny rule would also match. First match wins, as documented.
	p := mustParse(t, approvalPolicy)
	req := ParseAccessRequest("prod-eu", []string{"delete", "ns", "payments"}, "")
	got := p.Evaluate("dev@corp.com", req, "")
	if got.Guardrail != "prod-writes-need-approval" {
		t.Errorf("Guardrail = %q, want the first matching guardrail", got.Guardrail)
	}
}

func TestAction_Valid(t *testing.T) {
	tests := []struct {
		action Action
		want   bool
	}{
		{ActionDeny, true},
		{ActionRequireReason, true},
		{ActionRequireApproval, true},
		{Action("warn"), false},
		{Action(""), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			if got := tt.action.valid(); got != tt.want {
				t.Errorf("valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParse_AcceptsRequireApproval(t *testing.T) {
	if _, err := Parse([]byte(approvalPolicy)); err != nil {
		t.Fatalf("require-approval should be a valid action: %v", err)
	}
}

func TestResourceAliases(t *testing.T) {
	tests := []struct {
		resource string
		want     []string
	}{
		{"deployments", []string{"deployment"}},
		{"deployment", []string{"deployments", "deploymentes"}},
		{"ingresses", []string{"ingress", "ingresse"}},
		{"policies", []string{"policy"}},
		{"policy", []string{"policies"}},
		{"pods", []string{"pod"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			got := resourceAliases(tt.resource)
			if len(got) != len(tt.want) {
				t.Fatalf("aliases = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("aliases = %v, want %v", got, tt.want)
					return
				}
			}
		})
	}
}

// TestGuardrailResourceMatchingIsPluralTolerant pins that a guardrail written
// for one spelling catches the others kubectl accepts, so an author does not
// have to enumerate every form to close a hole.
func TestGuardrailResourceMatchingIsPluralTolerant(t *testing.T) {
	p := mustParse(t, `
default: dev
roles:
  - name: dev
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
guardrails:
  - name: no-workload-delete
    match:
      resources: ["deployments"]
      verbs: ["delete"]
    action: deny
`)
	tests := []struct {
		name    string
		command []string
		blocked bool
	}{
		{"plural, as written", []string{"delete", "deployments", "api"}, true},
		{"singular, as a user would type", []string{"delete", "deployment", "api"}, true},
		{"slash form", []string{"delete", "deployment/api"}, true},
		{"an unrelated resource is untouched", []string{"delete", "pod", "api-0"}, false},
		{"a similar prefix is not a match", []string{"delete", "deploymentconfigs", "api"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ParseAccessRequest("prod", tt.command, "")
			got := p.Evaluate("dev@corp.com", req, "")
			blocked := got.Outcome == OutcomeBlocked
			if blocked != tt.blocked {
				t.Errorf("blocked = %v, want %v (outcome %q, resource %q)",
					blocked, tt.blocked, got.Outcome, req.Resource)
			}
		})
	}
}

// TestRoleRulesStayExact pins the other half of the trade-off: widening applies
// to guardrails only, so a role rule never grants more than it spells out.
func TestRoleRulesStayExact(t *testing.T) {
	p := mustParse(t, `
default: dev
roles:
  - name: dev
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["deployments"]
        verbs: ["delete"]
`)
	req := ParseAccessRequest("prod", []string{"delete", "deployment", "api"}, "")
	if p.Evaluate("dev@corp.com", req, "").Allowed() {
		t.Error("a role rule for \"deployments\" must not grant \"deployment\": widening only ever removes access")
	}
}

package cli

import (
	"strings"
	"testing"
)

func TestExtractReason(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantReason string
		wantRest   []string
	}{
		{
			name: "no reason flag", args: []string{"get", "pods"},
			wantReason: "", wantRest: []string{"get", "pods"},
		},
		{
			name: "separate value", args: []string{"delete", "pod", "x", "--reason", "INC-4521"},
			wantReason: "INC-4521", wantRest: []string{"delete", "pod", "x"},
		},
		{
			name: "inline value", args: []string{"delete", "pod", "x", "--reason=INC-4521"},
			wantReason: "INC-4521", wantRest: []string{"delete", "pod", "x"},
		},
		{
			name: "flag in the middle", args: []string{"delete", "--reason", "INC-4521", "pod", "x"},
			wantReason: "INC-4521", wantRest: []string{"delete", "pod", "x"},
		},
		{
			name: "multi-word value", args: []string{"apply", "-f", "a.yaml", "--reason", "rolling back the bad deploy"},
			wantReason: "rolling back the bad deploy", wantRest: []string{"apply", "-f", "a.yaml"},
		},
		{
			name: "inline empty value", args: []string{"get", "pods", "--reason="},
			wantReason: "", wantRest: []string{"get", "pods"},
		},
		{
			name: "trailing flag with no value", args: []string{"get", "pods", "--reason"},
			wantReason: "", wantRest: []string{"get", "pods"},
		},
		{
			name: "value is trimmed", args: []string{"get", "pods", "--reason", "  INC-1  "},
			wantReason: "INC-1", wantRest: []string{"get", "pods"},
		},
		{
			name: "last occurrence wins", args: []string{"get", "pods", "--reason", "first", "--reason", "second"},
			wantReason: "second", wantRest: []string{"get", "pods"},
		},
		{
			name:       "a kubectl arg that merely looks similar is left alone",
			args:       []string{"get", "pods", "--reasonable", "x"},
			wantReason: "", wantRest: []string{"get", "pods", "--reasonable", "x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, rest := extractReason(tt.args)
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
			if strings.Join(rest, " ") != strings.Join(tt.wantRest, " ") {
				t.Errorf("rest = %v, want %v", rest, tt.wantRest)
			}
		})
	}
}

func TestPolicyRejectionError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
		exact    string
	}{
		{
			name:  "plain rbac denial",
			body:  `{"error":"permission denied"}`,
			exact: "permission denied",
		},
		{
			name:     "guardrail deny names the guardrail",
			body:     `{"error":"deleting namespaces in production is not allowed","guardrail":"no-prod-ns-delete"}`,
			contains: []string{"deleting namespaces in production is not allowed", `guardrail "no-prod-ns-delete"`},
		},
		{
			name:     "reason required adds the hint",
			body:     `{"error":"applying to production requires a reason","guardrail":"prod-apply","reason_required":true}`,
			contains: []string{"applying to production requires a reason", "--reason"},
		},
		{
			name:  "unparseable body degrades gracefully",
			body:  `<html>gateway error</html>`,
			exact: "permission denied",
		},
		{
			name:  "empty body degrades gracefully",
			body:  ``,
			exact: "permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policyRejectionError([]byte(tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.exact != "" && err.Error() != tt.exact {
				t.Errorf("error = %q, want %q", err.Error(), tt.exact)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

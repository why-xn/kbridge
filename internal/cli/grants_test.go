package cli

import (
	"strings"
	"testing"
	"time"
)

func TestGrantRow(t *testing.T) {
	future := time.Now().Add(90 * time.Minute)
	past := time.Now().Add(-time.Hour)

	tests := []struct {
		name        string
		grant       GrantInfo
		withSubject bool
		wantFields  int
		wantScope   string
		wantExpiry  string
	}{
		{
			name: "pending grant, user view",
			grant: GrantInfo{ID: "g1", ClusterName: "prod-eu", DisplayStatus: "pending",
				Reason: "INC-4521"},
			wantFields: 5, wantScope: "prod-eu", wantExpiry: "-",
		},
		{
			name: "namespace-scoped grant shows the scope",
			grant: GrantInfo{ID: "g2", ClusterName: "prod-eu", Namespace: "payments",
				DisplayStatus: "approved", ExpiresAt: &future, Reason: "INC-4521"},
			wantFields: 5, wantScope: "prod-eu/payments", wantExpiry: "1h30m0s left",
		},
		{
			name: "expired grant reads as expired",
			grant: GrantInfo{ID: "g3", ClusterName: "prod-eu", DisplayStatus: "expired",
				ExpiresAt: &past, Reason: "INC-4521"},
			wantFields: 5, wantScope: "prod-eu", wantExpiry: "expired",
		},
		{
			name: "admin view adds subject and approver",
			grant: GrantInfo{ID: "g4", Subject: "dev@corp.com", ClusterName: "prod-eu",
				DisplayStatus: "approved", ExpiresAt: &future, DecidedBy: "boss@corp.com",
				Reason: "INC-4521"},
			withSubject: true, wantFields: 7, wantScope: "prod-eu", wantExpiry: "1h30m0s left",
		},
		{
			name: "admin view dashes an undecided grant",
			grant: GrantInfo{ID: "g5", Subject: "dev@corp.com", ClusterName: "prod-eu",
				DisplayStatus: "pending", Reason: "INC-4521"},
			withSubject: true, wantFields: 7, wantScope: "prod-eu", wantExpiry: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := strings.Split(grantRow(tt.grant, tt.withSubject), "\t")
			if len(fields) != tt.wantFields {
				t.Fatalf("fields = %d, want %d (%q)", len(fields), tt.wantFields, fields)
			}
			scopeIdx := 1
			expiryIdx := 3
			if tt.withSubject {
				scopeIdx, expiryIdx = 2, 4
			}
			if fields[scopeIdx] != tt.wantScope {
				t.Errorf("scope = %q, want %q", fields[scopeIdx], tt.wantScope)
			}
			if fields[expiryIdx] != tt.wantExpiry {
				t.Errorf("expiry = %q, want %q", fields[expiryIdx], tt.wantExpiry)
			}
			if tt.withSubject && tt.grant.DecidedBy == "" && fields[5] != "-" {
				t.Errorf("approver = %q, want %q for an undecided grant", fields[5], "-")
			}
		})
	}
}

func TestGrantHeader(t *testing.T) {
	if h := grantHeader(false); strings.Contains(h, "SUBJECT") {
		t.Errorf("user view should not show SUBJECT: %q", h)
	}
	if h := grantHeader(true); !strings.Contains(h, "SUBJECT") || !strings.Contains(h, "DECIDED BY") {
		t.Errorf("admin view should show who asked and who decided: %q", h)
	}
	if got, want := len(strings.Split(grantHeader(false), "\t")), 5; got != want {
		t.Errorf("user header columns = %d, want %d", got, want)
	}
	if got, want := len(strings.Split(grantHeader(true), "\t")), 7; got != want {
		t.Errorf("admin header columns = %d, want %d", got, want)
	}
}

func TestExpiryText(t *testing.T) {
	tests := []struct {
		name  string
		grant GrantInfo
		want  string
	}{
		{"no expiry", GrantInfo{}, "-"},
		{"in the future", GrantInfo{ExpiresAt: ptrTime(time.Now().Add(2 * time.Hour))}, "2h0m0s left"},
		{"already past", GrantInfo{ExpiresAt: ptrTime(time.Now().Add(-time.Minute))}, "expired"},
		{"rounds to the minute", GrantInfo{ExpiresAt: ptrTime(time.Now().Add(30*time.Minute + 20*time.Second))}, "30m0s left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expiryText(tt.grant); got != tt.want {
				t.Errorf("expiryText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestPolicyRejectionError_ApprovalRequired(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name:     "approval required points at kb request",
			body:     `{"error":"changing production requires an approved grant","guardrail":"prod-writes","approval_required":true}`,
			contains: []string{"changing production requires an approved grant", "kb request", "kb grants", "approve"},
			// The retry-with-a-reason hint is wrong here: a reason cannot
			// satisfy an approval. (kb request's own --reason flag is fine.)
			absent: []string{"retry with --reason"},
		},
		{
			name:     "reason required still points at --reason",
			body:     `{"error":"needs a reason","guardrail":"g","reason_required":true}`,
			contains: []string{"retry with --reason"},
			absent:   []string{"kb request"},
		},
		{
			name:     "a plain deny gets neither hint",
			body:     `{"error":"blocked","guardrail":"g"}`,
			contains: []string{"blocked"},
			absent:   []string{"kb request", "retry with --reason"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := policyRejectionError([]byte(tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("error %q should not contain %q", err, bad)
				}
			}
		})
	}
}

func TestGrantCommandsAreManagementCommands(t *testing.T) {
	// kubectl-by-default must not swallow the new commands and send them to a
	// cluster as kubectl verbs.
	for _, word := range []string{"request", "grants", "policy"} {
		t.Run(word, func(t *testing.T) {
			got := rewriteArgs([]string{word, "prod-eu"})
			if len(got) > 0 && got[0] == "kubectl" {
				t.Errorf("%q was dispatched to kubectl, want the management command", word)
			}
		})
	}
}

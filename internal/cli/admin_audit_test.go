package cli

import (
	"strings"
	"testing"
)

func TestAuditTable_ReasonColumn(t *testing.T) {
	code := int32(0)
	tests := []struct {
		name       string
		logs       []AuditLogInfo
		wantColumn bool
	}{
		{
			name: "no reasons keeps the original columns",
			logs: []AuditLogInfo{
				{UserEmail: "a@x.com", ClusterName: "prod", Status: "success", ExitCode: &code, Command: "get pods"},
			},
			wantColumn: false,
		},
		{
			name: "one reason adds the column for every row",
			logs: []AuditLogInfo{
				{UserEmail: "a@x.com", ClusterName: "prod", Status: "success", Command: "get pods"},
				{UserEmail: "b@x.com", ClusterName: "prod", Status: "success", Command: "delete pod p", Reason: "INC-4521"},
			},
			wantColumn: true,
		},
		{
			name:       "empty listing",
			logs:       nil,
			wantColumn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyHasReason(tt.logs)
			if got != tt.wantColumn {
				t.Fatalf("anyHasReason = %v, want %v", got, tt.wantColumn)
			}
			header := auditHeader(got)
			if strings.Contains(header, "REASON") != tt.wantColumn {
				t.Errorf("header %q does not match wantColumn=%v", header, tt.wantColumn)
			}
		})
	}
}

func TestAuditRow(t *testing.T) {
	code := int32(1)
	tests := []struct {
		name       string
		log        AuditLogInfo
		withReason bool
		wantFields int
		wantLast   string
	}{
		{
			name:       "without the reason column",
			log:        AuditLogInfo{CreatedAt: "t", UserEmail: "a@x.com", ClusterName: "prod", Status: "success", ExitCode: &code, Command: "get pods"},
			withReason: false, wantFields: 6, wantLast: "get pods",
		},
		{
			name:       "with a reason",
			log:        AuditLogInfo{CreatedAt: "t", UserEmail: "a@x.com", ClusterName: "prod", Status: "success", Command: "delete pod p", Reason: "INC-4521"},
			withReason: true, wantFields: 7, wantLast: "INC-4521",
		},
		{
			name:       "column present but this row has none",
			log:        AuditLogInfo{CreatedAt: "t", UserEmail: "a@x.com", ClusterName: "prod", Status: "success", Command: "get pods"},
			withReason: true, wantFields: 7, wantLast: "-",
		},
		{
			name:       "missing exit code renders as a dash",
			log:        AuditLogInfo{CreatedAt: "t", UserEmail: "a@x.com", ClusterName: "prod", Status: "blocked", Command: "delete ns x"},
			withReason: false, wantFields: 6, wantLast: "delete ns x",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := strings.Split(auditRow(tt.log, tt.withReason), "\t")
			if len(fields) != tt.wantFields {
				t.Fatalf("fields = %d, want %d (%q)", len(fields), tt.wantFields, fields)
			}
			if fields[len(fields)-1] != tt.wantLast {
				t.Errorf("last field = %q, want %q", fields[len(fields)-1], tt.wantLast)
			}
			if tt.log.ExitCode == nil && fields[4] != "-" {
				t.Errorf("exit field = %q, want %q for a missing exit code", fields[4], "-")
			}
		})
	}
}

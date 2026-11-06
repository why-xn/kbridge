package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderRBACStatus(t *testing.T) {
	at := "2026-11-05T10:00:00Z"
	ns := "2026-11-05T10:00:00.123456789Z"
	tests := []struct {
		name    string
		rows    []RBACClusterStatus
		want    []string
		notWant []string
	}{
		{"nanoseconds trimmed", []RBACClusterStatus{{Cluster: "dev-1", State: "in sync", ReportedAt: &ns}},
			[]string{"2026-11-05T10:00:00Z"}, []string{".123456789"}},
		{"empty", nil, []string{"No clusters connected."}, []string{"CLUSTER"}},
		{"in sync", []RBACClusterStatus{{Cluster: "dev-1", Version: "0123456789abcdef", State: "in sync", ReportedAt: &at}},
			[]string{"CLUSTER", "dev-1", "01234567 ", "in sync", at}, []string{"89abcdef", "drift:"}},
		{"drift with errors", []RBACClusterStatus{
			{Cluster: "dev-1", Version: "aaaaaaaabbbb", State: "drift", Errors: []string{"forbidden: ns dev", "timeout"}, ReportedAt: &at},
			{Cluster: "prod-1", State: "unreported"}},
			[]string{"aaaaaaaa", "drift", "\n    forbidden: ns dev\n    timeout\nprod-1", "unreported", "drift: "}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			renderRBACStatus(&buf, tt.rows)
			out := buf.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output missing %q:\n%s", w, out)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(out, w) {
					t.Errorf("output should not contain %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestControlPlaneClient_GetRBACStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/rbac/status" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"clusters": []map[string]any{{"cluster": "dev-1", "state": "drift", "errors": []string{"x"}}},
		})
	}))
	defer server.Close()

	rows, err := NewControlPlaneClient(server.URL).GetRBACStatus()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 || rows[0].Cluster != "dev-1" || rows[0].State != "drift" || len(rows[0].Errors) != 1 {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

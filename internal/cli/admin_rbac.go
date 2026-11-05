package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// RBACClusterStatus is one cluster's binding sync state as the control plane reports it.
type RBACClusterStatus struct {
	Cluster        string   `json:"cluster"`
	Version        string   `json:"version,omitempty"`
	DesiredVersion string   `json:"desired_version"`
	State          string   `json:"state"`
	Errors         []string `json:"errors,omitempty"`
	ReportedAt     *string  `json:"reported_at,omitempty"`
}

var adminRBACCmd = &cobra.Command{
	Use:   "rbac",
	Short: "Inspect cluster RBAC sync",
}

var adminRBACStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether each cluster's bindings match kbridge policy",
	RunE:  runAdminRBACStatus,
}

func init() {
	adminCmd.AddCommand(adminRBACCmd)
	adminRBACCmd.AddCommand(adminRBACStatusCmd)
}

func runAdminRBACStatus(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	rows, err := client.GetRBACStatus()
	if err != nil {
		return fmt.Errorf("failed to fetch rbac status: %w", err)
	}
	renderRBACStatus(os.Stdout, rows)
	return nil
}

// renderRBACStatus prints one row per cluster with its errors indented below it.
func renderRBACStatus(out io.Writer, rows []RBACClusterStatus) {
	if len(rows) == 0 {
		fmt.Fprintln(out, "No clusters connected.")
		return
	}
	lines := rbacTableLines(rows)
	fmt.Fprintln(out, lines[0])
	drift := false
	for i, r := range rows {
		fmt.Fprintln(out, lines[i+1])
		for _, e := range r.Errors {
			fmt.Fprintf(out, "    %s\n", e)
		}
		drift = drift || r.State == "drift"
	}
	if drift {
		fmt.Fprintln(out, "\ndrift: the cluster last reported an older policy version, or could not apply every binding.")
	}
}

// rbacTableLines aligns the header and rows; error lines stay outside the
// table so long messages do not widen its columns.
func rbacTableLines(rows []RBACClusterStatus) []string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CLUSTER\tVERSION\tSTATE\tERRORS\tREPORTED")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", r.Cluster, shortVersion(r.Version), r.State, len(r.Errors), orDash(r.ReportedAt))
	}
	w.Flush()
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

func shortVersion(v string) string {
	if v == "" {
		return "-"
	}
	return v[:min(8, len(v))]
}

func orDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// GetRBACStatus fetches each connected cluster's binding sync state.
func (c *ControlPlaneClient) GetRBACStatus() ([]RBACClusterStatus, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v1/admin/rbac/status", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := adminStatusError(resp); err != nil {
		return nil, err
	}
	var out struct {
		Clusters []RBACClusterStatus `json:"clusters"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return out.Clusters, nil
}

func adminStatusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("admin role required")
	default:
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
}

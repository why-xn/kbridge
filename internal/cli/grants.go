package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	requestDuration  string
	requestReason    string
	requestNamespace string
	grantsStatus     string
	grantsLimit      int
)

var requestCmd = &cobra.Command{
	Use:   "request [cluster]",
	Short: "Request time-boxed access to a cluster",
	Long: `Ask for temporary access to a cluster. The request is pending until an
administrator approves it, and the access expires on its own afterwards.

With no cluster argument the currently selected cluster is used.

Examples:
  kb request prod-eu --duration 2h --reason "INC-4521 rolling back bad deploy"
  kb request --reason "INC-4521 investigating" --namespace payments`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE:         runRequest,
}

var grantsCmd = &cobra.Command{
	Use:   "grants",
	Short: "Show your access grants",
	Long: `List the access grants you have requested, with their status and expiry.

Examples:
  kb grants
  kb grants --status pending`,
	SilenceUsage: true,
	RunE:         runGrants,
}

func init() {
	requestCmd.Flags().StringVar(&requestDuration, "duration", "", "how long the access should last once approved (e.g. 2h)")
	requestCmd.Flags().StringVar(&requestReason, "reason", "", "why the access is needed (required)")
	requestCmd.Flags().StringVarP(&requestNamespace, "namespace", "n", "", "limit the grant to one namespace")
	_ = requestCmd.MarkFlagRequired("reason")

	grantsCmd.Flags().StringVar(&grantsStatus, "status", "", "filter by status (pending, approved, denied, revoked)")
	grantsCmd.Flags().IntVar(&grantsLimit, "limit", 50, "maximum grants to show")

	rootCmd.AddCommand(requestCmd)
	rootCmd.AddCommand(grantsCmd)
}

func runRequest(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	cluster := ""
	if len(args) == 1 {
		cluster = args[0]
	} else {
		cluster = viper.GetString(ConfigKeyCurrentCluster)
	}
	if cluster == "" {
		return fmt.Errorf("no cluster given and none selected. Pass one, or run 'kb clusters use <name>' first")
	}

	g, err := client.RequestGrant(cluster, requestNamespace, requestDuration, requestReason)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Requested %s access to %s.\n", g.Duration, g.ClusterName)
	fmt.Fprintf(out, "Grant ID: %s\nStatus:   %s\n\n", g.ID, g.DisplayStatus)
	fmt.Fprintln(out, "An administrator must approve it before the access is live.")
	fmt.Fprintln(out, "Track it with 'kb grants'.")
	return nil
}

func runGrants(cmd *cobra.Command, _ []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	grants, err := client.ListMyGrants(grantsStatus, grantsLimit)
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No grants found.")
		return nil
	}
	printGrantTable(cmd.OutOrStdout(), grants, false)
	return nil
}

// printGrantTable renders grants for a terminal. The approver column only earns
// its space in the admin view, where several people may be deciding.
func printGrantTable(out io.Writer, grants []GrantInfo, withSubject bool) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, grantHeader(withSubject))
	for _, g := range grants {
		fmt.Fprintln(w, grantRow(g, withSubject))
	}
	w.Flush()
}

// grantHeader returns the tab-separated column titles.
func grantHeader(withSubject bool) string {
	if withSubject {
		return "ID\tSUBJECT\tCLUSTER\tSTATUS\tEXPIRES\tDECIDED BY\tREASON"
	}
	return "ID\tCLUSTER\tSTATUS\tEXPIRES\tREASON"
}

// grantRow renders one grant as a tab-separated row.
func grantRow(g GrantInfo, withSubject bool) string {
	scope := g.ClusterName
	if g.Namespace != "" {
		scope += "/" + g.Namespace
	}
	if withSubject {
		return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s",
			g.ID, g.Subject, scope, g.DisplayStatus, expiryText(g), dashIfEmpty(g.DecidedBy), g.Reason)
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
		g.ID, scope, g.DisplayStatus, expiryText(g), g.Reason)
}

// expiryText renders how much of a live grant is left, which is what someone
// checking their own access actually wants to know.
func expiryText(g GrantInfo) string {
	if g.ExpiresAt == nil {
		return "-"
	}
	remaining := time.Until(*g.ExpiresAt)
	if remaining <= 0 {
		return "expired"
	}
	return remaining.Round(time.Minute).String() + " left"
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	adminGrantsSubject string
	adminGrantsStatus  string
	adminGrantsLimit   int
	adminGrantNote     string
	adminGrantDuration string
)

var adminGrantsCmd = &cobra.Command{
	Use:     "grants",
	Aliases: []string{"grant"},
	Short:   "Review and decide access requests",
	Long: `Triage just-in-time access requests.

A pending request grants nothing until it is approved, and an approved grant
expires on its own. Revoke ends one early.

Examples:
  kb admin grants list --status pending
  kb admin grants approve <id> --note "paged, go ahead"
  kb admin grants approve <id> --duration 30m
  kb admin grants deny <id> --note "use the runbook instead"
  kb admin grants revoke <id>`,
}

var adminGrantsListCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List access requests",
	SilenceUsage: true,
	RunE:         runAdminGrantsList,
}

var adminGrantsApproveCmd = &cobra.Command{
	Use:          "approve <id>",
	Short:        "Approve a pending request, starting its window now",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runAdminGrantsApprove,
}

var adminGrantsDenyCmd = &cobra.Command{
	Use:          "deny <id>",
	Short:        "Reject a pending request",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runAdminGrantsDeny,
}

var adminGrantsRevokeCmd = &cobra.Command{
	Use:          "revoke <id>",
	Short:        "End an approved grant early",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE:         runAdminGrantsRevoke,
}

func init() {
	adminGrantsListCmd.Flags().StringVar(&adminGrantsSubject, "subject", "", "filter by requesting user")
	adminGrantsListCmd.Flags().StringVar(&adminGrantsStatus, "status", "", "filter by status (pending, approved, denied, revoked)")
	adminGrantsListCmd.Flags().IntVar(&adminGrantsLimit, "limit", 50, "maximum grants to show")

	adminGrantsApproveCmd.Flags().StringVar(&adminGrantNote, "note", "", "note recorded with the decision")
	adminGrantsApproveCmd.Flags().StringVar(&adminGrantDuration, "duration", "", "shorten the granted window (e.g. 30m)")
	adminGrantsDenyCmd.Flags().StringVar(&adminGrantNote, "note", "", "note recorded with the decision")
	adminGrantsRevokeCmd.Flags().StringVar(&adminGrantNote, "note", "", "note recorded with the decision")

	adminGrantsCmd.AddCommand(adminGrantsListCmd, adminGrantsApproveCmd, adminGrantsDenyCmd, adminGrantsRevokeCmd)
	adminCmd.AddCommand(adminGrantsCmd)
}

func runAdminGrantsList(cmd *cobra.Command, _ []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	grants, err := client.ListAllGrants(adminGrantsSubject, adminGrantsStatus, adminGrantsLimit)
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No grants found.")
		return nil
	}
	printGrantTable(os.Stdout, grants, true)
	return nil
}

func runAdminGrantsApprove(cmd *cobra.Command, args []string) error {
	return decideGrant(cmd, func(c *ControlPlaneClient) (*GrantInfo, error) {
		return c.ApproveGrant(args[0], adminGrantNote, adminGrantDuration)
	})
}

func runAdminGrantsDeny(cmd *cobra.Command, args []string) error {
	return decideGrant(cmd, func(c *ControlPlaneClient) (*GrantInfo, error) {
		return c.DenyGrant(args[0], adminGrantNote)
	})
}

func runAdminGrantsRevoke(cmd *cobra.Command, args []string) error {
	return decideGrant(cmd, func(c *ControlPlaneClient) (*GrantInfo, error) {
		return c.RevokeGrant(args[0], adminGrantNote)
	})
}

// decideGrant runs a decision and reports the outcome, including when the
// access ends so the approver can see what they just handed out.
func decideGrant(cmd *cobra.Command, action func(*ControlPlaneClient) (*GrantInfo, error)) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	g, err := action(client)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Grant %s is now %s.\n", g.ID, g.DisplayStatus)
	fmt.Fprintf(out, "Subject: %s\nCluster: %s\n", g.Subject, g.ClusterName)
	if g.ExpiresAt != nil {
		fmt.Fprintf(out, "Expires: %s (%s)\n", g.ExpiresAt.Format("2006-01-02 15:04:05 MST"), expiryText(*g))
	}
	return nil
}

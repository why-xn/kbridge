package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/why-xn/kbridge/internal/policy"
)

// exitPolicyRefused is the exit code for a command the policy does not allow,
// so `kb policy test` can gate a CI job.
const exitPolicyRefused = 1

var (
	policyFile      string
	policyUser      string
	policyCluster   string
	policyNamespace string
	policyReason    string
)

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Inspect and test the authorization policy",
	Long: `Work with a kbridge policy file without contacting the control plane.

Both subcommands read the file directly, so they can run in CI on a proposed
policy before it is deployed.`,
}

var policyValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check that a policy file parses and is internally consistent",
	Long: `Parse a policy file and report any structural problems: unknown roles in
bindings, duplicate role or guardrail names, and unknown guardrail actions.

Examples:
  kb policy validate -f configs/rbac.yaml`,
	SilenceUsage: true,
	RunE:         runPolicyValidate,
}

var policyTestCmd = &cobra.Command{
	Use:   "test -- <kubectl args...>",
	Short: "Show how a policy would rule on a command",
	Long: `Evaluate one command against a policy file and print the verdict, without
running anything against a cluster.

Examples:
  kb policy test -f configs/rbac.yaml -u alice@corp.com -c prod-eu -- delete ns payments
  kb policy test -f configs/rbac.yaml -u bob@corp.com -c prod-eu --reason "INC-4521" -- delete pod api-0`,
	SilenceUsage: true,
	RunE:         runPolicyTest,
}

func init() {
	policyCmd.PersistentFlags().StringVarP(&policyFile, "file", "f", "", "path to the policy file (required)")
	_ = policyCmd.MarkPersistentFlagRequired("file")

	policyTestCmd.Flags().StringVarP(&policyUser, "user", "u", "", "subject to evaluate as, matched against policy bindings (required)")
	policyTestCmd.Flags().StringVarP(&policyCluster, "cluster", "c", "", "cluster the command targets (required)")
	policyTestCmd.Flags().StringVarP(&policyNamespace, "namespace", "n", "", "namespace to assume when the command does not name one")
	policyTestCmd.Flags().StringVar(&policyReason, "reason", "", "justification to supply, for guardrails that require one")
	_ = policyTestCmd.MarkFlagRequired("user")
	_ = policyTestCmd.MarkFlagRequired("cluster")

	policyCmd.AddCommand(policyValidateCmd)
	policyCmd.AddCommand(policyTestCmd)
	rootCmd.AddCommand(policyCmd)
}

// loadPolicy reads and parses the policy file named by the --file flag.
func loadPolicy() (*policy.Policy, error) {
	data, err := os.ReadFile(policyFile)
	if err != nil {
		return nil, fmt.Errorf("reading policy file: %w", err)
	}
	return policy.Parse(data)
}

func runPolicyValidate(cmd *cobra.Command, _ []string) error {
	p, err := loadPolicy()
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s is valid: %d role(s), %d binding(s), %d guardrail(s)\n",
		policyFile, len(p.Roles), len(p.Bindings), len(p.Guardrails))
	return nil
}

func runPolicyTest(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("a kubectl command is required, for example: -- delete ns payments")
	}
	p, err := loadPolicy()
	if err != nil {
		return err
	}

	req := policy.ParseAccessRequest(policyCluster, args, policyNamespace)
	decision := p.Evaluate(policyUser, req, policyReason)
	fmt.Fprint(cmd.OutOrStdout(), formatDecision(policyUser, req, decision))
	if !decision.Allowed() {
		os.Exit(exitPolicyRefused)
	}
	return nil
}

// formatDecision renders a verdict for a human reading a terminal, echoing the
// parsed request so a surprising outcome points at how the command was read.
func formatDecision(subject string, req policy.AccessRequest, d policy.Decision) string {
	out := fmt.Sprintf("subject:   %s\ncluster:   %s\nnamespace: %s\nresource:  %s\nverb:      %s\n\n",
		subject, req.Cluster, req.Namespace, req.Resource, req.Verb)
	out += "verdict:   " + string(d.Outcome)
	if d.Guardrail != "" {
		out += fmt.Sprintf(" (guardrail %q)", d.Guardrail)
	}
	out += "\n"
	if d.Message != "" {
		out += "reason:    " + d.Message + "\n"
	}
	if d.Outcome == policy.OutcomeReasonRequired {
		out += "hint:      " + reasonHint + "\n"
	}
	if d.Outcome == policy.OutcomeApprovalRequired {
		out += "hint:      " + approvalHint + "\n"
	}
	return out
}

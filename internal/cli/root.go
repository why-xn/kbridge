package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/why-xn/kbridge/internal/version"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "kb",
	Short: "Run kubectl on remote clusters through the kbridge central service",
	Long: `kb is the kbridge command-line interface for managing and accessing
multiple Kubernetes clusters through a central service.

kubectl by default: any command that is not a kbridge management command is run
as kubectl on the selected cluster. Management commands are login, logout,
status, clusters, and admin.

  kb get pods -A            # runs kubectl on the active cluster
  kb logs -f deploy/api     # streaming works too
  kb clusters use prod      # management command
  kb admin users list       # management command

Use 'kb kubectl ...' (or 'kb k ...') to force kubectl explicitly.`,
	Version: version.String(),
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// kubectl-by-default: rewrite the arguments so non-management commands are
	// dispatched to kubectl. See rewriteArgs.
	rootCmd.SetArgs(rewriteArgs(os.Args[1:]))
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Add version flag template
	rootCmd.SetVersionTemplate("kb version {{.Version}}\n")
}

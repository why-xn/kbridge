package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/why-xn/kbridge/internal/version"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "kb",
	Short: "Run kubectl on remote clusters through the kbridge control plane",
	Long: `kb is the kbridge command-line interface for managing and accessing
multiple Kubernetes clusters through a control plane.

kubectl by default: any command that is not a kbridge management command is run
as kubectl on the selected cluster. Management commands are login, logout,
status, clusters, and admin.

  kb get pods -A            # runs kubectl on the active cluster
  kb logs -f deploy/api     # streaming works too
  kb clusters use prod      # management command
  kb admin users list       # management command

Use 'kb kubectl ...' (or 'kb k ...') to force kubectl explicitly.`,
	Version: version.String(),
	// Execute prints the error once; cobra would print it again with usage.
	SilenceErrors: true,
	SilenceUsage:  true,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// kubectl-by-default: rewrite the arguments so non-management commands are
	// dispatched to kubectl. See rewriteArgs.
	os.Exit(execute(rewriteArgs(os.Args[1:]), os.Stderr))
}

// execute runs the root command with args and returns the process exit code.
func execute(args []string, stderr io.Writer) int {
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func init() {
	cobra.OnInitialize(initConfig)

	// Declared so cobra adds no -v shorthand: kubectl owns -v for verbosity.
	rootCmd.Flags().Bool("version", false, "version for kb")

	// Add version flag template
	rootCmd.SetVersionTemplate("kb version {{.Version}}\n")
}

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information
var (
	version = "0.1.0"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "mk8s",
	Short:   "A CLI tool for managing multiple Kubernetes clusters",
	Long: `mk8s is a command-line interface for managing and accessing
multiple Kubernetes clusters through a central service.

It provides a unified way to:
  - Authenticate with the central service
  - List and select from available Kubernetes clusters
  - Execute kubectl commands on remote clusters seamlessly`,
	Version: version,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Add version flag template
	rootCmd.SetVersionTemplate("mk8s version {{.Version}}\n")
}

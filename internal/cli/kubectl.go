package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// kubectlCmd represents the kubectl command
var kubectlCmd = &cobra.Command{
	Use:   "kubectl [args...]",
	Short: "Execute kubectl commands on the selected cluster",
	Long: `Execute kubectl commands on the currently selected Kubernetes cluster.

All arguments are passed through to kubectl on the remote cluster.`,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Println("Would execute: kubectl")
			return
		}
		fmt.Printf("Would execute: kubectl %s\n", strings.Join(args, " "))
	},
}

// kCmd is an alias for kubectl
var kCmd = &cobra.Command{
	Use:   "k [args...]",
	Short: "Alias for kubectl",
	Long: `Alias for the kubectl command.

All arguments are passed through to kubectl on the remote cluster.`,
	DisableFlagParsing: true,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			fmt.Println("Would execute: kubectl")
			return
		}
		fmt.Printf("Would execute: kubectl %s\n", strings.Join(args, " "))
	},
}

func init() {
	rootCmd.AddCommand(kubectlCmd)
	rootCmd.AddCommand(kCmd)
}

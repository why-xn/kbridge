package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Default timeout for kubectl commands (5 minutes)
const defaultKubectlTimeout = 5 * time.Minute

// kubectlCmd represents the kubectl command
var kubectlCmd = &cobra.Command{
	Use:   "kubectl [args...]",
	Short: "Execute kubectl commands on the selected cluster",
	Long: `Execute kubectl commands on the currently selected Kubernetes cluster.

All arguments are passed through to kubectl on the remote cluster.

Examples:
  mk8s kubectl get pods
  mk8s kubectl get pods -n kube-system
  mk8s kubectl describe node my-node
  mk8s kubectl logs my-pod -f`,
	DisableFlagParsing: true,
	RunE:               runKubectl,
}

// kCmd is an alias for kubectl
var kCmd = &cobra.Command{
	Use:   "k [args...]",
	Short: "Alias for kubectl",
	Long: `Alias for the kubectl command.

All arguments are passed through to kubectl on the remote cluster.

Examples:
  mk8s k get pods
  mk8s k get svc -A`,
	DisableFlagParsing: true,
	RunE:               runKubectl,
}

func init() {
	rootCmd.AddCommand(kubectlCmd)
	rootCmd.AddCommand(kCmd)
}

func runKubectl(cmd *cobra.Command, args []string) error {
	// Handle help flag manually since flag parsing is disabled
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return cmd.Help()
		}
	}

	// Check if this is an edit command - handle specially
	if isEditCommand(args) {
		return runKubectlEdit(args)
	}

	// Check central URL
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central URL not configured. Run 'mk8s login' first")
	}

	// Check current cluster
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)
	if currentCluster == "" {
		return fmt.Errorf("no cluster selected. Run 'mk8s clusters use <name>' first")
	}

	// Extract namespace from args if -n or --namespace is provided
	namespace := ""
	for i, arg := range args {
		if (arg == "-n" || arg == "--namespace") && i+1 < len(args) {
			namespace = args[i+1]
			break
		}
	}

	// Create client with longer timeout for command execution
	client := NewCentralClientWithTimeout(centralURL, defaultKubectlTimeout+10*time.Second)

	// Execute the command
	resp, err := client.ExecCommand(currentCluster, args, namespace, int(defaultKubectlTimeout.Seconds()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	// Print output
	if resp.Output != "" {
		fmt.Print(resp.Output)
	}

	// Print error message if present
	if resp.Error != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n", resp.Error)
	}

	// Exit with the same code as the remote kubectl
	if resp.ExitCode != 0 {
		os.Exit(int(resp.ExitCode))
	}

	return nil
}

// runKubectlEdit handles the kubectl edit command using a local editor workflow.
func runKubectlEdit(args []string) error {
	handler, err := NewEditHandler(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	if err := handler.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	return nil
}

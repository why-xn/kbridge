package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
  kbridge kubectl get pods
  kbridge kubectl get pods -n kube-system
  kbridge kubectl describe node my-node
  kbridge kubectl logs my-pod -f`,
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
  kbridge k get pods
  kbridge k get svc -A`,
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
		return fmt.Errorf("central URL not configured. Run 'kbridge login' first")
	}

	// Check current cluster
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)
	if currentCluster == "" {
		return fmt.Errorf("no cluster selected. Run 'kbridge clusters use <name>' first")
	}

	// Extract namespace from args if -n or --namespace is provided
	namespace := ""
	for i, arg := range args {
		if (arg == "-n" || arg == "--namespace") && i+1 < len(args) {
			namespace = args[i+1]
			break
		}
	}

	// Stream long-lived follow/watch commands via chunked HTTP.
	if isStreamingCommand(args) {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		streamClient := newAuthenticatedClient(centralURL)
		if err := streamClient.StreamCommand(ctx, currentCluster, args, namespace, os.Stdout); err != nil {
			return err
		}
		return nil
	}

	// Create client with longer timeout for command execution
	client := newAuthenticatedClientWithTimeout(centralURL, defaultKubectlTimeout+10*time.Second)

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

// isStreamingCommand reports whether args use a follow/watch flag and should be
// streamed rather than run one-shot.
func isStreamingCommand(args []string) bool {
	for _, a := range args {
		switch a {
		case "-f", "--follow", "-w", "--watch":
			return true
		}
	}
	return false
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

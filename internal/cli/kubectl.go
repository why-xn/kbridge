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
  kb kubectl get pods
  kb kubectl get pods -n kube-system
  kb kubectl describe node my-node
  kb kubectl logs my-pod -f`,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	RunE:               runKubectl,
}

// kCmd is an alias for kubectl
var kCmd = &cobra.Command{
	Use:   "k [args...]",
	Short: "Alias for kubectl",
	Long: `Alias for the kubectl command.

All arguments are passed through to kubectl on the remote cluster.

Examples:
  kb k get pods
  kb k get svc -A`,
	DisableFlagParsing: true,
	SilenceUsage:       true,
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

	// --reason is ours, not kubectl's: strip it before the command travels.
	reason, args := extractReason(args)

	// Check if this is an edit command - handle specially
	if isEditCommand(args) {
		return runKubectlEdit(args, reason)
	}

	// Interactive exec (-i/-t) needs a bidirectional stream, not the one-shot path.
	if tgt, ok := parseExecArgs(args); ok {
		tgt.reason = reason
		return execInteractiveFromConfig(tgt)
	}

	// Port-forward needs a bidi HTTP/2 stream with per-connection multiplexing.
	if tgt, ok := parsePortForwardArgs(args); ok {
		tgt.reason = reason
		return portForwardFromConfig(tgt)
	}

	// Check control plane URL
	controlPlaneURL := viper.GetString(ConfigKeyControlPlaneURL)
	if controlPlaneURL == "" {
		return errNoControlPlaneURL()
	}

	// Check current cluster
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)
	if currentCluster == "" {
		return fmt.Errorf("no cluster selected. Run 'kb clusters use <name>' first")
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
		return runStreamingKubectl(controlPlaneURL, currentCluster, args, namespace, reason)
	}

	// Local manifests (-f file/dir/-) are read here and sent as stdin.
	args, manifests, hasManifests, err := localManifests(args, os.Stdin)
	if err != nil {
		return err
	}

	// Create client with longer timeout for command execution
	client := newAuthenticatedClientWithTimeout(controlPlaneURL, defaultKubectlTimeout+10*time.Second)
	client.SetReason(reason)

	// Execute the command
	timeout := int(defaultKubectlTimeout.Seconds())
	var resp *ExecResponse
	if hasManifests {
		resp, err = client.ExecCommandWithStdin(currentCluster, args, namespace, timeout, string(manifests))
	} else {
		resp, err = client.ExecCommand(currentCluster, args, namespace, timeout)
	}
	if err != nil {
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

// runStreamingKubectl streams a follow/watch command and exits with the remote
// kubectl's exit code, so failures are not reported as success.
func runStreamingKubectl(controlPlaneURL, cluster string, args []string, namespace, reason string) error {
	if _, sources := splitFilenameFlags(args); len(sources) > 0 {
		return fmt.Errorf("watching resources given with -f is not supported; name the resources instead")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	client := newAuthenticatedClient(controlPlaneURL)
	client.SetReason(reason)
	code, err := client.StreamCommand(ctx, cluster, args, namespace, os.Stdout)
	if err != nil {
		return err
	}
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// isStreamingCommand reports whether args use a follow/watch flag and should be
// streamed rather than run one-shot. -f means --follow only for logs; for every
// other verb it is --filename.
func isStreamingCommand(args []string) bool {
	follow := kubectlVerb(args) == "logs"
	for _, a := range args {
		switch a {
		case "-w", "--watch", "--watch=true", "--watch-only", "--watch-only=true":
			return true
		case "-f", "--follow", "--follow=true":
			if follow {
				return true
			}
		}
	}
	return false
}

// runKubectlEdit handles the kubectl edit command using a local editor workflow.
func runKubectlEdit(args []string, reason string) error {
	handler, err := NewEditHandler(args)
	if err != nil {
		return err
	}
	handler.SetReason(reason)
	return handler.Execute()
}

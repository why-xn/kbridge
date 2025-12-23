package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// clustersCmd represents the clusters command
var clustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "Manage cluster connections",
	Long: `Manage connections to Kubernetes clusters.

Use subcommands to list available clusters or select a cluster to use.`,
}

// clustersListCmd represents the 'clusters list' subcommand
var clustersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available clusters",
	Long:  `List all Kubernetes clusters available through the central service.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Cluster listing not implemented yet")
	},
}

// clustersUseCmd represents the 'clusters use' subcommand
var clustersUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active cluster",
	Long:  `Set the active cluster for subsequent kubectl commands.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		clusterName := args[0]
		fmt.Printf("Selected cluster: %s\n", clusterName)
	},
}

func init() {
	rootCmd.AddCommand(clustersCmd)
	clustersCmd.AddCommand(clustersListCmd)
	clustersCmd.AddCommand(clustersUseCmd)
}

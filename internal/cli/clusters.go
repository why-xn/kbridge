package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// clustersCmd represents the clusters command
var clustersCmd = &cobra.Command{
	Use:   "clusters",
	Short: "Manage cluster connections",
	Long: `Manage cluster connections to Kubernetes clusters.

Use subcommands to list available clusters or select a cluster to use.`,
}

// clustersListCmd represents the 'clusters list' subcommand
var clustersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List available clusters",
	Long:    `List available clusters from the central service.`,
	RunE:    runClustersList,
}

// clustersUseCmd represents the 'clusters use' subcommand
var clustersUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active cluster",
	Long:  `Set the active cluster for subsequent kubectl commands.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runClustersUse,
}

func init() {
	rootCmd.AddCommand(clustersCmd)
	clustersCmd.AddCommand(clustersListCmd)
	clustersCmd.AddCommand(clustersUseCmd)
}

func runClustersList(cmd *cobra.Command, args []string) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central URL not configured. Run 'kbridge login' first or set %s", ConfigKeyCentralURL)
	}

	client := newAuthenticatedClient(centralURL)
	clusters, err := client.ListClusters()
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	if len(clusters) == 0 {
		fmt.Println("No clusters available.")
		return nil
	}

	currentCluster := viper.GetString(ConfigKeyCurrentCluster)

	// Print clusters in table format
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CURRENT\tNAME\tSTATUS\tVERSION\tNODES\tREGION\tPROVIDER")

	for _, c := range clusters {
		current := ""
		if c.Name == currentCluster {
			current = "*"
		}

		version := c.KubernetesVersion
		if version == "" {
			version = "-"
		}

		nodes := "-"
		if c.NodeCount > 0 {
			nodes = fmt.Sprintf("%d", c.NodeCount)
		}

		region := c.Region
		if region == "" {
			region = "-"
		}

		provider := c.Provider
		if provider == "" {
			provider = "-"
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			current, c.Name, c.Status, version, nodes, region, provider)
	}

	w.Flush()
	return nil
}

func runClustersUse(cmd *cobra.Command, args []string) error {
	clusterName := args[0]

	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central URL not configured. Run 'kbridge login' first or set %s", ConfigKeyCentralURL)
	}

	// Verify cluster exists and is connected
	client := newAuthenticatedClient(centralURL)
	cluster, err := client.GetCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to verify cluster: %w", err)
	}

	if cluster.Status != "connected" {
		fmt.Printf("Warning: cluster %q is currently %s\n", clusterName, cluster.Status)
	}

	// Save to config
	viper.Set(ConfigKeyCurrentCluster, clusterName)
	if err := saveConfig(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Switched to cluster %q.\n", clusterName)
	return nil
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current connection status",
	Long: `Display the current connection status including:
  - Central service URL
  - Selected cluster
  - Connection state`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)

	fmt.Println("mk8s Status")
	fmt.Println("-----------")

	// Central service status
	if centralURL == "" {
		fmt.Println("Central:  (not configured)")
	} else {
		fmt.Printf("Central:  %s\n", centralURL)

		// Check connection
		client := NewCentralClient(centralURL)
		if err := client.CheckHealth(); err != nil {
			fmt.Println("Status:   Disconnected")
		} else {
			fmt.Println("Status:   Connected")
		}
	}

	// Current cluster
	if currentCluster == "" {
		fmt.Println("Cluster:  (none selected)")
	} else {
		fmt.Printf("Cluster:  %s\n", currentCluster)

		// If we have central URL, fetch cluster status
		if centralURL != "" {
			client := NewCentralClient(centralURL)
			cluster, err := client.GetCluster(currentCluster)
			if err != nil {
				fmt.Printf("          (cluster not found: %v)\n", err)
			} else {
				fmt.Printf("          Status: %s\n", cluster.Status)
				if cluster.KubernetesVersion != "" {
					fmt.Printf("          Version: %s\n", cluster.KubernetesVersion)
				}
			}
		}
	}

	return nil
}

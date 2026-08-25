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
	Long: `Show current connection status including:
  - ControlPlane service URL
  - Selected cluster
  - Connection state`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	controlPlaneURL := viper.GetString(ConfigKeyControlPlaneURL)
	currentCluster := viper.GetString(ConfigKeyCurrentCluster)

	fmt.Println("kbridge Status")
	fmt.Println("-----------")

	// ControlPlane service status
	if controlPlaneURL == "" {
		fmt.Println("ControlPlane:  (not configured)")
	} else {
		fmt.Printf("ControlPlane:  %s\n", controlPlaneURL)

		// Check connection
		client := newAuthenticatedClient(controlPlaneURL)
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

		// If we have control plane URL, fetch cluster status
		if controlPlaneURL != "" {
			client := newAuthenticatedClient(controlPlaneURL)
			cluster, err := client.GetCluster(currentCluster)
			if err != nil {
				fmt.Printf("          (cluster not found: %v)\n", err)
			} else {
				fmt.Printf("          Status: %s\n", cluster.Status)
			}
		}
	}

	return nil
}

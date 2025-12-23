package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current connection status",
	Long: `Display the current connection status including:
  - Logged in user
  - Selected cluster
  - Connection state`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("mk8s Status")
		fmt.Println("-----------")
		fmt.Println("User:    (not logged in)")
		fmt.Println("Cluster: (none selected)")
		fmt.Println("Status:  Disconnected")
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

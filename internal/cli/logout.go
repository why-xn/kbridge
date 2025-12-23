package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// logoutCmd represents the logout command
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear authentication credentials",
	Long: `Clear stored authentication credentials.

This command removes any stored tokens and clears the current session.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Logout not implemented yet")
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}

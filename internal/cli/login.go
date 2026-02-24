package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the central service",
	Long: `Authenticate with the kbridge central service.

This command will prompt for the central server URL (if not configured),
email, and password to authenticate and obtain an access token.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Login not implemented yet")
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// logoutCmd represents the logout command
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Clear authentication credentials",
	Long: `Clear stored authentication credentials.

This command removes any stored tokens and clears the current session.`,
	RunE: runLogout,
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}

func runLogout(cmd *cobra.Command, args []string) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	token := viper.GetString(ConfigKeyToken)
	refreshToken := viper.GetString(ConfigKeyRefreshToken)

	// Try to invalidate the refresh token on the server
	if centralURL != "" && token != "" && refreshToken != "" {
		client := NewCentralClient(centralURL)
		client.SetToken(token)
		// Best-effort: don't fail if server is unreachable
		_ = client.Logout(refreshToken)
	}

	// Clear tokens from config
	viper.Set(ConfigKeyToken, "")
	viper.Set(ConfigKeyRefreshToken, "")
	if err := saveConfig(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println("Logged out.")
	return nil
}

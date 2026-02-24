package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the central service",
	Long: `Authenticate with the kbridge central service.

This command will prompt for email and password to authenticate
and obtain an access token.`,
	RunE: runLogin,
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, args []string) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central_url not configured: run 'kbridge config set central_url <url>'")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading email: %w", err)
	}
	email = strings.TrimSpace(email)

	fmt.Print("Password: ")
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	password := string(passwordBytes)

	client := NewCentralClient(centralURL)
	resp, err := client.Login(email, password)
	if err != nil {
		return err
	}

	// Store tokens in config
	viper.Set(ConfigKeyToken, resp.AccessToken)
	viper.Set(ConfigKeyRefreshToken, resp.RefreshToken)
	if err := saveConfig(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Println("Login successful.")
	return nil
}

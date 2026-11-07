package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

// loginCmd represents the login command
var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with the control plane",
	Long: `Authenticate with the kbridge control plane.

This command will prompt for email and password to authenticate
and obtain an access token.`,
	RunE: runLogin,
}

func init() {
	rootCmd.AddCommand(loginCmd)
}

func runLogin(cmd *cobra.Command, args []string) error {
	reader := bufio.NewReader(os.Stdin)
	controlPlaneURL := viper.GetString(ConfigKeyControlPlaneURL)
	if controlPlaneURL == "" {
		var err error
		if controlPlaneURL, err = promptControlPlaneURL(reader, os.Stdout); errors.Is(err, io.EOF) {
			return errNoControlPlaneURL()
		} else if err != nil {
			return err
		}
		viper.Set(ConfigKeyControlPlaneURL, controlPlaneURL)
	}

	fmt.Print("Email: ")
	email, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading email: %w", err)
	}
	email = strings.TrimSpace(email)

	fmt.Print("Password: ")
	password, err := readPassword(reader, int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}

	client := NewControlPlaneClient(controlPlaneURL)
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

// promptControlPlaneURL asks for the URL on first login so no config file edit is needed.
func promptControlPlaneURL(in *bufio.Reader, out io.Writer) (string, error) {
	fmt.Fprint(out, "Control plane URL: ")
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading control plane URL: %w", err)
	}
	if line = strings.TrimSpace(line); line == "" {
		return "", errors.New("control plane URL is required")
	}
	return line, nil
}

// readPassword hides input on a terminal and reads one line from a pipe.
func readPassword(in *bufio.Reader, fd int) (string, error) {
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		return string(b), err
	}
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

// adminCmd is the parent for administrative commands.
var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Administrative commands",
	Long:  `Administrative commands for managing users (requires the admin role).`,
}

var adminUsersCmd = &cobra.Command{
	Use:   "users",
	Short: "Manage users",
}

var adminUsersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all users",
	RunE:    runAdminUsersList,
}

var (
	createUserEmail    string
	createUserName     string
	createUserPassword string
)

var adminUsersCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user",
	Long:  `Create a new user. If --password is omitted, you will be prompted for it.`,
	RunE:  runAdminUsersCreate,
}

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(adminUsersCmd)
	adminUsersCmd.AddCommand(adminUsersListCmd)
	adminUsersCmd.AddCommand(adminUsersCreateCmd)

	adminUsersCreateCmd.Flags().StringVar(&createUserEmail, "email", "", "user email (required)")
	adminUsersCreateCmd.Flags().StringVar(&createUserName, "name", "", "user display name (required)")
	adminUsersCreateCmd.Flags().StringVar(&createUserPassword, "password", "", "user password (prompted if omitted)")
	adminUsersCreateCmd.MarkFlagRequired("email")
	adminUsersCreateCmd.MarkFlagRequired("name")
}

func adminClient() (*CentralClient, error) {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return nil, fmt.Errorf("central URL not configured. Run 'kbridge login' first or set %s", ConfigKeyCentralURL)
	}
	return newAuthenticatedClient(centralURL), nil
}

func runAdminUsersList(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	users, err := client.ListUsers()
	if err != nil {
		return fmt.Errorf("failed to list users: %w", err)
	}
	if len(users) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "EMAIL\tNAME\tACTIVE\tID")
	for _, u := range users {
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\n", u.Email, u.Name, u.IsActive, u.ID)
	}
	return w.Flush()
}

func runAdminUsersCreate(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}

	password := createUserPassword
	if password == "" {
		password, err = promptPassword("Password: ")
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("password must not be empty")
	}

	user, err := client.CreateUser(createUserEmail, createUserName, password)
	if err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}
	fmt.Printf("Created user %q (%s).\n", user.Email, user.ID)
	return nil
}

// promptPassword reads a password from the terminal without echoing it.
func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(syscall.Stdin)) {
		b, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("reading password: %w", err)
		}
		return string(b), nil
	}
	// Non-interactive (e.g. piped input): read a line.
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), nil
}

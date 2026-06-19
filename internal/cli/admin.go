package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
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

var adminTokensCmd = &cobra.Command{
	Use:     "agent-tokens",
	Aliases: []string{"agent-token", "tokens"},
	Short:   "Manage agent registration tokens",
}

var (
	tokenCluster     string
	tokenDescription string
	tokenExpiresDays int
)

var adminTokensCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Generate an agent token for a cluster",
	Long:  `Generate an agent token bound to a cluster. The token is printed once and cannot be retrieved again.`,
	RunE:  runAdminTokensCreate,
}

var adminTokensListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List agent tokens",
	RunE:    runAdminTokensList,
}

var adminTokensRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an agent token by ID",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdminTokensRevoke,
}

var (
	auditUser    string
	auditCluster string
	auditStatus  string
	auditLimit   int
)

var adminAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "View command audit logs",
	Long:  `View the audit log of kubectl commands, optionally filtered by user, cluster, or status.`,
	RunE:  runAdminAudit,
}

func init() {
	rootCmd.AddCommand(adminCmd)
	adminCmd.AddCommand(adminUsersCmd)
	adminUsersCmd.AddCommand(adminUsersListCmd)
	adminUsersCmd.AddCommand(adminUsersCreateCmd)
	adminCmd.AddCommand(adminAuditCmd)

	adminCmd.AddCommand(adminTokensCmd)
	adminTokensCmd.AddCommand(adminTokensCreateCmd)
	adminTokensCmd.AddCommand(adminTokensListCmd)
	adminTokensCmd.AddCommand(adminTokensRevokeCmd)
	adminTokensCreateCmd.Flags().StringVar(&tokenCluster, "cluster", "", "cluster the token is bound to (required)")
	adminTokensCreateCmd.Flags().StringVar(&tokenDescription, "description", "", "optional description")
	adminTokensCreateCmd.Flags().IntVar(&tokenExpiresDays, "expires-in-days", 0, "optional expiry in days (0 = no expiry)")
	adminTokensCreateCmd.MarkFlagRequired("cluster")
	adminTokensListCmd.Flags().StringVar(&tokenCluster, "cluster", "", "filter by cluster name")

	adminUsersCreateCmd.Flags().StringVar(&createUserEmail, "email", "", "user email (required)")
	adminUsersCreateCmd.Flags().StringVar(&createUserName, "name", "", "user display name (required)")
	adminUsersCreateCmd.Flags().StringVar(&createUserPassword, "password", "", "user password (prompted if omitted)")
	adminUsersCreateCmd.MarkFlagRequired("email")
	adminUsersCreateCmd.MarkFlagRequired("name")

	adminAuditCmd.Flags().StringVar(&auditUser, "user", "", "filter by user email")
	adminAuditCmd.Flags().StringVar(&auditCluster, "cluster", "", "filter by cluster name")
	adminAuditCmd.Flags().StringVar(&auditStatus, "status", "", "filter by status (success/failed/denied/timeout)")
	adminAuditCmd.Flags().IntVar(&auditLimit, "limit", 50, "maximum number of entries to show")
}

func adminClient() (*CentralClient, error) {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return nil, fmt.Errorf("central URL not configured. Run 'kb login' first or set %s", ConfigKeyCentralURL)
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
	fmt.Fprintln(w, "EMAIL\tNAME\tACTIVE\tADMIN\tID")
	for _, u := range users {
		fmt.Fprintf(w, "%s\t%s\t%t\t%t\t%s\n", u.Email, u.Name, u.IsActive, u.IsAdmin, u.ID)
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

func runAdminAudit(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}

	filters := map[string]string{
		"user":     auditUser,
		"cluster":  auditCluster,
		"status":   auditStatus,
		"per_page": strconv.Itoa(auditLimit),
	}
	logs, total, err := client.ListAuditLogs(filters)
	if err != nil {
		return fmt.Errorf("failed to fetch audit logs: %w", err)
	}
	if len(logs) == 0 {
		fmt.Println("No audit logs found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tUSER\tCLUSTER\tSTATUS\tEXIT\tCOMMAND")
	for _, l := range logs {
		exit := "-"
		if l.ExitCode != nil {
			exit = strconv.Itoa(int(*l.ExitCode))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			l.CreatedAt, l.UserEmail, l.ClusterName, l.Status, exit, l.Command)
	}
	w.Flush()
	fmt.Printf("\nShowing %d of %d entries.\n", len(logs), total)
	return nil
}

func runAdminTokensCreate(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	tok, err := client.CreateAgentToken(tokenCluster, tokenDescription, tokenExpiresDays)
	if err != nil {
		return fmt.Errorf("failed to create agent token: %w", err)
	}
	fmt.Printf("Agent token for cluster %q created.\n\n", tok.ClusterName)
	fmt.Printf("  %s\n\n", tok.Token)
	fmt.Println("Store it now — it cannot be retrieved again. Set it as the agent's central.token.")
	return nil
}

func runAdminTokensList(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	tokens, err := client.ListAgentTokens(tokenCluster)
	if err != nil {
		return fmt.Errorf("failed to list agent tokens: %w", err)
	}
	if len(tokens) == 0 {
		fmt.Println("No agent tokens found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tCLUSTER\tPREFIX\tREVOKED\tDESCRIPTION")
	for _, tok := range tokens {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n",
			tok.ID, tok.ClusterName, tok.TokenPrefix, tok.IsRevoked, tok.Description)
	}
	return w.Flush()
}

func runAdminTokensRevoke(cmd *cobra.Command, args []string) error {
	client, err := adminClient()
	if err != nil {
		return err
	}
	if err := client.RevokeAgentToken(args[0]); err != nil {
		return fmt.Errorf("failed to revoke agent token: %w", err)
	}
	fmt.Printf("Agent token %q revoked.\n", args[0])
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

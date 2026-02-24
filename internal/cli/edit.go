package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EditHandler handles the kubectl edit command locally.
// It fetches the resource YAML, opens it in an editor, and applies changes.
type EditHandler struct {
	client         *CentralClient
	clusterName    string
	timeout        time.Duration
	originalArgs   []string
	resourceType   string
	resourceName   string
	namespace      string
	outputFormat   string
	additionalArgs []string
}

// NewEditHandler creates a new edit handler from kubectl edit arguments.
func NewEditHandler(args []string) (*EditHandler, error) {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return nil, fmt.Errorf("central URL not configured. Run 'kbridge login' first")
	}

	clusterName := viper.GetString(ConfigKeyCurrentCluster)
	if clusterName == "" {
		return nil, fmt.Errorf("no cluster selected. Run 'kbridge clusters use <name>' first")
	}

	h := &EditHandler{
		client:       newAuthenticatedClientWithTimeout(centralURL, defaultKubectlTimeout+10*time.Second),
		clusterName:  clusterName,
		timeout:      defaultKubectlTimeout,
		originalArgs: args,
	}

	if err := h.parseArgs(args); err != nil {
		return nil, err
	}

	return h, nil
}

// parseArgs parses the edit command arguments to extract resource info.
func (h *EditHandler) parseArgs(args []string) error {
	// Skip "edit" verb
	if len(args) == 0 {
		return fmt.Errorf("edit requires a resource type and name")
	}

	remaining := args
	if remaining[0] == "edit" {
		remaining = remaining[1:]
	}

	// Parse flags and positional arguments
	for i := 0; i < len(remaining); i++ {
		arg := remaining[i]

		switch {
		case arg == "-n" || arg == "--namespace":
			if i+1 >= len(remaining) {
				return fmt.Errorf("flag %s requires a value", arg)
			}
			h.namespace = remaining[i+1]
			i++
		case strings.HasPrefix(arg, "-n="):
			h.namespace = strings.TrimPrefix(arg, "-n=")
		case strings.HasPrefix(arg, "--namespace="):
			h.namespace = strings.TrimPrefix(arg, "--namespace=")
		case arg == "-o" || arg == "--output":
			if i+1 >= len(remaining) {
				return fmt.Errorf("flag %s requires a value", arg)
			}
			h.outputFormat = remaining[i+1]
			i++
		case strings.HasPrefix(arg, "-o="):
			h.outputFormat = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "--output="):
			h.outputFormat = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-"):
			// Collect other flags for later use
			h.additionalArgs = append(h.additionalArgs, arg)
			// Check if it's a flag with a value
			if i+1 < len(remaining) && !strings.HasPrefix(remaining[i+1], "-") {
				h.additionalArgs = append(h.additionalArgs, remaining[i+1])
				i++
			}
		default:
			// Positional argument: could be "type/name" or "type name"
			if strings.Contains(arg, "/") {
				parts := strings.SplitN(arg, "/", 2)
				h.resourceType = parts[0]
				h.resourceName = parts[1]
			} else if h.resourceType == "" {
				h.resourceType = arg
			} else if h.resourceName == "" {
				h.resourceName = arg
			}
		}
	}

	if h.resourceType == "" {
		return fmt.Errorf("edit requires a resource type")
	}
	if h.resourceName == "" {
		return fmt.Errorf("edit requires a resource name")
	}

	return nil
}

// Execute performs the edit workflow: fetch, edit locally, apply.
func (h *EditHandler) Execute() error {
	// Step 1: Fetch the current resource YAML
	yaml, err := h.fetchResource()
	if err != nil {
		return fmt.Errorf("failed to fetch resource: %w", err)
	}

	// Step 2: Write to temp file
	tmpFile, err := h.createTempFile(yaml)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile)

	// Step 3: Get original content hash for comparison
	originalContent := yaml

	// Step 4: Open editor
	if err := h.openEditor(tmpFile); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}

	// Step 5: Read modified content
	modifiedContent, err := os.ReadFile(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to read modified file: %w", err)
	}

	// Step 6: Check if content changed
	if string(modifiedContent) == originalContent {
		fmt.Println("Edit cancelled, no changes made.")
		return nil
	}

	// Step 7: Apply the changes
	if err := h.applyChanges(modifiedContent); err != nil {
		return fmt.Errorf("failed to apply changes: %w", err)
	}

	return nil
}

// fetchResource retrieves the resource YAML from the cluster.
func (h *EditHandler) fetchResource() (string, error) {
	// Build get command: get <type>/<name> -o yaml
	args := []string{"get", fmt.Sprintf("%s/%s", h.resourceType, h.resourceName), "-o", "yaml"}
	args = append(args, h.additionalArgs...)

	resp, err := h.client.ExecCommand(h.clusterName, args, h.namespace, int(h.timeout.Seconds()))
	if err != nil {
		return "", err
	}

	if resp.ExitCode != 0 {
		if resp.Error != "" {
			return "", fmt.Errorf("%s", resp.Error)
		}
		if resp.Output != "" {
			return "", fmt.Errorf("%s", strings.TrimSpace(resp.Output))
		}
		return "", fmt.Errorf("command failed with exit code %d", resp.ExitCode)
	}

	return resp.Output, nil
}

// createTempFile creates a temporary file with the resource YAML.
func (h *EditHandler) createTempFile(content string) (string, error) {
	// Create temp file with descriptive name
	filename := fmt.Sprintf("kbridge-edit-%s-%s-*.yaml", h.resourceType, h.resourceName)
	tmpFile, err := os.CreateTemp("", filename)
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(content); err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

// openEditor opens the specified file in the user's preferred editor.
func (h *EditHandler) openEditor(filepath string) error {
	editor := getEditor()

	cmd := exec.Command(editor, filepath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// getEditor returns the user's preferred editor.
func getEditor() string {
	// Check KUBE_EDITOR first (kubectl convention)
	if editor := os.Getenv("KUBE_EDITOR"); editor != "" {
		return editor
	}

	// Then EDITOR
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}

	// Then VISUAL
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}

	// Default to vi
	return "vi"
}

// applyChanges applies the modified YAML to the cluster.
func (h *EditHandler) applyChanges(content []byte) error {
	// Create temp file for apply
	tmpFile, err := os.CreateTemp("", "kbridge-apply-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Read the file content for sending to central
	fileContent, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("failed to read temp file: %w", err)
	}

	// Use apply with stdin via --filename with the content
	// We send the content as part of the command using apply -f -
	args := []string{"apply", "-f", "-"}

	resp, err := h.client.ExecCommandWithStdin(h.clusterName, args, h.namespace, int(h.timeout.Seconds()), string(fileContent))
	if err != nil {
		return err
	}

	// Print output
	if resp.Output != "" {
		fmt.Print(resp.Output)
	}

	if resp.ExitCode != 0 {
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		return fmt.Errorf("apply failed with exit code %d", resp.ExitCode)
	}

	return nil
}

// isEditCommand checks if the given args represent an edit command.
func isEditCommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "edit" {
			return true
		}
		// Skip flag values
		if arg == "-n" || arg == "--namespace" || arg == "-o" || arg == "--output" {
			i++ // skip the next arg which is the flag value
			continue
		}
		// Skip flags with values attached
		if strings.HasPrefix(arg, "-") {
			continue
		}
		// We hit a non-flag argument that isn't "edit", so edit is not the verb
		return false
	}
	return false
}

// resourceIdentifier returns a string identifying the resource being edited.
func (h *EditHandler) resourceIdentifier() string {
	id := fmt.Sprintf("%s/%s", h.resourceType, h.resourceName)
	if h.namespace != "" {
		id = fmt.Sprintf("%s (namespace: %s)", id, h.namespace)
	}
	return id
}

// TempFilePath returns a safe temp file path for the resource.
func (h *EditHandler) TempFilePath() string {
	safeName := strings.ReplaceAll(h.resourceName, "/", "-")
	safeType := strings.ReplaceAll(h.resourceType, "/", "-")
	return filepath.Join(os.TempDir(), fmt.Sprintf("kbridge-edit-%s-%s.yaml", safeType, safeName))
}

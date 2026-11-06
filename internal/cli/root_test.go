package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
)

func TestExecute_PrintsErrorOnce(t *testing.T) {
	failing := &cobra.Command{Use: "fail-for-test", RunE: func(*cobra.Command, []string) error {
		return errors.New("boom")
	}}
	rootCmd.AddCommand(failing)
	defer rootCmd.RemoveCommand(failing)

	var stderr bytes.Buffer
	code := execute([]string{"fail-for-test"}, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := stderr.String(); got != "Error: boom\n" {
		t.Errorf("stderr = %q, want exactly one Error line", got)
	}
}

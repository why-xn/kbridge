package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// CommandResult holds the result of a kubectl command execution.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	Error    error
}

// KubectlExecutor executes kubectl commands on the local cluster.
type KubectlExecutor struct {
	kubectlPath string
}

// NewKubectlExecutor creates a new kubectl executor.
func NewKubectlExecutor() *KubectlExecutor {
	return &KubectlExecutor{
		kubectlPath: "kubectl",
	}
}

// Execute runs a kubectl command with the given arguments.
func (e *KubectlExecutor) Execute(ctx context.Context, args []string, namespace string, timeout time.Duration) *CommandResult {
	return e.ExecuteWithStdin(ctx, args, namespace, timeout, nil)
}

// ExecuteWithStdin runs a kubectl command with optional stdin input.
func (e *KubectlExecutor) ExecuteWithStdin(ctx context.Context, args []string, namespace string, timeout time.Duration, stdin []byte) *CommandResult {
	result := &CommandResult{}

	// Build command arguments
	cmdArgs := make([]string, 0, len(args)+2)
	if namespace != "" {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmdArgs = append(cmdArgs, args...)

	// Create command with context for timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, e.kubectlPath, cmdArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set stdin if provided
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	// Run the command
	err := cmd.Run()

	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = fmt.Errorf("failed to execute kubectl: %w", err)
		}
	}

	return result
}

// ExecuteStreaming runs a kubectl command and streams output via callbacks.
func (e *KubectlExecutor) ExecuteStreaming(ctx context.Context, args []string, namespace string, timeout time.Duration, onStdout, onStderr func([]byte)) *CommandResult {
	result := &CommandResult{}

	// Build command arguments
	cmdArgs := make([]string, 0, len(args)+2)
	if namespace != "" {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmdArgs = append(cmdArgs, args...)

	// Create command with context for timeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, e.kubectlPath, cmdArgs...)

	// Get pipes for streaming
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to create stdout pipe: %w", err)
		result.ExitCode = -1
		return result
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		result.Error = fmt.Errorf("failed to create stderr pipe: %w", err)
		result.ExitCode = -1
		return result
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("failed to start kubectl: %w", err)
		result.ExitCode = -1
		return result
	}

	// Read stdout and stderr concurrently
	var allStdout, allStderr bytes.Buffer
	done := make(chan struct{}, 2)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				allStdout.Write(data)
				if onStdout != nil {
					onStdout(data)
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				allStderr.Write(data)
				if onStderr != nil {
					onStderr(data)
				}
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()

	// Wait for both readers to finish
	<-done
	<-done

	// Wait for command to complete
	err = cmd.Wait()
	result.Stdout = allStdout.Bytes()
	result.Stderr = allStderr.Bytes()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = fmt.Errorf("command execution failed: %w", err)
		}
	}

	return result
}

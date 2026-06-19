package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
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

// streamChunkSize bounds how much output is buffered before a chunk is emitted.
const streamChunkSize = 32 * 1024

// ExecuteStream runs a kubectl command, invoking onChunk for each piece of
// output as it is produced. Cancelling ctx kills the process.
// onChunk may be called concurrently from the stdout and stderr readers;
// callers must synchronize access to any shared state touched inside onChunk.
func (e *KubectlExecutor) ExecuteStream(ctx context.Context, args []string, namespace string, onChunk func(stdout bool, data []byte)) (int, error) {
	cmdArgs := args
	if namespace != "" {
		cmdArgs = append([]string{"-n", namespace}, args...)
	}
	cmd := exec.CommandContext(ctx, e.kubectlPath, cmdArgs...)
	// WaitDelay ensures pipes are force-closed after context cancellation so that
	// any goroutines blocked on Read are unblocked even if child processes
	// inherited and still hold the pipe handles.
	cmd.WaitDelay = 500 * time.Millisecond
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("starting kubectl: %w", err)
	}

	// waitCh carries the result of cmd.Wait so we can unblock pipe readers.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var wg sync.WaitGroup
	wg.Add(2)
	go pumpStream(&wg, stdout, true, onChunk)
	go pumpStream(&wg, stderr, false, onChunk)
	wg.Wait()

	waitErr := <-waitCh
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("kubectl wait: %w", waitErr)
	}
	return 0, nil
}

func pumpStream(wg *sync.WaitGroup, r io.Reader, stdout bool, onChunk func(bool, []byte)) {
	defer wg.Done()
	buf := make([]byte, streamChunkSize)
	reader := bufio.NewReader(r)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			onChunk(stdout, chunk)
		}
		if err != nil {
			return
		}
	}
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

// ExecuteInteractive runs a kubectl command attached to a PTY, pumping stdin and
// window-resize events in and output out, until the process exits or ctx is
// cancelled (which kills the child). onOutput is called only from the single
// read-loop goroutine, so it is never called concurrently.
func (e *KubectlExecutor) ExecuteInteractive(ctx context.Context, args []string, namespace string, rows, cols uint16, stdin <-chan []byte, resize <-chan [2]uint16, onOutput func([]byte)) (int, error) {
	cmdArgs := args
	if namespace != "" {
		cmdArgs = append([]string{"-n", namespace}, args...)
	}
	cmd := exec.CommandContext(ctx, e.kubectlPath, cmdArgs...)
	cmd.WaitDelay = 500 * time.Millisecond

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return -1, fmt.Errorf("starting pty: %w", err)
	}
	defer ptmx.Close()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-stdin:
				if !ok {
					return
				}
				_, _ = ptmx.Write(data)
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ws, ok := <-resize:
				if !ok {
					return
				}
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: ws[0], Cols: ws[1]})
			}
		}
	}()

	buf := make([]byte, streamChunkSize)
	for {
		n, rerr := ptmx.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			onOutput(chunk)
		}
		if rerr != nil {
			break // EOF/EIO when the child exits or the PTY closes
		}
	}

	if werr := cmd.Wait(); werr != nil {
		if exitErr, ok := werr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, fmt.Errorf("kubectl wait: %w", werr)
	}
	return 0, nil
}

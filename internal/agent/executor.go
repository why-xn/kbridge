package agent

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
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

// streamDrainGrace bounds how long the pipe readers are given to finish after
// the process exits before their read ends are force-closed.
const streamDrainGrace = 500 * time.Millisecond

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
	// WaitDelay bounds cancellation: if the process outlives the interrupt,
	// os/exec force-kills it this long after ctx is done.
	cmd.WaitDelay = 500 * time.Millisecond

	outR, errR, closeWriters, err := attachStreamPipes(cmd)
	if err != nil {
		return -1, err
	}
	defer func() { outR.Close(); errR.Close() }()

	if err := cmd.Start(); err != nil {
		closeWriters()
		return -1, fmt.Errorf("starting kubectl: %w", err)
	}
	// The child holds the write ends now; drop the parent's copies so the
	// readers see EOF once the process (and any inheritor) is gone.
	closeWriters()

	return drainStream(cmd, outR, errR, onChunk)
}

// attachStreamPipes wires fresh os.Pipe pairs to cmd's stdout and stderr,
// returning the parent's read ends and a closer for the parent's write ends.
//
// It deliberately avoids cmd.StdoutPipe/StderrPipe: Wait closes the parent end
// of an exec-owned pipe as soon as the process exits, which races the readers
// and silently truncates output, while WaitDelay does not cover those pipes at
// all (os/exec only force-closes them when it created copy goroutines of its
// own, which it does not do for StdoutPipe). Owning the pipes puts both the
// truncation and the unblocking under our control.
func attachStreamPipes(cmd *exec.Cmd) (outR, errR *os.File, closeWriters func(), err error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	return outR, errR, func() { outW.Close(); errW.Close() }, nil
}

// drainStream pumps both pipes until the process exits and the readers drain.
// Nothing else closes these pipes, so if a grandchild inherited a write end and
// holds it open, the read ends are force-closed after streamDrainGrace.
func drainStream(cmd *exec.Cmd, outR, errR *os.File, onChunk func(bool, []byte)) (int, error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go pumpStream(&wg, outR, true, onChunk)
	go pumpStream(&wg, errR, false, onChunk)

	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()

	waitErr := cmd.Wait()
	select {
	case <-drained:
	case <-time.After(streamDrainGrace):
		outR.Close()
		errR.Close()
		<-drained
	}

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

// ExecuteInteractiveNoTTY runs a kubectl command with piped stdin (no PTY), pumping
// stdin from the channel and streaming stdout/stderr to onOutput. It returns the
// exit code when the process exits or ctx is cancelled.
func (e *KubectlExecutor) ExecuteInteractiveNoTTY(ctx context.Context, args []string, namespace string, stdin <-chan []byte, onOutput func(bool, []byte)) (int, error) {
	// Derive a child context so stdin pump goroutines are guaranteed to exit
	// before the function returns, independent of the caller's cancel timing.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmdArgs := args
	if namespace != "" {
		cmdArgs = append([]string{"-n", namespace}, args...)
	}
	cmd := exec.CommandContext(ctx, e.kubectlPath, cmdArgs...)
	cmd.WaitDelay = 500 * time.Millisecond

	stdinPr, stdinPw, err := os.Pipe()
	if err != nil {
		return -1, fmt.Errorf("stdin pipe: %w", err)
	}
	cmd.Stdin = stdinPr

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPr.Close()
		stdinPw.Close()
		return -1, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		stdinPr.Close()
		stdinPw.Close()
		return -1, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdinPr.Close()
		stdinPw.Close()
		return -1, fmt.Errorf("starting kubectl: %w", err)
	}
	stdinPr.Close() // child owns read end; parent writes to stdinPw

	// Forward stdin channel to the process.
	go func() {
		defer stdinPw.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case data, ok := <-stdin:
				if !ok {
					return
				}
				if _, werr := stdinPw.Write(data); werr != nil {
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go pumpStream(&wg, stdoutPipe, true, onOutput)
	go pumpStream(&wg, stderrPipe, false, onOutput)
	wg.Wait()

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

// ExecuteInteractive runs a kubectl command attached to a PTY, pumping stdin and
// window-resize events in and output out, until the process exits or ctx is
// cancelled (which kills the child). onOutput is called only from the single
// read-loop goroutine, so it is never called concurrently.
func (e *KubectlExecutor) ExecuteInteractive(ctx context.Context, args []string, namespace string, rows, cols uint16, stdin <-chan []byte, resize <-chan [2]uint16, onOutput func([]byte)) (int, error) {
	// Derive a child context so stdin/resize pump goroutines are guaranteed to
	// exit before the function returns, independent of the caller's cancel timing.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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

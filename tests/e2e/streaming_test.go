//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestStreamingLogsFollow verifies kubectl logs -f streams incrementally and
// stops when the client is killed.
func TestStreamingLogsFollow(t *testing.T) {
	// Create a pod that emits a line every second.
	apply := exec.Command("kubectl", "run", "log-emitter", "--image=busybox", "--restart=Never",
		"--", "sh", "-c", "i=0; while true; do echo line-$i; i=$((i+1)); sleep 1; done")
	if out, err := apply.CombinedOutput(); err != nil && !strings.Contains(string(out), "already exists") {
		t.Fatalf("create pod: %v: %s", err, out)
	}
	defer exec.Command("kubectl", "delete", "pod", "log-emitter", "--ignore-not-found", "--force", "--grace-period=0").Run()

	waitPodReady(t, "log-emitter", 60*time.Second)

	// Use a 30s overall safety timeout so the test can't hang forever.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, getCLIPath(), "kubectl", "logs", "-f", "log-emitter")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	// Merge stderr into /dev/null — we only care about stdout lines.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		t.Fatalf("start cmd: %v", err)
	}

	type timestampedLine struct {
		text string
		at   time.Time
	}

	linesCh := make(chan timestampedLine, 32)
	scanDone := make(chan struct{})

	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "line-") {
				linesCh <- timestampedLine{text: line, at: time.Now()}
			}
		}
	}()

	// Collect lines until we see a gap >= 700ms between consecutive arrivals,
	// or until we have enough lines to assert the total span >= 1.5s.
	// We collect up to 8 lines to find evidence of incremental delivery.
	// A non-streaming buffered dump would deliver all lines in one instant.
	var collected []timestampedLine
	collectLoop:
	for {
		select {
		case tl, ok := <-linesCh:
			if !ok {
				break collectLoop
			}
			collected = append(collected, tl)
			// Stop collecting once we have enough to assert incremental delivery.
			if len(collected) >= 8 {
				break collectLoop
			}
			// Also stop early if we've already seen an inter-line gap >= 700ms,
			// which directly proves streaming (two consecutive lines did NOT
			// arrive in a single buffered flush).
			if len(collected) >= 2 {
				gap := collected[len(collected)-1].at.Sub(collected[len(collected)-2].at)
				if gap >= 700*time.Millisecond {
					break collectLoop
				}
			}
		case <-ctx.Done():
			break collectLoop
		}
	}

	// Cancel the context to stop the process, then verify prompt termination.
	cancel()
	<-scanDone // wait for scanner goroutine to finish

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	select {
	case <-waitErr:
		// process exited — good
	case <-time.After(3 * time.Second):
		t.Errorf("process did not terminate within 3s of context cancellation")
	}

	// Assert we received at least 2 line- entries.
	if len(collected) < 2 {
		t.Fatalf("expected at least 2 streamed lines, got %d", len(collected))
	}

	// Assert INCREMENTAL delivery using one of two criteria:
	//
	// (a) At least one consecutive pair arrived >= 700ms apart — this directly
	//     proves streaming: two logically separate "ticks" of the emitter
	//     arrived as separate HTTP chunks, not as a single buffered dump.
	//
	// (b) The total span from first to last line >= 1.5s — this handles the
	//     case where the initial burst delivers the first few lines at once
	//     (due to kubectl's own log buffer accumulating before the stream
	//     connects) but subsequent lines arrive incrementally; 1.5s of total
	//     span still requires at least two distinct "ticks" of the emitter.
	//
	// A truly non-streaming buffered implementation buffers everything until
	// the process exits, then delivers all at once — ALL gaps would be ~0 and
	// the total span would also be ~0, failing BOTH criteria.

	foundGap := false
	for i := 1; i < len(collected); i++ {
		if collected[i].at.Sub(collected[i-1].at) >= 700*time.Millisecond {
			foundGap = true
			break
		}
	}

	totalSpan := collected[len(collected)-1].at.Sub(collected[0].at)
	if !foundGap && totalSpan < 1500*time.Millisecond {
		t.Errorf("lines did not arrive incrementally: no consecutive gap >= 700ms and total span %v < 1500ms across %d lines — not truly streaming?",
			totalSpan, len(collected))
	}
}

func waitPodReady(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("kubectl", "get", "pod", name, "-o", "jsonpath={.status.phase}").Output()
		if strings.TrimSpace(string(out)) == "Running" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("pod %s not Running within %s", name, timeout)
}

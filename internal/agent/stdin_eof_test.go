package agent

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionCancels_CloseStdin(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool // channel closed
	}{
		{"known session is closed", "s1", true},
		{"unknown session is a no-op", "nope", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSessionCancels()
			_, c := context.WithCancel(context.Background())
			stdin := make(chan []byte, 1)
			s.add("s1", c, stdin, nil)

			s.closeStdin(tt.id)
			s.closeStdin(tt.id)              // idempotent: must not panic on double close
			s.stdinTo(tt.id, []byte("late")) // data after EOF is dropped, not sent on a closed channel

			select {
			case _, ok := <-stdin:
				if ok || !tt.want {
					t.Fatalf("closed=%v, want closed=%v", !ok, tt.want)
				}
			default:
				if tt.want {
					t.Fatal("stdin channel was not closed")
				}
			}
		})
	}
}

func TestSessionCancels_StdinToIgnoresEmpty(t *testing.T) {
	s := newSessionCancels()
	_, c := context.WithCancel(context.Background())
	stdin := make(chan []byte, 1)
	s.add("s1", c, stdin, nil)

	s.stdinTo("s1", nil) // an EOF message carries no data
	select {
	case got := <-stdin:
		t.Fatalf("empty data was forwarded: %q", got)
	default:
	}
}

// Closing the session's stdin channel must surface as EOF to the process, so a
// command that reads to EOF (cat, sh reading a piped script) exits by itself.
func TestExecuteInteractiveNoTTY_ExitsOnStdinEOF(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	e := windowedExecutor(t, "cat")
	stdin := make(chan []byte, 1)
	var mu sync.Mutex
	var out []byte
	done := make(chan int, 1)
	go func() {
		code, _ := e.ExecuteInteractiveNoTTY(context.Background(), testID, nil, "", stdin,
			func(_ bool, b []byte) { mu.Lock(); out = append(out, b...); mu.Unlock() })
		done <- code
	}()

	stdin <- []byte("piped\n")
	close(stdin)
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit code %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process did not exit after stdin EOF")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(out), "piped") {
		t.Fatalf("output %q missing piped input", out)
	}
}

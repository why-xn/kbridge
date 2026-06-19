package agent

import (
	"context"
	"testing"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

func TestOutputTypeFor(t *testing.T) {
	if outputTypeFor(true) != agentpb.OutputType_OUTPUT_TYPE_STDOUT {
		t.Error("stdout mismatch")
	}
	if outputTypeFor(false) != agentpb.OutputType_OUTPUT_TYPE_STDERR {
		t.Error("stderr mismatch")
	}
}

func TestSessionCancels_CancelAllOnTeardown(t *testing.T) {
	// Models the stream-teardown path: two in-flight sessions must both be
	// cancelled (so their kubectl processes are killed, not orphaned).
	s := newSessionCancels()
	ctx1, c1 := context.WithCancel(context.Background())
	ctx2, c2 := context.WithCancel(context.Background())
	s.add("a", c1)
	s.add("b", c2)

	if s.len() != 2 {
		t.Fatalf("expected 2 tracked sessions, got %d", s.len())
	}

	s.cancelAll()

	if ctx1.Err() == nil || ctx2.Err() == nil {
		t.Fatal("cancelAll must cancel every tracked session")
	}
	if s.len() != 0 {
		t.Fatalf("cancelAll must forget every session, %d left", s.len())
	}
}

func TestSessionCancels_CancelOne(t *testing.T) {
	s := newSessionCancels()
	ctxA, cA := context.WithCancel(context.Background())
	ctxB, cB := context.WithCancel(context.Background())
	s.add("a", cA)
	s.add("b", cB)

	s.cancel("a")

	if ctxA.Err() == nil {
		t.Error("cancel(a) must cancel session a")
	}
	if ctxB.Err() != nil {
		t.Error("cancel(a) must not cancel session b")
	}
	if s.len() != 1 {
		t.Errorf("expected 1 session left, got %d", s.len())
	}

	// Completion bookkeeping: cancelling again (e.g. on goroutine exit) is a
	// no-op and does not grow or corrupt the map.
	s.cancel("a")
	s.cancel("unknown")
	if s.len() != 1 {
		t.Errorf("repeat/unknown cancel changed the map, len=%d", s.len())
	}
}

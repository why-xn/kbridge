package central

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewCommandQueue(t *testing.T) {
	q := NewCommandQueue()
	if q == nil {
		t.Fatal("expected non-nil queue")
	}
	if q.commands == nil {
		t.Fatal("expected commands map to be initialized")
	}
}

func TestCommandQueue_Enqueue(t *testing.T) {
	q := NewCommandQueue()

	requestID, err := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "default", 30, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(requestID, "req-") {
		t.Errorf("expected request ID to start with 'req-', got %q", requestID)
	}

	cmd, exists := q.Get(requestID)
	if !exists {
		t.Fatal("expected command to exist in queue")
	}

	if cmd.AgentID != "agent-1" {
		t.Errorf("expected agent ID 'agent-1', got %q", cmd.AgentID)
	}

	if cmd.ClusterName != "cluster-1" {
		t.Errorf("expected cluster name 'cluster-1', got %q", cmd.ClusterName)
	}

	if len(cmd.Command) != 2 || cmd.Command[0] != "get" || cmd.Command[1] != "pods" {
		t.Errorf("expected command [get, pods], got %v", cmd.Command)
	}

	if cmd.Status != CommandStatusPending {
		t.Errorf("expected status %q, got %q", CommandStatusPending, cmd.Status)
	}
}

func TestCommandQueue_GetPendingForAgent(t *testing.T) {
	q := NewCommandQueue()

	// Enqueue commands for different agents
	q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)
	q.Enqueue("agent-1", "cluster-1", []string{"get", "services"}, "", 30, nil)
	q.Enqueue("agent-2", "cluster-2", []string{"get", "nodes"}, "", 30, nil)

	// Get pending for agent-1
	pending := q.GetPendingForAgent("agent-1")
	if len(pending) != 2 {
		t.Errorf("expected 2 pending commands for agent-1, got %d", len(pending))
	}

	// Get pending for agent-2
	pending = q.GetPendingForAgent("agent-2")
	if len(pending) != 1 {
		t.Errorf("expected 1 pending command for agent-2, got %d", len(pending))
	}

	// Get pending for unknown agent
	pending = q.GetPendingForAgent("unknown")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending commands for unknown agent, got %d", len(pending))
	}
}

func TestCommandQueue_MarkRunning(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)

	// Mark as running
	ok := q.MarkRunning(requestID)
	if !ok {
		t.Error("expected MarkRunning to return true")
	}

	cmd, _ := q.Get(requestID)
	if cmd.Status != CommandStatusRunning {
		t.Errorf("expected status %q, got %q", CommandStatusRunning, cmd.Status)
	}

	// Should no longer appear in pending
	pending := q.GetPendingForAgent("agent-1")
	if len(pending) != 0 {
		t.Errorf("expected 0 pending commands after marking running, got %d", len(pending))
	}

	// Mark unknown command
	ok = q.MarkRunning("unknown-id")
	if ok {
		t.Error("expected MarkRunning to return false for unknown command")
	}
}

func TestCommandQueue_Complete(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)
	q.MarkRunning(requestID)

	result := &CommandResult{
		RequestID: requestID,
		Stdout:    []byte("pod-1\npod-2"),
		ExitCode:  0,
	}

	ok := q.Complete(requestID, result)
	if !ok {
		t.Error("expected Complete to return true")
	}

	cmd, _ := q.Get(requestID)
	if cmd.Status != CommandStatusCompleted {
		t.Errorf("expected status %q, got %q", CommandStatusCompleted, cmd.Status)
	}

	// Complete unknown command
	ok = q.Complete("unknown-id", result)
	if ok {
		t.Error("expected Complete to return false for unknown command")
	}
}

func TestCommandQueue_Fail(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)
	q.MarkRunning(requestID)

	ok := q.Fail(requestID, "command timed out")
	if !ok {
		t.Error("expected Fail to return true")
	}

	cmd, _ := q.Get(requestID)
	if cmd.Status != CommandStatusFailed {
		t.Errorf("expected status %q, got %q", CommandStatusFailed, cmd.Status)
	}

	// Fail unknown command
	ok = q.Fail("unknown-id", "error")
	if ok {
		t.Error("expected Fail to return false for unknown command")
	}
}

func TestCommandQueue_WaitForResult_Success(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)

	// Complete in a goroutine
	go func() {
		time.Sleep(10 * time.Millisecond)
		q.Complete(requestID, &CommandResult{
			RequestID: requestID,
			Stdout:    []byte("output"),
			ExitCode:  0,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := q.WaitForResult(ctx, requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Stdout) != "output" {
		t.Errorf("expected stdout 'output', got %q", string(result.Stdout))
	}

	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestCommandQueue_WaitForResult_Timeout(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := q.WaitForResult(ctx, requestID)
	if err == nil {
		t.Fatal("expected error for timeout")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}

	// Verify status is marked as timeout
	cmd, _ := q.Get(requestID)
	if cmd.Status != CommandStatusTimeout {
		t.Errorf("expected status %q, got %q", CommandStatusTimeout, cmd.Status)
	}
}

func TestCommandQueue_WaitForResult_NotFound(t *testing.T) {
	q := NewCommandQueue()

	ctx := context.Background()
	_, err := q.WaitForResult(ctx, "unknown-id")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}

	if !strings.Contains(err.Error(), "command not found") {
		t.Errorf("expected 'command not found' error, got %v", err)
	}
}

func TestCommandQueue_Remove(t *testing.T) {
	q := NewCommandQueue()

	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)

	q.Remove(requestID)

	_, exists := q.Get(requestID)
	if exists {
		t.Error("expected command to be removed")
	}
}

func TestCommandQueue_CleanupOld(t *testing.T) {
	q := NewCommandQueue()

	// Enqueue a command
	requestID, _ := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)

	// Manually set created time to be old
	q.mu.Lock()
	q.commands[requestID].CreatedAt = time.Now().Add(-2 * time.Hour)
	q.mu.Unlock()

	// Cleanup with 1 hour max age
	removed := q.CleanupOld(1 * time.Hour)
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	_, exists := q.Get(requestID)
	if exists {
		t.Error("expected old command to be removed")
	}
}

func TestCommandQueue_Concurrent(t *testing.T) {
	q := NewCommandQueue()
	var wg sync.WaitGroup

	// Concurrent enqueue
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := q.Enqueue("agent-1", "cluster-1", []string{"get", "pods"}, "", 30, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()

	pending := q.GetPendingForAgent("agent-1")
	if len(pending) != 100 {
		t.Errorf("expected 100 pending commands, got %d", len(pending))
	}
}

func TestGenerateRequestID(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := generateRequestID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.HasPrefix(id, "req-") {
			t.Errorf("expected ID to start with 'req-', got %q", id)
		}

		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

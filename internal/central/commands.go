package central

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// CommandStatus represents the state of a command execution.
type CommandStatus string

const (
	CommandStatusPending   CommandStatus = "pending"
	CommandStatusRunning   CommandStatus = "running"
	CommandStatusCompleted CommandStatus = "completed"
	CommandStatusFailed    CommandStatus = "failed"
	CommandStatusTimeout   CommandStatus = "timeout"
)

// PendingCommand represents a command waiting to be executed by an agent.
type PendingCommand struct {
	RequestID      string
	AgentID        string
	ClusterName    string
	Command        []string
	Namespace      string
	TimeoutSeconds int32
	CreatedAt      time.Time
	Status         CommandStatus

	// Results channel - buffered to avoid blocking
	resultCh chan *CommandResult
}

// CommandResult holds the accumulated result of a command execution.
type CommandResult struct {
	RequestID    string
	Stdout       []byte
	Stderr       []byte
	ExitCode     int32
	ErrorMessage string
	Completed    bool
}

// CommandQueue manages pending commands for agents.
type CommandQueue struct {
	mu       sync.RWMutex
	commands map[string]*PendingCommand // keyed by request ID
}

// NewCommandQueue creates a new command queue.
func NewCommandQueue() *CommandQueue {
	return &CommandQueue{
		commands: make(map[string]*PendingCommand),
	}
}

// Enqueue adds a new command to the queue and returns its request ID.
func (q *CommandQueue) Enqueue(agentID, clusterName string, command []string, namespace string, timeoutSeconds int32) (string, error) {
	requestID, err := generateRequestID()
	if err != nil {
		return "", fmt.Errorf("generating request ID: %w", err)
	}

	cmd := &PendingCommand{
		RequestID:      requestID,
		AgentID:        agentID,
		ClusterName:    clusterName,
		Command:        command,
		Namespace:      namespace,
		TimeoutSeconds: timeoutSeconds,
		CreatedAt:      time.Now(),
		Status:         CommandStatusPending,
		resultCh:       make(chan *CommandResult, 1),
	}

	q.mu.Lock()
	q.commands[requestID] = cmd
	q.mu.Unlock()

	return requestID, nil
}

// Get retrieves a pending command by request ID.
func (q *CommandQueue) Get(requestID string) (*PendingCommand, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	cmd, exists := q.commands[requestID]
	return cmd, exists
}

// GetPendingForAgent returns pending commands for a specific agent.
func (q *CommandQueue) GetPendingForAgent(agentID string) []*PendingCommand {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var pending []*PendingCommand
	for _, cmd := range q.commands {
		if cmd.AgentID == agentID && cmd.Status == CommandStatusPending {
			pending = append(pending, cmd)
		}
	}
	return pending
}

// MarkRunning marks a command as currently running.
func (q *CommandQueue) MarkRunning(requestID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	cmd, exists := q.commands[requestID]
	if !exists {
		return false
	}
	cmd.Status = CommandStatusRunning
	return true
}

// Complete marks a command as completed and stores the result.
func (q *CommandQueue) Complete(requestID string, result *CommandResult) bool {
	q.mu.Lock()
	cmd, exists := q.commands[requestID]
	if !exists {
		q.mu.Unlock()
		return false
	}

	cmd.Status = CommandStatusCompleted
	result.Completed = true
	q.mu.Unlock()

	// Send result to waiting channel (non-blocking)
	select {
	case cmd.resultCh <- result:
	default:
	}

	return true
}

// Fail marks a command as failed with an error message.
func (q *CommandQueue) Fail(requestID string, errorMessage string) bool {
	q.mu.Lock()
	cmd, exists := q.commands[requestID]
	if !exists {
		q.mu.Unlock()
		return false
	}

	cmd.Status = CommandStatusFailed
	q.mu.Unlock()

	result := &CommandResult{
		RequestID:    requestID,
		ErrorMessage: errorMessage,
		ExitCode:     -1,
		Completed:    true,
	}

	// Send result to waiting channel (non-blocking)
	select {
	case cmd.resultCh <- result:
	default:
	}

	return true
}

// WaitForResult waits for a command result with timeout.
func (q *CommandQueue) WaitForResult(ctx context.Context, requestID string) (*CommandResult, error) {
	q.mu.RLock()
	cmd, exists := q.commands[requestID]
	q.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("command not found: %s", requestID)
	}

	select {
	case result := <-cmd.resultCh:
		return result, nil
	case <-ctx.Done():
		// Mark as timeout
		q.mu.Lock()
		if cmd.Status == CommandStatusPending || cmd.Status == CommandStatusRunning {
			cmd.Status = CommandStatusTimeout
		}
		q.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Remove removes a command from the queue.
func (q *CommandQueue) Remove(requestID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.commands, requestID)
}

// CleanupOld removes commands older than the specified duration.
func (q *CommandQueue) CleanupOld(maxAge time.Duration) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	for id, cmd := range q.commands {
		if cmd.CreatedAt.Before(cutoff) {
			delete(q.commands, id)
			removed++
		}
	}

	return removed
}

// generateRequestID creates a unique request identifier.
func generateRequestID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "req-" + hex.EncodeToString(bytes), nil
}

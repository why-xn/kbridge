package central

import (
	"context"
	"strings"
	"testing"

	"github.com/why-xn/mk8s/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func newTestGRPCServer() (*GRPCServer, *AgentStore, *CommandQueue) {
	store := NewAgentStore()
	store.AddValidToken("valid-token")
	cmdQueue := NewCommandQueue()
	return NewGRPCServer(store, cmdQueue), store, cmdQueue
}

func TestGRPCServer_Register_Success(t *testing.T) {
	srv, store, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "test-cluster",
		Metadata: &agentpb.ClusterMetadata{
			KubernetesVersion: "1.28.0",
			NodeCount:         3,
			Region:            "us-east-1",
			Provider:          "aws",
		},
	}

	resp, err := srv.Register(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success=true, got error: %s", resp.ErrorMessage)
	}

	if !strings.HasPrefix(resp.AgentId, "agent-") {
		t.Errorf("expected agent_id to start with 'agent-', got %q", resp.AgentId)
	}

	// Verify agent is stored
	agent, exists := store.Get(resp.AgentId)
	if !exists {
		t.Fatal("agent should be stored after registration")
	}

	if agent.ClusterName != "test-cluster" {
		t.Errorf("expected cluster name 'test-cluster', got %q", agent.ClusterName)
	}

	if agent.KubernetesVersion != "1.28.0" {
		t.Errorf("expected kubernetes version '1.28.0', got %q", agent.KubernetesVersion)
	}
}

func TestGRPCServer_Register_InvalidToken(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.RegisterRequest{
		AgentToken:  "invalid-token",
		ClusterName: "test-cluster",
	}

	resp, err := srv.Register(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false for invalid token")
	}

	if resp.ErrorMessage != "invalid agent token" {
		t.Errorf("expected error message 'invalid agent token', got %q", resp.ErrorMessage)
	}
}

func TestGRPCServer_Register_MissingClusterName(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "",
	}

	resp, err := srv.Register(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Success {
		t.Error("expected success=false for missing cluster name")
	}

	if resp.ErrorMessage != "cluster_name is required" {
		t.Errorf("expected error message 'cluster_name is required', got %q", resp.ErrorMessage)
	}
}

func TestGRPCServer_Register_MultipleAgents(t *testing.T) {
	srv, store, _ := newTestGRPCServer()
	ctx := context.Background()

	clusters := []string{"cluster-a", "cluster-b", "cluster-c"}
	agentIDs := make([]string, len(clusters))

	for i, cluster := range clusters {
		req := &agentpb.RegisterRequest{
			AgentToken:  "valid-token",
			ClusterName: cluster,
		}

		resp, err := srv.Register(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !resp.Success {
			t.Errorf("expected success for cluster %s", cluster)
		}

		agentIDs[i] = resp.AgentId
	}

	// Verify all agents are stored
	agents := store.List()
	if len(agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(agents))
	}

	// Verify all agent IDs are unique
	seen := make(map[string]bool)
	for _, id := range agentIDs {
		if seen[id] {
			t.Errorf("duplicate agent ID: %s", id)
		}
		seen[id] = true
	}
}

func TestGRPCServer_Heartbeat_Success(t *testing.T) {
	srv, store, _ := newTestGRPCServer()
	ctx := context.Background()

	// First register an agent
	regReq := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "test-cluster",
	}

	regResp, _ := srv.Register(ctx, regReq)

	// Send heartbeat
	req := &agentpb.HeartbeatRequest{
		AgentId: regResp.AgentId,
		Status:  agentpb.AgentStatus_AGENT_STATUS_HEALTHY,
	}

	resp, err := srv.Heartbeat(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Acknowledged {
		t.Error("expected acknowledged=true")
	}

	if resp.NextHeartbeatSeconds != DefaultHeartbeatInterval {
		t.Errorf("expected next_heartbeat_seconds=%d, got %d",
			DefaultHeartbeatInterval, resp.NextHeartbeatSeconds)
	}

	// Verify agent status is updated
	agent, _ := store.Get(regResp.AgentId)
	if agent.Status != AgentStatusConnected {
		t.Errorf("expected status %q, got %q", AgentStatusConnected, agent.Status)
	}
}

func TestGRPCServer_Heartbeat_UnregisteredAgent(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.HeartbeatRequest{
		AgentId: "unknown-agent",
		Status:  agentpb.AgentStatus_AGENT_STATUS_HEALTHY,
	}

	_, err := srv.Heartbeat(ctx, req)
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected code %v, got %v", codes.NotFound, st.Code())
	}
}

func TestGRPCServer_Heartbeat_MissingAgentID(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.HeartbeatRequest{
		AgentId: "",
		Status:  agentpb.AgentStatus_AGENT_STATUS_HEALTHY,
	}

	_, err := srv.Heartbeat(ctx, req)
	if err == nil {
		t.Fatal("expected error for missing agent ID")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
	}
}

func TestGRPCServer_ExecuteCommand(t *testing.T) {
	srv, _, _ := newTestGRPCServer()

	req := &agentpb.CommandRequest{
		RequestId: "req-123",
		AgentId:   "agent-test-cluster",
		Command:   []string{"get", "pods"},
	}

	// ExecuteCommand should return Unimplemented error
	err := srv.ExecuteCommand(req, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.Unimplemented {
		t.Errorf("expected code %v, got %v", codes.Unimplemented, st.Code())
	}
}

func TestConvertAgentStatus(t *testing.T) {
	tests := []struct {
		input agentpb.AgentStatus
		want  string
	}{
		{agentpb.AgentStatus_AGENT_STATUS_HEALTHY, AgentStatusConnected},
		{agentpb.AgentStatus_AGENT_STATUS_DEGRADED, AgentStatusConnected},
		{agentpb.AgentStatus_AGENT_STATUS_UNKNOWN, AgentStatusConnected},
	}

	for _, tt := range tests {
		t.Run(tt.input.String(), func(t *testing.T) {
			got := convertAgentStatus(tt.input)
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestGenerateAgentID(t *testing.T) {
	// Generate multiple IDs and check they are unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := generateAgentID()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.HasPrefix(id, "agent-") {
			t.Errorf("expected ID to start with 'agent-', got %q", id)
		}

		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestGRPCServer_GetPendingCommands_Success(t *testing.T) {
	srv, store, cmdQueue := newTestGRPCServer()
	ctx := context.Background()

	// Register an agent
	regReq := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "test-cluster",
	}
	regResp, _ := srv.Register(ctx, regReq)
	agentID := regResp.AgentId

	// Queue some commands
	cmdQueue.Enqueue(agentID, "test-cluster", []string{"get", "pods"}, "default", 30)
	cmdQueue.Enqueue(agentID, "test-cluster", []string{"get", "services"}, "", 30)

	// Get pending commands
	req := &agentpb.GetPendingCommandsRequest{
		AgentId: agentID,
	}

	resp, err := srv.GetPendingCommands(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(resp.Commands))
	}

	// Verify commands are marked as running
	pending := cmdQueue.GetPendingForAgent(agentID)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending commands after retrieval, got %d", len(pending))
	}

	// Verify agent is registered (used to silence unused variable warning)
	_, exists := store.Get(agentID)
	if !exists {
		t.Error("expected agent to be registered")
	}
}

func TestGRPCServer_GetPendingCommands_MissingAgentID(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.GetPendingCommandsRequest{
		AgentId: "",
	}

	_, err := srv.GetPendingCommands(ctx, req)
	if err == nil {
		t.Fatal("expected error for missing agent ID")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
	}
}

func TestGRPCServer_GetPendingCommands_UnregisteredAgent(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.GetPendingCommandsRequest{
		AgentId: "unknown-agent",
	}

	_, err := srv.GetPendingCommands(ctx, req)
	if err == nil {
		t.Fatal("expected error for unregistered agent")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("expected code %v, got %v", codes.NotFound, st.Code())
	}
}

func TestGRPCServer_SubmitCommandResult_Success(t *testing.T) {
	srv, _, cmdQueue := newTestGRPCServer()
	ctx := context.Background()

	// Queue a command
	requestID, _ := cmdQueue.Enqueue("agent-1", "test-cluster", []string{"get", "pods"}, "", 30)
	cmdQueue.MarkRunning(requestID)

	// Submit result
	req := &agentpb.SubmitCommandResultRequest{
		RequestId: requestID,
		Stdout:    []byte("pod-1\npod-2"),
		Stderr:    nil,
		ExitCode:  0,
	}

	resp, err := srv.SubmitCommandResult(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}

	// Verify command is marked as completed
	cmd, _ := cmdQueue.Get(requestID)
	if cmd.Status != CommandStatusCompleted {
		t.Errorf("expected status %q, got %q", CommandStatusCompleted, cmd.Status)
	}
}

func TestGRPCServer_SubmitCommandResult_WithError(t *testing.T) {
	srv, _, cmdQueue := newTestGRPCServer()
	ctx := context.Background()

	// Queue a command
	requestID, _ := cmdQueue.Enqueue("agent-1", "test-cluster", []string{"get", "pods"}, "", 30)
	cmdQueue.MarkRunning(requestID)

	// Submit result with error
	req := &agentpb.SubmitCommandResultRequest{
		RequestId:    requestID,
		ErrorMessage: "kubectl not found",
	}

	resp, err := srv.SubmitCommandResult(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}

	// Verify command is marked as failed
	cmd, _ := cmdQueue.Get(requestID)
	if cmd.Status != CommandStatusFailed {
		t.Errorf("expected status %q, got %q", CommandStatusFailed, cmd.Status)
	}
}

func TestGRPCServer_SubmitCommandResult_MissingRequestID(t *testing.T) {
	srv, _, _ := newTestGRPCServer()
	ctx := context.Background()

	req := &agentpb.SubmitCommandResultRequest{
		RequestId: "",
	}

	_, err := srv.SubmitCommandResult(ctx, req)
	if err == nil {
		t.Fatal("expected error for missing request ID")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected code %v, got %v", codes.InvalidArgument, st.Code())
	}
}

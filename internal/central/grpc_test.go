package central

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	testAgentToken  = "valid-token"
	testClusterName = "test-cluster"
)

func newTestGRPCServer(t *testing.T) (*GRPCServer, *AgentStore, *CommandQueue) {
	t.Helper()
	agents := NewAgentStore()
	cmdQueue := NewCommandQueue()
	db := newTestStore(t)
	seedClusterToken(t, db, testClusterName, testAgentToken, nil)
	authn := NewAgentAuthenticator(db, testPepper)
	return NewGRPCServer(agents, cmdQueue, authn, NewSessionManager(10)), agents, cmdQueue
}

func TestGRPCServer_Register_Success(t *testing.T) {
	srv, store, _ := newTestGRPCServer(t)
	ctx := context.Background()

	req := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "test-cluster",
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
}

func TestGRPCServer_Register_PersistsClusterConnected(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)
	cluster := seedClusterToken(t, db, "edge", "edge-token", nil)
	srv := NewGRPCServer(NewAgentStore(), NewCommandQueue(), NewAgentAuthenticator(db, testPepper), NewSessionManager(10))

	resp, err := srv.Register(ctx, &agentpb.RegisterRequest{
		AgentToken:  "edge-token",
		ClusterName: "edge",
	})
	if err != nil || !resp.Success {
		t.Fatalf("register failed: err=%v resp=%+v", err, resp)
	}

	got, err := db.GetClusterByID(ctx, cluster.ID)
	if err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if got.Status != AgentStatusConnected {
		t.Errorf("want status connected, got %q", got.Status)
	}
	if got.AgentID != resp.AgentId {
		t.Errorf("want agent id %q, got %q", resp.AgentId, got.AgentID)
	}
	if got.LastSeenAt == nil {
		t.Errorf("expected last_seen persisted, got %+v", got)
	}
}

func TestGRPCServer_Register_RevokedToken(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)
	seedClusterToken(t, db, "edge", "edge-token", func(at *AgentToken) { at.IsRevoked = true })
	srv := NewGRPCServer(NewAgentStore(), NewCommandQueue(), NewAgentAuthenticator(db, testPepper), NewSessionManager(10))

	resp, err := srv.Register(ctx, &agentpb.RegisterRequest{
		AgentToken:  "edge-token",
		ClusterName: "edge",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Fatal("expected revoked token to be rejected")
	}
	if resp.ErrorMessage != "agent token revoked" {
		t.Errorf("want 'agent token revoked', got %q", resp.ErrorMessage)
	}
}

func TestGRPCServer_Register_InvalidToken(t *testing.T) {
	srv, _, _ := newTestGRPCServer(t)
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
	srv, _, _ := newTestGRPCServer(t)
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
	ctx := context.Background()
	db := newTestStore(t)
	store := NewAgentStore()
	srv := NewGRPCServer(store, NewCommandQueue(), NewAgentAuthenticator(db, testPepper), NewSessionManager(10))

	// Each cluster has its own token: a token authorizes exactly one cluster.
	clusters := []string{"cluster-a", "cluster-b", "cluster-c"}
	agentIDs := make([]string, len(clusters))

	for i, cluster := range clusters {
		token := "token-" + cluster
		seedClusterToken(t, db, cluster, token, nil)

		req := &agentpb.RegisterRequest{
			AgentToken:  token,
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
	srv, store, _ := newTestGRPCServer(t)
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
	srv, _, _ := newTestGRPCServer(t)
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
	srv, _, _ := newTestGRPCServer(t)
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
	srv, store, cmdQueue := newTestGRPCServer(t)
	ctx := context.Background()

	// Register an agent
	regReq := &agentpb.RegisterRequest{
		AgentToken:  "valid-token",
		ClusterName: "test-cluster",
	}
	regResp, _ := srv.Register(ctx, regReq)
	agentID := regResp.AgentId

	// Queue some commands
	cmdQueue.Enqueue(agentID, "test-cluster", []string{"get", "pods"}, "default", 30, nil)
	cmdQueue.Enqueue(agentID, "test-cluster", []string{"get", "services"}, "", 30, nil)

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
	srv, _, _ := newTestGRPCServer(t)
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
	srv, _, _ := newTestGRPCServer(t)
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
	srv, _, cmdQueue := newTestGRPCServer(t)
	ctx := context.Background()

	// Queue a command
	requestID, _ := cmdQueue.Enqueue("agent-1", "test-cluster", []string{"get", "pods"}, "", 30, nil)
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
	srv, _, cmdQueue := newTestGRPCServer(t)
	ctx := context.Background()

	// Queue a command
	requestID, _ := cmdQueue.Enqueue("agent-1", "test-cluster", []string{"get", "pods"}, "", 30, nil)
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
	srv, _, _ := newTestGRPCServer(t)
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

func TestGRPCServer_OpenStream_RegistersAndRoutes(t *testing.T) {
	ctx := context.Background()
	db := newTestStore(t)
	seedClusterToken(t, db, "edge", "edge-stream-token", nil)
	sm := NewSessionManager(10)
	srv := NewGRPCServer(NewAgentStore(), NewCommandQueue(), NewAgentAuthenticator(db, testPepper), sm)

	// Register the agent in the in-memory store so OpenStream accepts it.
	resp, _ := srv.Register(ctx, &agentpb.RegisterRequest{AgentToken: "edge-stream-token", ClusterName: "edge"})

	fs := newFakeOpenStream(resp.AgentId)
	go func() { _ = srv.OpenStream(fs) }()
	fs.waitRegistered(t)

	// Once registered, the manager can start a session for this agent.
	if _, err := sm.Start(resp.AgentId, []string{"logs", "-f"}, ""); err != nil {
		t.Fatalf("start after OpenStream: %v", err)
	}
}

type fakeOpenStream struct {
	agentpb.AgentService_OpenStreamServer
	agentID  string
	incoming chan *agentpb.AgentStreamMessage
	sent     chan *agentpb.CentralStreamMessage
	ctx      context.Context
}

func newFakeOpenStream(agentID string) *fakeOpenStream {
	f := &fakeOpenStream{
		agentID:  agentID,
		incoming: make(chan *agentpb.AgentStreamMessage, 4),
		sent:     make(chan *agentpb.CentralStreamMessage, 4),
		ctx:      context.Background(),
	}
	f.incoming <- &agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Register{
		Register: &agentpb.StreamRegister{AgentId: agentID},
	}}
	return f
}

func (f *fakeOpenStream) Context() context.Context { return f.ctx }
func (f *fakeOpenStream) Send(m *agentpb.CentralStreamMessage) error {
	f.sent <- m
	return nil
}
func (f *fakeOpenStream) Recv() (*agentpb.AgentStreamMessage, error) {
	msg, ok := <-f.incoming
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}
func (f *fakeOpenStream) waitRegistered(t *testing.T) {
	t.Helper()
	// Give the handler a moment to process the Register message.
	time.Sleep(100 * time.Millisecond)
}

package agent

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/why-xn/mk8s/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockAgentService implements the AgentService for testing.
type mockAgentService struct {
	agentpb.UnimplementedAgentServiceServer
	registerCalled   int
	heartbeatCalled  int
	rejectRegister   bool
	rejectHeartbeat  bool
	lastClusterName  string
	lastAgentToken   string
}

func (m *mockAgentService) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	m.registerCalled++
	m.lastClusterName = req.GetClusterName()
	m.lastAgentToken = req.GetAgentToken()

	if m.rejectRegister {
		return &agentpb.RegisterResponse{
			Success:      false,
			ErrorMessage: "rejected",
		}, nil
	}

	return &agentpb.RegisterResponse{
		Success: true,
		AgentId: "test-agent-id",
	}, nil
}

func (m *mockAgentService) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	m.heartbeatCalled++

	if m.rejectHeartbeat {
		return nil, status.Error(codes.NotFound, "agent not found")
	}

	return &agentpb.HeartbeatResponse{
		Acknowledged:         true,
		NextHeartbeatSeconds: 1, // Short interval for testing
	}, nil
}

func startMockServer(t *testing.T, svc *mockAgentService) (string, func()) {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer()
	agentpb.RegisterAgentServiceServer(srv, svc)

	go func() {
		if err := srv.Serve(lis); err != nil {
			// Server stopped
		}
	}()

	return lis.Addr().String(), func() {
		srv.GracefulStop()
	}
}

func TestNew(t *testing.T) {
	cfg := &Config{
		Central: CentralConfig{URL: "localhost:9090", Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	if a == nil {
		t.Fatal("expected non-nil agent")
	}

	if a.config != cfg {
		t.Error("expected config to be set")
	}

	if a.stopCh == nil {
		t.Error("expected stopCh to be set")
	}
}

func TestAgent_AgentID(t *testing.T) {
	cfg := DefaultConfig()
	a := New(cfg)

	// Initially empty
	if a.AgentID() != "" {
		t.Errorf("expected empty agent ID, got %q", a.AgentID())
	}
}

func TestAgent_Register(t *testing.T) {
	mock := &mockAgentService{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "test-token"},
		Cluster: ClusterConfig{
			Name:              "test-cluster",
			KubernetesVersion: "1.28.0",
			NodeCount:         3,
			Region:            "us-east-1",
			Provider:          "aws",
		},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect
	if err := a.connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer a.disconnect()

	// Register
	if err := a.register(ctx); err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	// Verify
	if mock.registerCalled != 1 {
		t.Errorf("expected 1 register call, got %d", mock.registerCalled)
	}

	if mock.lastClusterName != "test-cluster" {
		t.Errorf("expected cluster name 'test-cluster', got %q", mock.lastClusterName)
	}

	if mock.lastAgentToken != "test-token" {
		t.Errorf("expected token 'test-token', got %q", mock.lastAgentToken)
	}

	if a.AgentID() != "test-agent-id" {
		t.Errorf("expected agent ID 'test-agent-id', got %q", a.AgentID())
	}
}

func TestAgent_Register_Rejected(t *testing.T) {
	mock := &mockAgentService{rejectRegister: true}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "bad-token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer a.disconnect()

	err := a.register(ctx)
	if err == nil {
		t.Error("expected error for rejected registration")
	}
}

func TestAgent_Heartbeat(t *testing.T) {
	mock := &mockAgentService{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer a.disconnect()

	if err := a.register(ctx); err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	// Send heartbeat
	interval, err := a.sendHeartbeat(ctx)
	if err != nil {
		t.Fatalf("failed to send heartbeat: %v", err)
	}

	if mock.heartbeatCalled != 1 {
		t.Errorf("expected 1 heartbeat call, got %d", mock.heartbeatCalled)
	}

	if interval != 1*time.Second {
		t.Errorf("expected 1s interval, got %v", interval)
	}
}

func TestAgent_ConnectTimeout(t *testing.T) {
	cfg := &Config{
		Central: CentralConfig{URL: "localhost:59999", Token: "token"}, // Non-existent server
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := a.connect(ctx)
	if err == nil {
		t.Error("expected error for connection to non-existent server")
		a.disconnect()
	}
}

func TestAgent_Run_StopSignal(t *testing.T) {
	mock := &mockAgentService{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx := context.Background()

	// Run in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Run(ctx)
	}()

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	// Stop the agent
	a.Stop()

	// Should complete without error
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for agent to stop")
	}
}

func TestAgent_Run_ContextCancel(t *testing.T) {
	mock := &mockAgentService{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	// Run in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Run(ctx)
	}()

	// Wait for registration
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Should complete - need to call Stop() to prevent hang
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(a.stopCh)
	}()

	select {
	case <-errCh:
		// Completed
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for agent to stop")
	}
}

func TestAgent_Disconnect_NoConnection(t *testing.T) {
	cfg := DefaultConfig()
	a := New(cfg)

	// Should not panic when disconnecting without a connection
	a.disconnect()
}

func TestAgent_HeartbeatNotAcknowledged(t *testing.T) {
	mock := &mockAgentService{}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	// Override to return not acknowledged
	mock.rejectHeartbeat = true

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.connect(ctx); err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer a.disconnect()

	if err := a.register(ctx); err != nil {
		t.Fatalf("failed to register: %v", err)
	}

	// Heartbeat should fail
	_, err := a.sendHeartbeat(ctx)
	if err == nil {
		t.Error("expected error for rejected heartbeat")
	}
}

func TestAgent_Run_ConnectionError(t *testing.T) {
	cfg := &Config{
		Central: CentralConfig{URL: "localhost:59999", Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := a.Run(ctx)
	if err == nil {
		t.Error("expected error for connection failure")
	}
}

func TestAgent_Run_RegistrationError(t *testing.T) {
	mock := &mockAgentService{rejectRegister: true}
	addr, cleanup := startMockServer(t, mock)
	defer cleanup()

	cfg := &Config{
		Central: CentralConfig{URL: addr, Token: "bad-token"},
		Cluster: ClusterConfig{Name: "test"},
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := a.Run(ctx)
	if err == nil {
		t.Error("expected error for registration failure")
	}
}

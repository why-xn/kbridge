package agent

import (
	"context"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
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
	// rejectAfter / unavailableAfter, when set, refuse every registration
	// after that many with a rejection / a transport error.
	rejectAfter      int
	unavailableAfter int
	lastClusterName  string
	lastAgentToken   string
	lastCapabilities []string
}

func (m *mockAgentService) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	m.registerCalled++
	m.lastClusterName = req.GetClusterName()
	m.lastAgentToken = req.GetAgentToken()
	m.lastCapabilities = req.GetCapabilities()

	if m.unavailableAfter > 0 && m.registerCalled > m.unavailableAfter {
		return nil, status.Error(codes.Unavailable, "control plane unavailable")
	}
	if m.rejectRegister || (m.rejectAfter > 0 && m.registerCalled > m.rejectAfter) {
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

// kubeFixture points the agent's window at a TLS test upstream.
func kubeFixture(t *testing.T) KubernetesConfig {
	t.Helper()
	srv := newEchoUpstream(t)
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, caPEM, 0o644); err != nil {
		t.Fatalf("writing ca: %v", err)
	}
	token := filepath.Join(dir, "token")
	writeToken(t, token, "agent-token")
	return KubernetesConfig{Server: srv.URL, CAFile: ca, TokenFile: token}
}

func TestNew(t *testing.T) {
	cfg := &Config{
		ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: "token"},
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "test-token"},
		Cluster: ClusterConfig{Name: "test-cluster"},
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

	if !slices.Equal(mock.lastCapabilities, []string{"impersonation", "rbac-sync"}) {
		t.Errorf("capabilities = %v, want [impersonation rbac-sync]", mock.lastCapabilities)
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "bad-token"},
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
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
		ControlPlane: ControlPlaneConfig{URL: "localhost:59999", Token: "token"}, // Non-existent server
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
		Kubernetes: kubeFixture(t),
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
		Kubernetes: kubeFixture(t),
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
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
		ControlPlane: ControlPlaneConfig{URL: "localhost:59999", Token: "token"},
		Cluster: ClusterConfig{Name: "test"},
		Kubernetes: kubeFixture(t),
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
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "bad-token"},
		Cluster: ClusterConfig{Name: "test"},
		Kubernetes: kubeFixture(t),
	}

	a := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := a.Run(ctx)
	if err == nil {
		t.Error("expected error for registration failure")
	}
}

func TestTouchHealthFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "healthy")
	a := &Agent{config: &Config{}}
	a.config.HealthFile = path
	a.touchHealthFile()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("health file not written: %v", err)
	}
}

// Without a usable cluster connection the agent must not start, rather than
// let kubectl fall back to ambient credentials.
func TestAgent_Run_WindowError(t *testing.T) {
	a := New(&Config{
		ControlPlane: ControlPlaneConfig{URL: "localhost:59999", Token: "token"},
		Cluster:      ClusterConfig{Name: "test"},
		Kubernetes:   KubernetesConfig{Server: "https://127.0.0.1:1", CAFile: filepath.Join(t.TempDir(), "missing")},
	})
	err := a.Run(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "starting window: ") {
		t.Fatalf("err = %v, want a starting window error", err)
	}
}

func TestWindowProtected(t *testing.T) {
	saDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(saDir, "namespace"), []byte("kbridge\n"), 0o644); err != nil {
		t.Fatalf("writing namespace: %v", err)
	}
	tests := []struct {
		name      string
		protected []string
		tokenFile string
		want      []string
	}{
		{"configured plus own", []string{"ops", ""}, filepath.Join(saDir, "token"), []string{"ops", "kbridge"}},
		{"no namespace file", []string{"ops"}, filepath.Join(t.TempDir(), "token"), []string{"ops"}},
		{"nothing", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ProtectedNamespaces: tt.protected, Kubernetes: KubernetesConfig{TokenFile: tt.tokenFile}}
			if got := windowProtected(cfg); !slices.Equal(got, tt.want) {
				t.Errorf("windowProtected = %v, want %v", got, tt.want)
			}
		})
	}
}

// Shutdown removes the window CA dir and a token file written from a kubeconfig.
func TestAgent_CloseWindowCleansUp(t *testing.T) {
	a := New(&Config{Kubernetes: kubeFixture(t)})
	if err := a.openWindow(); err != nil {
		t.Fatalf("openWindow: %v", err)
	}
	tmp, err := os.CreateTemp("", "kbridge-token-*")
	if err != nil {
		t.Fatalf("creating temp token: %v", err)
	}
	tmp.Close()
	a.tempToken = tmp.Name()
	a.closeWindow()
	for _, p := range []string{a.windowDir, a.tempToken} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists after closeWindow", p)
		}
	}
}

func TestRun_TriggersSyncAfterRegister(t *testing.T) {
	addr, cleanup := startMockServer(t, &mockAgentService{})
	defer cleanup()
	a := New(&Config{ControlPlane: ControlPlaneConfig{URL: addr, Token: "t"}, Cluster: ClusterConfig{Name: "test"}})
	a.sync = newSyncer(nil, nil, 0, a.currentClient)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer a.disconnect()
	if err := a.register(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.sync.kick:
	default:
		t.Error("register did not trigger a sync")
	}
}

// A control plane that refuses re-registration (e.g. the token was revoked)
// will refuse every retry, so the agent exits; an unreachable one is retried.
func TestAgent_Run_ReRegistration(t *testing.T) {
	tests := []struct {
		name       string
		mock       *mockAgentService
		wantReturn bool
	}{
		{"rejection exits", &mockAgentService{rejectHeartbeat: true, rejectAfter: 1}, true},
		{"transport error keeps retrying", &mockAgentService{rejectHeartbeat: true, unavailableAfter: 1}, false},
	}
	defer func(d time.Duration) { initialHeartbeatInterval = d }(initialHeartbeatInterval)
	initialHeartbeatInterval = 50 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, cleanup := startMockServer(t, tt.mock)
			defer cleanup()
			a := New(&Config{
				ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   kubeFixture(t),
			})
			errCh := make(chan error, 1)
			go func() { errCh <- a.Run(context.Background()) }()
			select {
			case err := <-errCh:
				if !tt.wantReturn || !errors.Is(err, errRegistrationRejected) || !strings.Contains(err.Error(), "rejected") {
					t.Fatalf("Run returned %v, want it to exit only on a rejection", err)
				}
			case <-time.After(1500 * time.Millisecond):
				if tt.wantReturn {
					t.Fatal("Run kept going after registration was rejected")
				}
				a.Stop()
			}
		})
	}
}

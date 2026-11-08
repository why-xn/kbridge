package agent

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	// heartbeatCode / pollCode, when set, fail every heartbeat / poll with it.
	heartbeatCode    codes.Code
	pollCode         codes.Code
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
	if m.heartbeatCode != codes.OK {
		return nil, status.Error(m.heartbeatCode, "refused")
	}

	return &agentpb.HeartbeatResponse{
		Acknowledged:         true,
		NextHeartbeatSeconds: 1, // Short interval for testing
	}, nil
}

func (m *mockAgentService) GetPendingCommands(ctx context.Context, req *agentpb.GetPendingCommandsRequest) (*agentpb.GetPendingCommandsResponse, error) {
	if m.pollCode != codes.OK {
		return nil, status.Error(m.pollCode, "refused")
	}
	return &agentpb.GetPendingCommandsResponse{}, nil
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
// will refuse every retry, so the agent exits; so does an agent a newer one
// replaced. An unreachable control plane is retried, and an unknown agent
// (control plane restarted) re-registers.
func TestAgent_Run_ReRegistration(t *testing.T) {
	tests := []struct {
		name          string
		mock          *mockAgentService
		wantErr       error // nil: Run keeps going
		wantRegisters int
	}{
		{"rejection exits", &mockAgentService{rejectHeartbeat: true, rejectAfter: 1}, errRegistrationRejected, 2},
		{"transport error keeps retrying", &mockAgentService{rejectHeartbeat: true, unavailableAfter: 1}, nil, 2},
		{"not found re-registers", &mockAgentService{rejectHeartbeat: true}, nil, 2},
	}
	defer func(d time.Duration) { initialHeartbeatInterval = d }(initialHeartbeatInterval)
	initialHeartbeatInterval = 50 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, cleanup := startMockServer(t, tt.mock)
			a := New(&Config{
				ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   kubeFixture(t),
			})
			errCh := make(chan error, 1)
			go func() { errCh <- a.Run(context.Background()) }()
			select {
			case err := <-errCh:
				if tt.wantErr == nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("Run returned %v, want %v", err, tt.wantErr)
				}
			case <-time.After(1500 * time.Millisecond):
				if tt.wantErr != nil {
					t.Fatalf("Run kept going, want %v", tt.wantErr)
				}
				a.Stop()
				<-errCh
			}
			cleanup() // waits for handlers, so registerCalled is safe to read
			if tt.mock.registerCalled < tt.wantRegisters {
				t.Fatalf("registered %d times, want %d", tt.mock.registerCalled, tt.wantRegisters)
			}
		})
	}
}

// A replaced agent must neither exit (the kubelet would restart it and it
// would take the cluster back) nor re-register: it stands by, keeping its
// liveness file fresh, until it is stopped, or gives up after standByTimeout.
func TestAgent_Run_ReplacedStandsBy(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		stop    bool
		wantErr string
	}{
		{"stopped while standing by", time.Minute, true, ""},
		{"never stopped", 200 * time.Millisecond, false, "replaced by a newer agent and never stopped: exiting"},
	}
	defer func(h, i, d time.Duration) { initialHeartbeatInterval, standByTouchInterval, standByTimeout = h, i, d }(
		initialHeartbeatInterval, standByTouchInterval, standByTimeout)
	initialHeartbeatInterval, standByTouchInterval = 50*time.Millisecond, 100*time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			standByTimeout = tt.timeout
			mock := &mockAgentService{heartbeatCode: codes.FailedPrecondition}
			addr, cleanup := startMockServer(t, mock)
			health := filepath.Join(t.TempDir(), "healthy")
			a := New(&Config{
				ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   kubeFixture(t),
				HealthFile:   health,
			})
			errCh := make(chan error, 1)
			go func() { errCh <- a.Run(context.Background()) }()
			if tt.stop {
				assertStandingBy(t, errCh, health)
				a.Stop()
			}
			err := <-errCh
			if got := fmt.Sprint(err); (tt.wantErr == "" && err != nil) || (tt.wantErr != "" && got != tt.wantErr) {
				t.Fatalf("Run returned %v, want %q", err, tt.wantErr)
			}
			cleanup() // waits for handlers, so registerCalled is safe to read
			if mock.registerCalled != 1 {
				t.Fatalf("registered %d times, want 1 (no re-registration once replaced)", mock.registerCalled)
			}
		})
	}
}

// assertStandingBy checks that Run keeps going and keeps touching health.
func assertStandingBy(t *testing.T, errCh <-chan error, health string) {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	first, err := os.Stat(health)
	if err != nil {
		t.Fatalf("health file: %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Run returned %v while standing by", err)
	case <-time.After(800 * time.Millisecond):
	}
	second, _ := os.Stat(health)
	if !second.ModTime().After(first.ModTime()) {
		t.Fatalf("health file not touched while standing by: %v then %v", first.ModTime(), second.ModTime())
	}
}

// takeOverService answers heartbeats "replaced" until the test says the
// replacer is gone, then NotFound until the agent registers again.
type takeOverService struct {
	mockAgentService
	mu        sync.Mutex
	registers int
	gone      bool
}

func (s *takeOverService) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registers++
	return &agentpb.RegisterResponse{Success: true, AgentId: fmt.Sprintf("agent-%d", s.registers)}, nil
}

func (s *takeOverService) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.registers > 1:
		return &agentpb.HeartbeatResponse{Acknowledged: true, NextHeartbeatSeconds: 1}, nil
	case s.gone:
		return nil, status.Error(codes.NotFound, "agent not registered")
	default:
		return nil, status.Error(codes.FailedPrecondition, "agent replaced by a newer registration for this cluster")
	}
}

func (s *takeOverService) registered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registers
}

// A standing-by agent whose replacer goes away registers again within one
// stand-by tick and serves the cluster, instead of sitting out the timeout.
func TestAgent_StandingByTakesOverWhenReplacerGone(t *testing.T) {
	defer func(h, i time.Duration) { initialHeartbeatInterval, standByTouchInterval = h, i }(
		initialHeartbeatInterval, standByTouchInterval)
	initialHeartbeatInterval, standByTouchInterval = 50*time.Millisecond, 100*time.Millisecond
	svc := &takeOverService{}
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	agentpb.RegisterAgentServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()
	addr := lis.Addr().String()
	a := New(&Config{
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
		Cluster:      ClusterConfig{Name: "test"},
		Kubernetes:   kubeFixture(t),
	})
	errCh := make(chan error, 1)
	go func() { errCh <- a.Run(context.Background()) }()

	<-a.replacedC()
	time.Sleep(300 * time.Millisecond)
	if n := svc.registered(); n != 1 {
		t.Fatalf("registered %d times while the replacer is connected, want 1", n)
	}
	svc.mu.Lock()
	svc.gone = true
	svc.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for svc.registered() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("standing-by agent did not register again after its replacer went away")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Run returned %v after taking the cluster back", err)
	case <-time.After(500 * time.Millisecond):
	}
	if a.AgentID() != "agent-2" {
		t.Fatalf("agent id = %q, want agent-2", a.AgentID())
	}
	a.Stop()
	if err := <-errCh; err != nil {
		t.Fatalf("Run = %v after Stop, want nil", err)
	}
}

// A "replaced" answer to a call made under an id a reconnect has since
// changed is about the old registration; acting on it would make the current
// agent stand by.
func TestAgent_ReplacedFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		id   string
		want error
	}{
		{"current id replaced", status.Error(codes.FailedPrecondition, "replaced"), "current", errReplaced},
		{"stale id replaced", status.Error(codes.FailedPrecondition, "replaced"), "old", nil},
		{"current id not found", status.Error(codes.NotFound, "agent not registered"), "current", nil},
		{"no error", nil, "current", nil},
	}
	a := New(&Config{})
	a.agentID = "current"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.replacedFor(tt.err, tt.id); got != tt.want {
				t.Fatalf("replacedFor = %v, want %v", got, tt.want)
			}
		})
	}
}

// The command poll runs more often than the heartbeat, so it is usually the
// first to learn the agent was replaced.
func TestAgent_Poll_Replaced(t *testing.T) {
	tests := []struct {
		name    string
		code    codes.Code
		wantErr error
	}{
		{"replaced", codes.FailedPrecondition, errReplaced},
		{"other errors are left to the heartbeat", codes.NotFound, nil},
		{"ok", codes.OK, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, cleanup := startMockServer(t, &mockAgentService{pollCode: tt.code})
			defer cleanup()
			a := New(&Config{ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"}, Cluster: ClusterConfig{Name: "test"}})
			ctx := context.Background()
			if err := a.connect(ctx); err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer a.disconnect()
			if err := a.register(ctx); err != nil {
				t.Fatalf("register: %v", err)
			}
			if err := a.pollAndExecuteCommands(ctx); !errors.Is(err, tt.wantErr) {
				t.Fatalf("poll = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

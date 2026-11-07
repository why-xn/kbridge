package agent

import (
	"context"
	"net"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// commandService hands out one long-running command and records the result.
type commandService struct {
	mockAgentService
	mu      sync.Mutex
	handed  bool
	results []*agentpb.SubmitCommandResultRequest
	seconds string // how long the handed-out command sleeps; default 30
	// replaceWhenHanded answers heartbeats as "replaced" once the command is out.
	replaceWhenHanded bool
}

func (c *commandService) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	if c.replaceWhenHanded && c.handedOut() {
		return nil, status.Error(codes.FailedPrecondition, "agent replaced by a newer registration for this cluster")
	}
	return c.mockAgentService.Heartbeat(ctx, req)
}

func (c *commandService) GetPendingCommands(ctx context.Context, req *agentpb.GetPendingCommandsRequest) (*agentpb.GetPendingCommandsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handed {
		return &agentpb.GetPendingCommandsResponse{}, nil
	}
	c.handed = true
	seconds := c.seconds
	if seconds == "" {
		seconds = "30"
	}
	return &agentpb.GetPendingCommandsResponse{Commands: []*agentpb.CommandRequest{
		{RequestId: "req-1", Command: []string{seconds}, TimeoutSeconds: 60, Identity: &agentpb.Identity{User: "kbridge:alice@example.com"}},
	}}, nil
}

func (c *commandService) SubmitCommandResult(ctx context.Context, req *agentpb.SubmitCommandResultRequest) (*agentpb.SubmitCommandResultResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, req)
	return &agentpb.SubmitCommandResultResponse{Success: true}, nil
}

func startCommandServer(t *testing.T, svc *commandService) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := grpc.NewServer()
	agentpb.RegisterAgentServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), srv.Stop
}

func (c *commandService) handedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handed
}

// A command running when the agent is told to shut down (SIGTERM, e.g. the
// old pod of a rollout) must finish and be reported with its real result,
// whether or not the agent has learned yet that a newer one replaced it.
func TestAgent_ShutdownLetsInFlightCommandFinish(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	tests := []struct {
		name     string
		replaced bool
	}{
		{"not yet told it was replaced", false},
		{"replaced first", true},
	}
	defer func(d time.Duration) { initialHeartbeatInterval = d }(initialHeartbeatInterval)
	initialHeartbeatInterval = 50 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &commandService{seconds: "3", replaceWhenHanded: tt.replaced}
			addr, stop := startCommandServer(t, svc)
			defer stop()
			a := New(&Config{
				ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   kubeFixture(t),
			})
			a.executor = windowedExecutor(t, "sleep") // "kubectl 3" -> sleep 3; Run swaps in the agent's window

			ctx, cancel := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- a.Run(ctx) }()
			handed := waitHandedOut(t, svc)
			if tt.replaced {
				<-a.replaced
			}
			cancel() // same order as cmd/agent: cancel, then Stop
			a.Stop()
			if err := <-runDone; err != nil {
				t.Fatalf("Run = %v, want nil", err)
			}
			if took := time.Since(handed); took < 2500*time.Millisecond {
				t.Fatalf("Run returned %s after the command started, before it could finish", took)
			}
			svc.mu.Lock()
			defer svc.mu.Unlock()
			if len(svc.results) != 1 || svc.results[0].GetRequestId() != "req-1" ||
				svc.results[0].GetErrorMessage() != "" || svc.results[0].GetExitCode() != 0 {
				t.Fatalf("results = %+v, want req-1 reported as run (exit 0, no error)", svc.results)
			}
		})
	}
}

// waitHandedOut waits until the agent has picked up the command and returns when.
func waitHandedOut(t *testing.T, svc *commandService) time.Time {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !svc.handedOut() {
		if time.Now().After(deadline) {
			t.Fatal("agent never picked up the command")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return time.Now()
}

package agent

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
)

// commandService hands out one long-running command and records the result.
type commandService struct {
	mockAgentService
	mu      sync.Mutex
	handed  bool
	results []*agentpb.SubmitCommandResultRequest
}

func (c *commandService) GetPendingCommands(ctx context.Context, req *agentpb.GetPendingCommandsRequest) (*agentpb.GetPendingCommandsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handed {
		return &agentpb.GetPendingCommandsResponse{}, nil
	}
	c.handed = true
	return &agentpb.GetPendingCommandsResponse{Commands: []*agentpb.CommandRequest{
		{RequestId: "req-1", Command: []string{"30"}, TimeoutSeconds: 60},
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

// A command running when the agent shuts down (e.g. a rollout) must be
// reported as failed before the agent disconnects, not silently dropped.
func TestAgent_ShutdownReportsInFlightCommand(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	svc := &commandService{}
	addr, stop := startCommandServer(t, svc)
	defer stop()

	a := New(&Config{
		ControlPlane: ControlPlaneConfig{URL: addr, Token: "token"},
		Cluster:      ClusterConfig{Name: "test"},
	})
	a.executor = &KubectlExecutor{kubectlPath: "sleep"} // "kubectl 30" -> sleep 30

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !svc.handedOut() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !svc.handedOut() {
		t.Fatal("agent never picked up the command")
	}
	time.Sleep(200 * time.Millisecond) // let the command start running

	cancel() // same order as cmd/agent: cancel, then Stop
	stopped := make(chan struct{})
	go func() { a.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		t.Fatal("agent did not stop")
	}
	<-runDone

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.results) != 1 {
		t.Fatalf("submitted %d results, want 1", len(svc.results))
	}
	if got := svc.results[0]; got.GetRequestId() != "req-1" || !strings.Contains(got.GetErrorMessage(), "shut down") {
		t.Fatalf("unexpected result: %+v", got)
	}
}

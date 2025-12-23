package central

import (
	"context"
	"testing"

	"github.com/why-xn/mk8s/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCServer_Register(t *testing.T) {
	srv := NewGRPCServer()
	ctx := context.Background()

	tests := []struct {
		name        string
		clusterName string
		wantAgentID string
	}{
		{
			name:        "register cluster",
			clusterName: "test-cluster",
			wantAgentID: "agent-test-cluster",
		},
		{
			name:        "register another cluster",
			clusterName: "production",
			wantAgentID: "agent-production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &agentpb.RegisterRequest{
				AgentToken:  "test-token",
				ClusterName: tt.clusterName,
				Metadata: &agentpb.ClusterMetadata{
					KubernetesVersion: "1.28.0",
					NodeCount:         3,
				},
			}

			resp, err := srv.Register(ctx, req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !resp.Success {
				t.Error("expected success=true")
			}

			if resp.AgentId != tt.wantAgentID {
				t.Errorf("expected agent_id=%q, got %q", tt.wantAgentID, resp.AgentId)
			}
		})
	}
}

func TestGRPCServer_Heartbeat(t *testing.T) {
	srv := NewGRPCServer()
	ctx := context.Background()

	req := &agentpb.HeartbeatRequest{
		AgentId: "agent-test-cluster",
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
}

func TestGRPCServer_ExecuteCommand(t *testing.T) {
	srv := NewGRPCServer()

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

func TestGenerateAgentID(t *testing.T) {
	tests := []struct {
		clusterName string
		want        string
	}{
		{"dev", "agent-dev"},
		{"production-us-east", "agent-production-us-east"},
		{"", "agent-"},
	}

	for _, tt := range tests {
		t.Run(tt.clusterName, func(t *testing.T) {
			got := generateAgentID(tt.clusterName)
			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

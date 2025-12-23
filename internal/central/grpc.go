package central

import (
	"context"
	"log"

	"github.com/why-xn/mk8s/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultHeartbeatInterval is the default interval between heartbeats in seconds.
const DefaultHeartbeatInterval = 30

// GRPCServer handles gRPC requests from agents.
type GRPCServer struct {
	agentpb.UnimplementedAgentServiceServer
}

// NewGRPCServer creates a new gRPC server.
func NewGRPCServer() *GRPCServer {
	return &GRPCServer{}
}

// RegisterWithServer registers the agent service with a gRPC server.
func (s *GRPCServer) RegisterWithServer(srv *grpc.Server) {
	agentpb.RegisterAgentServiceServer(srv, s)
}

// Register handles agent registration requests.
func (s *GRPCServer) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	log.Printf("Agent registration request: cluster=%s", req.GetClusterName())

	// Generate a simple agent ID (will be replaced with proper implementation later)
	agentID := generateAgentID(req.GetClusterName())

	return &agentpb.RegisterResponse{
		Success: true,
		AgentId: agentID,
	}, nil
}

// Heartbeat handles agent heartbeat requests.
func (s *GRPCServer) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	log.Printf("Heartbeat from agent: id=%s, status=%s", req.GetAgentId(), req.GetStatus())

	return &agentpb.HeartbeatResponse{
		Acknowledged:         true,
		NextHeartbeatSeconds: DefaultHeartbeatInterval,
	}, nil
}

// ExecuteCommand handles command execution requests.
func (s *GRPCServer) ExecuteCommand(req *agentpb.CommandRequest, stream grpc.ServerStreamingServer[agentpb.CommandResponse]) error {
	log.Printf("ExecuteCommand request: agent=%s, command=%v", req.GetAgentId(), req.GetCommand())

	return status.Error(codes.Unimplemented, "ExecuteCommand not implemented")
}

// generateAgentID creates a unique identifier for an agent.
func generateAgentID(clusterName string) string {
	// Simple implementation for now - will be replaced with UUID or similar
	return "agent-" + clusterName
}

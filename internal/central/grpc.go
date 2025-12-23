package central

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	store *AgentStore
}

// NewGRPCServer creates a new gRPC server with the given agent store.
func NewGRPCServer(store *AgentStore) *GRPCServer {
	return &GRPCServer{
		store: store,
	}
}

// RegisterWithServer registers the agent service with a gRPC server.
func (s *GRPCServer) RegisterWithServer(srv *grpc.Server) {
	agentpb.RegisterAgentServiceServer(srv, s)
}

// Register handles agent registration requests.
func (s *GRPCServer) Register(ctx context.Context, req *agentpb.RegisterRequest) (*agentpb.RegisterResponse, error) {
	log.Printf("Agent registration request: cluster=%s", req.GetClusterName())

	// Validate cluster name
	if req.GetClusterName() == "" {
		return &agentpb.RegisterResponse{
			Success:      false,
			ErrorMessage: "cluster_name is required",
		}, nil
	}

	// Validate agent token
	if !s.store.ValidateToken(req.GetAgentToken()) {
		log.Printf("Invalid agent token for cluster=%s", req.GetClusterName())
		return &agentpb.RegisterResponse{
			Success:      false,
			ErrorMessage: "invalid agent token",
		}, nil
	}

	// Generate unique agent ID
	agentID, err := generateAgentID()
	if err != nil {
		log.Printf("Failed to generate agent ID: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to generate agent ID")
	}

	// Build agent info from request
	info := &AgentInfo{
		ID:          agentID,
		ClusterName: req.GetClusterName(),
		Token:       req.GetAgentToken(),
	}

	// Extract metadata if provided
	if meta := req.GetMetadata(); meta != nil {
		info.KubernetesVersion = meta.GetKubernetesVersion()
		info.NodeCount = meta.GetNodeCount()
		info.Region = meta.GetRegion()
		info.Provider = meta.GetProvider()
	}

	// Store the agent
	s.store.Register(info)
	log.Printf("Agent registered: id=%s, cluster=%s", agentID, req.GetClusterName())

	return &agentpb.RegisterResponse{
		Success: true,
		AgentId: agentID,
	}, nil
}

// Heartbeat handles agent heartbeat requests.
func (s *GRPCServer) Heartbeat(ctx context.Context, req *agentpb.HeartbeatRequest) (*agentpb.HeartbeatResponse, error) {
	log.Printf("Heartbeat from agent: id=%s, status=%s", req.GetAgentId(), req.GetStatus())

	// Validate agent ID
	if req.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	// Convert protobuf status to string
	agentStatus := convertAgentStatus(req.GetStatus())

	// Update heartbeat in store
	if !s.store.UpdateHeartbeat(req.GetAgentId(), agentStatus) {
		log.Printf("Heartbeat from unknown agent: id=%s", req.GetAgentId())
		return nil, status.Error(codes.NotFound, "agent not registered")
	}

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

// generateAgentID creates a unique identifier for an agent using random bytes.
func generateAgentID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return "agent-" + hex.EncodeToString(bytes), nil
}

// convertAgentStatus converts protobuf AgentStatus to internal status string.
func convertAgentStatus(status agentpb.AgentStatus) string {
	switch status {
	case agentpb.AgentStatus_AGENT_STATUS_HEALTHY:
		return AgentStatusConnected
	case agentpb.AgentStatus_AGENT_STATUS_DEGRADED:
		return AgentStatusConnected // Still connected but degraded
	default:
		return AgentStatusConnected
	}
}

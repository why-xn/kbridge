package central

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultHeartbeatInterval is the default interval between heartbeats in seconds.
const DefaultHeartbeatInterval = 30

// GRPCServer handles gRPC requests from agents.
type GRPCServer struct {
	agentpb.UnimplementedAgentServiceServer
	store    *AgentStore
	cmdQueue *CommandQueue
}

// NewGRPCServer creates a new gRPC server with the given agent store and command queue.
func NewGRPCServer(store *AgentStore, cmdQueue *CommandQueue) *GRPCServer {
	return &GRPCServer{
		store:    store,
		cmdQueue: cmdQueue,
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
// NOTE: This is currently unused - see GetPendingCommands for agent-initiated flow.
func (s *GRPCServer) ExecuteCommand(req *agentpb.CommandRequest, stream grpc.ServerStreamingServer[agentpb.CommandResponse]) error {
	log.Printf("ExecuteCommand request: agent=%s, command=%v", req.GetAgentId(), req.GetCommand())

	return status.Error(codes.Unimplemented, "ExecuteCommand not implemented - use GetPendingCommands instead")
}

// GetPendingCommands returns any pending commands for the requesting agent.
func (s *GRPCServer) GetPendingCommands(ctx context.Context, req *agentpb.GetPendingCommandsRequest) (*agentpb.GetPendingCommandsResponse, error) {
	agentID := req.GetAgentId()
	if agentID == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	// Verify agent is registered
	if _, exists := s.store.Get(agentID); !exists {
		return nil, status.Error(codes.NotFound, "agent not registered")
	}

	// Get pending commands for this agent
	pending := s.cmdQueue.GetPendingForAgent(agentID)

	// Convert to protobuf format
	commands := make([]*agentpb.CommandRequest, 0, len(pending))
	for _, cmd := range pending {
		// Mark as running so it's not returned again
		s.cmdQueue.MarkRunning(cmd.RequestID)

		commands = append(commands, &agentpb.CommandRequest{
			RequestId:      cmd.RequestID,
			AgentId:        cmd.AgentID,
			Command:        cmd.Command,
			Namespace:      cmd.Namespace,
			TimeoutSeconds: cmd.TimeoutSeconds,
			Stdin:          cmd.Stdin,
		})
	}

	if len(commands) > 0 {
		log.Printf("Returning %d pending commands for agent %s", len(commands), agentID)
	}

	return &agentpb.GetPendingCommandsResponse{
		Commands: commands,
	}, nil
}

// SubmitCommandResult receives the result of a command execution from an agent.
func (s *GRPCServer) SubmitCommandResult(ctx context.Context, req *agentpb.SubmitCommandResultRequest) (*agentpb.SubmitCommandResultResponse, error) {
	requestID := req.GetRequestId()
	if requestID == "" {
		return nil, status.Error(codes.InvalidArgument, "request_id is required")
	}

	log.Printf("Received command result: request_id=%s, exit_code=%d", requestID, req.GetExitCode())

	result := &CommandResult{
		RequestID:    requestID,
		Stdout:       req.GetStdout(),
		Stderr:       req.GetStderr(),
		ExitCode:     req.GetExitCode(),
		ErrorMessage: req.GetErrorMessage(),
	}

	if req.GetErrorMessage() != "" {
		// Command failed to execute
		s.cmdQueue.Fail(requestID, req.GetErrorMessage())
	} else {
		// Command completed (success or non-zero exit)
		s.cmdQueue.Complete(requestID, result)
	}

	return &agentpb.SubmitCommandResultResponse{
		Success: true,
	}, nil
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

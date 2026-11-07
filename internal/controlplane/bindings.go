package controlplane

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/api/proto/agentpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// noPolicyVersion is the bindings version served when no RBAC policy is loaded.
const noPolicyVersion = "none"

// Limits on the untrusted error strings an agent reports.
const (
	maxSyncErrors     = 50
	maxSyncErrorBytes = 500
)

// Sync states shown to admins.
const (
	SyncStateInSync     = "in sync"
	SyncStateDrift      = "drift"
	SyncStateUnreported = "unreported"
)

// SyncStatus is an agent's last binding reconcile report.
type SyncStatus struct {
	Version    string
	InSync     bool
	Errors     []string
	ReportedAt time.Time
}

// SetSyncStatus records an agent's sync report; false if the agent is unknown.
func (s *AgentStore) SetSyncStatus(agentID string, st SyncStatus) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent, ok := s.agents[agentID]
	if !ok {
		return false
	}
	agent.SyncStatus = &st // replaced whole, never mutated, so shallow copies stay safe
	return true
}

// desiredFor lists users from the store, then asks the policy.
func (s *GRPCServer) desiredFor(ctx context.Context, cluster string) ([]DesiredBinding, string, error) {
	return desiredBindingsFor(ctx, s.authn.store, s.policy, cluster)
}

// desiredBindingsFor computes the bindings cluster should carry for the
// store's active users. Inactive users are left out so they lose access.
func desiredBindingsFor(ctx context.Context, store Store, policy *PolicyEngine, cluster string) ([]DesiredBinding, string, error) {
	if policy == nil {
		return []DesiredBinding{}, noPolicyVersion, nil
	}
	users, err := store.ListUsers(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("listing users: %w", err)
	}
	emails := make([]string, 0, len(users))
	for _, u := range users {
		if u.IsActive {
			emails = append(emails, u.Email)
		}
	}
	b := policy.DesiredBindings(cluster, emails)
	return b, BindingsVersion(b), nil
}

// heartbeatVersion is the desired version for an agent, or "" when it cannot
// be computed; a heartbeat never fails over it.
func (s *GRPCServer) heartbeatVersion(ctx context.Context, agentID string) string {
	agent, ok := s.agents.Get(agentID)
	if !ok {
		return ""
	}
	_, version, err := s.desiredFor(ctx, agent.ClusterName)
	if err != nil {
		log.Printf("computing desired bindings for cluster=%s: %v", agent.ClusterName, err)
	}
	return version
}

// callingAgent resolves the agent an RPC comes from. The agent ID issued at
// Register is the agent's credential, so an unknown ID is refused.
func (s *GRPCServer) callingAgent(agentID string) (*AgentInfo, error) {
	if agentID == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	agent, ok := s.agents.Get(agentID)
	if !ok {
		return nil, s.notRegistered(agentID, codes.PermissionDenied)
	}
	return agent, nil
}

// GetDesiredBindings returns the bindings for the calling agent's own cluster.
func (s *GRPCServer) GetDesiredBindings(ctx context.Context, req *agentpb.GetDesiredBindingsRequest) (*agentpb.GetDesiredBindingsResponse, error) {
	agent, err := s.callingAgent(req.GetAgentId())
	if err != nil {
		return nil, err
	}
	bindings, version, err := s.desiredFor(ctx, agent.ClusterName)
	if err != nil {
		log.Printf("computing desired bindings for cluster=%s: %v", agent.ClusterName, err)
		return nil, status.Error(codes.Internal, "computing desired bindings")
	}
	return &agentpb.GetDesiredBindingsResponse{Version: version, Bindings: toProtoBindings(bindings)}, nil
}

func toProtoBindings(bindings []DesiredBinding) []*agentpb.DesiredBinding {
	out := make([]*agentpb.DesiredBinding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, &agentpb.DesiredBinding{
			Name: b.Name, Namespace: b.Namespace, ClusterRole: b.ClusterRole, Users: b.Users,
		})
	}
	return out
}

// ReportBindingSync records the calling agent's last reconcile outcome.
func (s *GRPCServer) ReportBindingSync(ctx context.Context, req *agentpb.ReportBindingSyncRequest) (*agentpb.ReportBindingSyncResponse, error) {
	if _, err := s.callingAgent(req.GetAgentId()); err != nil {
		return nil, err
	}
	st := SyncStatus{
		Version:    sanitizeAgentString(req.GetVersion()),
		InSync:     req.GetInSync(),
		Errors:     capSyncErrors(req.GetErrors()),
		ReportedAt: time.Now().UTC(),
	}
	if !s.agents.SetSyncStatus(req.GetAgentId(), st) {
		return nil, s.notRegistered(req.GetAgentId(), codes.PermissionDenied)
	}
	return &agentpb.ReportBindingSyncResponse{}, nil
}

// capSyncErrors bounds the agent's untrusted error list before it is stored.
func capSyncErrors(errs []string) []string {
	if len(errs) > maxSyncErrors {
		errs = errs[:maxSyncErrors]
	}
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, sanitizeAgentString(e))
	}
	return out
}

// sanitizeAgentString drops control characters (no terminal escapes reach an
// admin's screen) and truncates to maxSyncErrorBytes on a UTF-8 boundary.
func sanitizeAgentString(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > maxSyncErrorBytes {
		s = strings.ToValidUTF8(s[:maxSyncErrorBytes], "")
	}
	return s
}

// syncState says whether a cluster's last report matches the desired version.
func syncState(st *SyncStatus, desired string) string {
	switch {
	case st == nil:
		return SyncStateUnreported
	case st.InSync && st.Version == desired:
		return SyncStateInSync
	default:
		return SyncStateDrift
	}
}

// rbacClusterStatus is one cluster's row in the admin RBAC sync status.
type rbacClusterStatus struct {
	Cluster        string     `json:"cluster"`
	Version        string     `json:"version,omitempty"`
	DesiredVersion string     `json:"desired_version"`
	State          string     `json:"state"`
	Errors         []string   `json:"errors,omitempty"`
	ReportedAt     *time.Time `json:"reported_at,omitempty"`
}

// handleRBACStatus lists each connected cluster's binding sync state.
func (s *HTTPServer) handleRBACStatus(c *gin.Context) {
	agents := s.agentStore.List()
	sort.Slice(agents, func(i, j int) bool { return agents[i].ClusterName < agents[j].ClusterName })
	out := make([]rbacClusterStatus, 0, len(agents))
	for _, a := range agents {
		row, err := s.rbacStatusFor(c.Request.Context(), a)
		if err != nil {
			log.Printf("rbac status for cluster=%s: %v", a.ClusterName, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"clusters": out})
}

func (s *HTTPServer) rbacStatusFor(ctx context.Context, a *AgentInfo) (rbacClusterStatus, error) {
	_, desired, err := desiredBindingsFor(ctx, s.adminHandlers.store, s.policy, a.ClusterName)
	if err != nil {
		return rbacClusterStatus{}, err
	}
	row := rbacClusterStatus{Cluster: a.ClusterName, DesiredVersion: desired, State: syncState(a.SyncStatus, desired)}
	if st := a.SyncStatus; st != nil {
		row.Version, row.Errors, row.ReportedAt = st.Version, st.Errors, &st.ReportedAt
	}
	return row, nil
}

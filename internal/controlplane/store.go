package controlplane

import (
	"sync"
	"time"
)

// AgentInfo represents a registered agent and its current state.
type AgentInfo struct {
	ID          string
	ClusterName string
	Token       string
	// TokenID is the id of the agent token it registered with, so revoking
	// the token can find and drop it.
	TokenID     string
	Status      string
	RegisteredAt time.Time
	LastSeen    time.Time
	// Capabilities the agent advertised at registration, e.g. "impersonation".
	Capabilities []string
	// SyncStatus is the agent's last binding sync report; nil until it reports.
	SyncStatus *SyncStatus
}

// AgentStatus constants for agent connection state.
const (
	AgentStatusConnected    = "connected"
	AgentStatusDisconnected = "disconnected"
)

// DisconnectTimeout is the duration after which an agent without heartbeat is marked disconnected.
const DisconnectTimeout = 60 * time.Second

// replacedTTL is how long a replaced agent id is remembered: longer than the
// agent's 10 minute stand-by, so a standing-by agent is never forgotten (and
// told NotFound, which would make it take the cluster back) while the agent
// that replaced it is healthy; short enough that the map stays small.
const replacedTTL = 15 * time.Minute

// replacedAgent records when an agent id was replaced and for which cluster.
type replacedAgent struct {
	at      time.Time
	cluster string
}

// AgentStore manages registered agents in memory.
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]*AgentInfo
	// validTokens holds pre-configured agent tokens for validation.
	// In production, this would be stored in a database.
	validTokens map[string]bool
	// revoked names clusters whose agent was dropped by token revocation, so
	// the clusters list shows them disconnected until an agent registers again.
	revoked map[string]bool
	// replaced holds ids a newer registration for the same cluster replaced,
	// with when and for which cluster, so their late calls are refused instead
	// of re-registering while the replacing agent is connected.
	replaced map[string]replacedAgent
	// OnDisconnect, when set, is called (outside the lock) for each agent
	// marked disconnected or dropped, so queued commands can fail fast.
	OnDisconnect func(agentID, clusterName string)
}

// NewAgentStore creates a new agent store.
func NewAgentStore() *AgentStore {
	return &AgentStore{
		agents:      make(map[string]*AgentInfo),
		validTokens: make(map[string]bool),
		revoked:     make(map[string]bool),
		replaced:    make(map[string]replacedAgent),
	}
}

// AddValidToken adds a token that agents can use to register.
func (s *AgentStore) AddValidToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validTokens[token] = true
}

// ValidateToken checks if the provided token is valid for agent registration.
func (s *AgentStore) ValidateToken(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.validTokens[token]
}

// Register adds or updates an agent in the store. A cluster is served by its
// most recently registered agent: any other agent registered for the same
// cluster (e.g. the pod a rollout just replaced) is dropped so requests are
// never routed to it. Their IDs are returned for logging and remembered for
// replacedTTL (see WasReplaced).
func (s *AgentStore) Register(info *AgentInfo) (replaced []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.evictReplaced(now)
	for id, agent := range s.agents {
		if id != info.ID && agent.ClusterName == info.ClusterName {
			delete(s.agents, id)
			s.replaced[id] = replacedAgent{at: now, cluster: agent.ClusterName}
			replaced = append(replaced, id)
		}
	}
	delete(s.revoked, info.ClusterName)
	info.RegisteredAt = time.Now()
	info.LastSeen = time.Now()
	info.Status = AgentStatusConnected
	s.agents[info.ID] = info
	return replaced
}

// evictReplaced forgets replaced ids older than replacedTTL; call with the lock.
func (s *AgentStore) evictReplaced(now time.Time) {
	for id, r := range s.replaced {
		if now.Sub(r.at) > replacedTTL {
			delete(s.replaced, id)
		}
	}
}

// WasReplaced reports whether a newer registration for the same cluster
// replaced this agent id within the last replacedTTL.
func (s *AgentStore) WasReplaced(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.replaced[agentID]
	return ok
}

// ReplacerConnected reports whether agentID was replaced and its cluster is
// now served by a connected agent. When the replacing agent is gone, a
// replaced agent standing by may take the cluster back.
func (s *AgentStore) ReplacerConnected(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.replaced[agentID]
	if !ok {
		return false
	}
	for _, agent := range s.agents {
		if agent.ClusterName == r.cluster && agent.Status == AgentStatusConnected {
			return true
		}
	}
	return false
}

// UpdateHeartbeat updates the last seen timestamp for an agent.
func (s *AgentStore) UpdateHeartbeat(agentID string, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return false
	}

	agent.LastSeen = time.Now()
	agent.Status = status
	return true
}

// Get retrieves an agent by ID.
func (s *AgentStore) Get(agentID string) (*AgentInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agent, exists := s.agents[agentID]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	copy := *agent
	return &copy, true
}

// GetByClusterName retrieves an agent by cluster name.
func (s *AgentStore) GetByClusterName(clusterName string) (*AgentInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, agent := range s.agents {
		if agent.ClusterName == clusterName {
			copy := *agent
			return &copy, true
		}
	}
	return nil, false
}

// List returns all registered agents.
func (s *AgentStore) List() []*AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*AgentInfo, 0, len(s.agents))
	for _, agent := range s.agents {
		copy := *agent
		result = append(result, &copy)
	}
	return result
}

// MarkDisconnected marks agents without recent heartbeats as disconnected.
func (s *AgentStore) MarkDisconnected() {
	var dropped []*AgentInfo
	s.mu.Lock()
	cutoff := time.Now().Add(-DisconnectTimeout)
	for _, agent := range s.agents {
		if agent.Status == AgentStatusConnected && agent.LastSeen.Before(cutoff) {
			agent.Status = AgentStatusDisconnected
			dropped = append(dropped, agent)
		}
	}
	s.mu.Unlock()
	s.notifyDisconnected(dropped)
}

// MarkAgentDisconnected marks one connected agent disconnected now, without
// waiting for DisconnectTimeout, and reports whether it did. A later
// heartbeat marks it connected again.
func (s *AgentStore) MarkAgentDisconnected(agentID string) bool {
	s.mu.Lock()
	agent, ok := s.agents[agentID]
	ok = ok && agent.Status == AgentStatusConnected
	if ok {
		agent.Status = AgentStatusDisconnected
	}
	s.mu.Unlock()
	if ok {
		s.notifyDisconnected([]*AgentInfo{agent})
	}
	return ok
}

// DropByToken removes every agent registered with the given agent token and
// returns their ids. Their next call gets NotFound, so they re-register and
// are refused if the token is revoked.
func (s *AgentStore) DropByToken(tokenID string) []string {
	var dropped []*AgentInfo
	var ids []string
	s.mu.Lock()
	for id, agent := range s.agents {
		if agent.TokenID == tokenID {
			delete(s.agents, id)
			s.revoked[agent.ClusterName] = true
			dropped = append(dropped, agent)
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	s.notifyDisconnected(dropped)
	return ids
}

// RevokedClusters lists clusters whose agent was dropped by DropByToken and
// has not registered again.
func (s *AgentStore) RevokedClusters() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.revoked))
	for name := range s.revoked {
		names = append(names, name)
	}
	return names
}

// notifyDisconnected fires OnDisconnect for each agent; call without the lock.
func (s *AgentStore) notifyDisconnected(agents []*AgentInfo) {
	if s.OnDisconnect == nil {
		return
	}
	for _, a := range agents {
		s.OnDisconnect(a.ID, a.ClusterName)
	}
}

// Remove removes an agent from the store.
func (s *AgentStore) Remove(agentID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.agents[agentID]; exists {
		delete(s.agents, agentID)
		return true
	}
	return false
}

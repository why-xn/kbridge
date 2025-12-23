package central

import (
	"sync"
	"time"
)

// AgentInfo represents a registered agent and its current state.
type AgentInfo struct {
	ID                string
	ClusterName       string
	Token             string
	Status            string
	KubernetesVersion string
	NodeCount         int32
	Region            string
	Provider          string
	RegisteredAt      time.Time
	LastSeen          time.Time
}

// AgentStatus constants for agent connection state.
const (
	AgentStatusConnected    = "connected"
	AgentStatusDisconnected = "disconnected"
)

// DisconnectTimeout is the duration after which an agent without heartbeat is marked disconnected.
const DisconnectTimeout = 60 * time.Second

// AgentStore manages registered agents in memory.
type AgentStore struct {
	mu     sync.RWMutex
	agents map[string]*AgentInfo
	// validTokens holds pre-configured agent tokens for validation.
	// In production, this would be stored in a database.
	validTokens map[string]bool
}

// NewAgentStore creates a new agent store.
func NewAgentStore() *AgentStore {
	return &AgentStore{
		agents:      make(map[string]*AgentInfo),
		validTokens: make(map[string]bool),
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

// Register adds or updates an agent in the store.
func (s *AgentStore) Register(info *AgentInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info.RegisteredAt = time.Now()
	info.LastSeen = time.Now()
	info.Status = AgentStatusConnected
	s.agents[info.ID] = info
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
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-DisconnectTimeout)
	for _, agent := range s.agents {
		if agent.Status == AgentStatusConnected && agent.LastSeen.Before(cutoff) {
			agent.Status = AgentStatusDisconnected
		}
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

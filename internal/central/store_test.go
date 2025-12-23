package central

import (
	"sync"
	"testing"
	"time"
)

func TestNewAgentStore(t *testing.T) {
	store := NewAgentStore()
	if store == nil {
		t.Fatal("NewAgentStore returned nil")
	}

	agents := store.List()
	if len(agents) != 0 {
		t.Errorf("expected empty store, got %d agents", len(agents))
	}
}

func TestAgentStore_AddValidToken(t *testing.T) {
	store := NewAgentStore()

	// Token should not be valid initially
	if store.ValidateToken("test-token") {
		t.Error("token should not be valid before adding")
	}

	// Add token
	store.AddValidToken("test-token")

	// Token should now be valid
	if !store.ValidateToken("test-token") {
		t.Error("token should be valid after adding")
	}

	// Other tokens should still be invalid
	if store.ValidateToken("other-token") {
		t.Error("other tokens should not be valid")
	}
}

func TestAgentStore_Register(t *testing.T) {
	store := NewAgentStore()

	info := &AgentInfo{
		ID:                "agent-1",
		ClusterName:       "test-cluster",
		KubernetesVersion: "1.28.0",
		NodeCount:         3,
		Region:            "us-east-1",
		Provider:          "aws",
	}

	store.Register(info)

	// Verify agent is registered
	agents := store.List()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	if agents[0].ID != "agent-1" {
		t.Errorf("expected agent ID 'agent-1', got %q", agents[0].ID)
	}

	if agents[0].Status != AgentStatusConnected {
		t.Errorf("expected status %q, got %q", AgentStatusConnected, agents[0].Status)
	}

	if agents[0].RegisteredAt.IsZero() {
		t.Error("RegisteredAt should be set")
	}

	if agents[0].LastSeen.IsZero() {
		t.Error("LastSeen should be set")
	}
}

func TestAgentStore_Get(t *testing.T) {
	store := NewAgentStore()

	// Get non-existent agent
	_, exists := store.Get("non-existent")
	if exists {
		t.Error("expected false for non-existent agent")
	}

	// Register and get agent
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test"})

	agent, exists := store.Get("agent-1")
	if !exists {
		t.Fatal("expected agent to exist")
	}

	if agent.ID != "agent-1" {
		t.Errorf("expected ID 'agent-1', got %q", agent.ID)
	}
}

func TestAgentStore_GetByClusterName(t *testing.T) {
	store := NewAgentStore()

	// Get non-existent cluster
	_, exists := store.GetByClusterName("non-existent")
	if exists {
		t.Error("expected false for non-existent cluster")
	}

	// Register and get by cluster name
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "production"})

	agent, exists := store.GetByClusterName("production")
	if !exists {
		t.Fatal("expected agent to exist")
	}

	if agent.ClusterName != "production" {
		t.Errorf("expected cluster name 'production', got %q", agent.ClusterName)
	}
}

func TestAgentStore_UpdateHeartbeat(t *testing.T) {
	store := NewAgentStore()

	// Update non-existent agent
	if store.UpdateHeartbeat("non-existent", AgentStatusConnected) {
		t.Error("expected false for non-existent agent")
	}

	// Register agent
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test"})

	// Wait a moment so we can detect time change
	time.Sleep(10 * time.Millisecond)

	// Update heartbeat
	if !store.UpdateHeartbeat("agent-1", AgentStatusConnected) {
		t.Error("expected true for existing agent")
	}

	// Verify LastSeen was updated
	agent, _ := store.Get("agent-1")
	if time.Since(agent.LastSeen) > 100*time.Millisecond {
		t.Error("LastSeen should be very recent")
	}
}

func TestAgentStore_MarkDisconnected(t *testing.T) {
	store := NewAgentStore()

	// Register an agent with old LastSeen time
	info := &AgentInfo{
		ID:          "agent-1",
		ClusterName: "test",
	}
	store.Register(info)

	// Manually set LastSeen to old time (simulate 2 minutes ago)
	store.mu.Lock()
	store.agents["agent-1"].LastSeen = time.Now().Add(-2 * time.Minute)
	store.mu.Unlock()

	// Mark disconnected
	store.MarkDisconnected()

	// Verify agent is now disconnected
	agent, _ := store.Get("agent-1")
	if agent.Status != AgentStatusDisconnected {
		t.Errorf("expected status %q, got %q", AgentStatusDisconnected, agent.Status)
	}
}

func TestAgentStore_MarkDisconnected_RecentHeartbeat(t *testing.T) {
	store := NewAgentStore()

	// Register an agent with recent heartbeat
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test"})

	// Mark disconnected (should not change status since heartbeat is recent)
	store.MarkDisconnected()

	// Verify agent is still connected
	agent, _ := store.Get("agent-1")
	if agent.Status != AgentStatusConnected {
		t.Errorf("expected status %q, got %q", AgentStatusConnected, agent.Status)
	}
}

func TestAgentStore_Remove(t *testing.T) {
	store := NewAgentStore()

	// Remove non-existent agent
	if store.Remove("non-existent") {
		t.Error("expected false for non-existent agent")
	}

	// Register and remove agent
	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test"})

	if !store.Remove("agent-1") {
		t.Error("expected true for existing agent")
	}

	// Verify agent is removed
	_, exists := store.Get("agent-1")
	if exists {
		t.Error("agent should not exist after removal")
	}
}

func TestAgentStore_List_ReturnscopyCopies(t *testing.T) {
	store := NewAgentStore()

	store.Register(&AgentInfo{ID: "agent-1", ClusterName: "test"})

	// Get list
	agents := store.List()

	// Modify the returned slice
	agents[0].ClusterName = "modified"

	// Original should be unchanged
	agent, _ := store.Get("agent-1")
	if agent.ClusterName == "modified" {
		t.Error("List should return copies, not references")
	}
}

func TestAgentStore_Concurrent(t *testing.T) {
	store := NewAgentStore()
	store.AddValidToken("test-token")

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent registrations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				store.Register(&AgentInfo{
					ID:          "agent-" + string(rune(id)) + "-" + string(rune(j)),
					ClusterName: "cluster",
				})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				store.List()
				store.ValidateToken("test-token")
			}
		}()
	}

	// Concurrent heartbeat updates
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				store.UpdateHeartbeat("agent-0", AgentStatusConnected)
			}
		}()
	}

	wg.Wait()
}

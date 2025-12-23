package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/why-xn/mk8s/api/proto/agentpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Agent represents the mk8s agent that connects to central service.
type Agent struct {
	config   *Config
	conn     *grpc.ClientConn
	client   agentpb.AgentServiceClient
	agentID  string
	mu       sync.RWMutex
	stopCh   chan struct{}
	stoppedCh chan struct{}
}

// New creates a new agent with the given configuration.
func New(cfg *Config) *Agent {
	return &Agent{
		config:    cfg,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

// Run starts the agent, connecting to central and maintaining the connection.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("Agent starting for cluster: %s", a.config.Cluster.Name)

	if err := a.connect(ctx); err != nil {
		return fmt.Errorf("connecting to central: %w", err)
	}
	defer a.disconnect()

	if err := a.register(ctx); err != nil {
		return fmt.Errorf("registering with central: %w", err)
	}

	// Run heartbeat loop
	a.runHeartbeatLoop(ctx)

	close(a.stoppedCh)
	return nil
}

// Stop signals the agent to stop and waits for it to finish.
func (a *Agent) Stop() {
	close(a.stopCh)
	<-a.stoppedCh
}

// AgentID returns the agent's assigned ID from central.
func (a *Agent) AgentID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agentID
}

func (a *Agent) connect(ctx context.Context) error {
	log.Printf("Connecting to central service at %s", a.config.Central.URL)

	// Create connection with timeout
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Use insecure credentials for now (TLS will be added later)
	conn, err := grpc.DialContext(dialCtx, a.config.Central.URL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return fmt.Errorf("dialing central: %w", err)
	}

	a.conn = conn
	a.client = agentpb.NewAgentServiceClient(conn)
	log.Printf("Connected to central service")
	return nil
}

func (a *Agent) disconnect() {
	if a.conn != nil {
		if err := a.conn.Close(); err != nil {
			log.Printf("Error closing connection: %v", err)
		}
		log.Printf("Disconnected from central service")
	}
}

func (a *Agent) register(ctx context.Context) error {
	log.Printf("Registering with central service")

	req := &agentpb.RegisterRequest{
		AgentToken:  a.config.Central.Token,
		ClusterName: a.config.Cluster.Name,
		Metadata: &agentpb.ClusterMetadata{
			KubernetesVersion: a.config.Cluster.KubernetesVersion,
			NodeCount:         a.config.Cluster.NodeCount,
			Region:            a.config.Cluster.Region,
			Provider:          a.config.Cluster.Provider,
		},
	}

	// Add timeout for registration
	regCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := a.client.Register(regCtx, req)
	if err != nil {
		return fmt.Errorf("register RPC failed: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("registration rejected: %s", resp.ErrorMessage)
	}

	a.mu.Lock()
	a.agentID = resp.AgentId
	a.mu.Unlock()

	log.Printf("Registered successfully with agent ID: %s", resp.AgentId)
	return nil
}

func (a *Agent) runHeartbeatLoop(ctx context.Context) {
	// Default to 30 second interval, will be updated from server response
	interval := 30 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			nextInterval, err := a.sendHeartbeat(ctx)
			if err != nil {
				log.Printf("Heartbeat failed: %v", err)
				// Try to reconnect on heartbeat failure
				if reconnectErr := a.reconnect(ctx); reconnectErr != nil {
					log.Printf("Reconnect failed: %v", reconnectErr)
				}
				continue
			}

			// Update ticker interval if server requested different timing
			if nextInterval > 0 && nextInterval != interval {
				interval = nextInterval
				ticker.Reset(interval)
			}

		case <-a.stopCh:
			log.Printf("Heartbeat loop stopping")
			return

		case <-ctx.Done():
			log.Printf("Context cancelled, stopping heartbeat loop")
			return
		}
	}
}

func (a *Agent) sendHeartbeat(ctx context.Context) (time.Duration, error) {
	a.mu.RLock()
	agentID := a.agentID
	a.mu.RUnlock()

	req := &agentpb.HeartbeatRequest{
		AgentId: agentID,
		Status:  agentpb.AgentStatus_AGENT_STATUS_HEALTHY,
	}

	// Add timeout for heartbeat
	hbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := a.client.Heartbeat(hbCtx, req)
	if err != nil {
		return 0, fmt.Errorf("heartbeat RPC failed: %w", err)
	}

	if !resp.Acknowledged {
		return 0, fmt.Errorf("heartbeat not acknowledged")
	}

	return time.Duration(resp.NextHeartbeatSeconds) * time.Second, nil
}

func (a *Agent) reconnect(ctx context.Context) error {
	log.Printf("Attempting to reconnect to central service")

	// Close existing connection
	a.disconnect()

	// Retry connection with exponential backoff
	backoff := time.Second
	maxBackoff := 30 * time.Second
	maxRetries := 5

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-a.stopCh:
			return fmt.Errorf("agent stopped during reconnection")
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := a.connect(ctx); err != nil {
			log.Printf("Reconnect attempt %d failed: %v", attempt, err)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Re-register after successful connection
		if err := a.register(ctx); err != nil {
			log.Printf("Re-registration failed: %v", err)
			a.disconnect()
			time.Sleep(backoff)
			continue
		}

		log.Printf("Reconnected successfully")
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

package central

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/why-xn/kbridge/internal/auth"
	"google.golang.org/grpc"
)

// DisconnectCheckInterval is how often to check for disconnected agents.
const DisconnectCheckInterval = 15 * time.Second

// Server is the main central service that runs both HTTP and gRPC servers.
type Server struct {
	config       *Config
	httpServer   *http.Server
	grpcServer   *grpc.Server
	agentStore   *AgentStore
	store        Store
	commandQueue *CommandQueue
	policy       *PolicyEngine
	stopCh       chan struct{}
}

// NewServer creates a new central server with the given configuration.
func NewServer(cfg *Config) (*Server, error) {
	agentStore := NewAgentStore()
	commandQueue := NewCommandQueue()

	// Open SQLite store
	dbStore, err := NewSQLiteStore(cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Run migrations
	if err := dbStore.Migrate(context.Background()); err != nil {
		dbStore.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	// Seed admin user if configured
	if cfg.Auth.AdminEmail != "" && cfg.Auth.AdminPassword != "" {
		seedAdminUser(dbStore, cfg)
	}

	// Seed a bootstrap agent token if configured (development convenience)
	if cfg.Bootstrap.AgentToken != "" && cfg.Bootstrap.AgentCluster != "" {
		seedAgentToken(dbStore, cfg)
	}

	// Set up auth components
	jwtManager := auth.NewJWTManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTokenExpiry)
	authHandlers := NewAuthHandlers(dbStore, jwtManager, cfg.Auth.RefreshTokenExpiry)
	adminHandlers := NewAdminHandlers(dbStore)
	authenticator := NewAgentAuthenticator(dbStore)

	// Load the RBAC policy if configured; nil engine means enforcement is off.
	var policy *PolicyEngine
	if cfg.RBAC.PolicyFile != "" {
		policy, err = NewPolicyEngineFromFile(cfg.RBAC.PolicyFile)
		if err != nil {
			dbStore.Close()
			return nil, fmt.Errorf("loading rbac policy: %w", err)
		}
		log.Printf("RBAC enforcement enabled from %s", cfg.RBAC.PolicyFile)
	} else {
		log.Printf("RBAC enforcement disabled (no rbac.policy_file configured)")
	}

	httpHandler := NewHTTPServer(agentStore, commandQueue, authHandlers, adminHandlers, policy, jwtManager)
	grpcHandler := NewGRPCServer(agentStore, commandQueue, authenticator)

	grpcSrv := grpc.NewServer()
	grpcHandler.RegisterWithServer(grpcSrv)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           httpHandler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &Server{
		config:       cfg,
		httpServer:   httpSrv,
		grpcServer:   grpcSrv,
		agentStore:   agentStore,
		store:        dbStore,
		commandQueue: commandQueue,
		policy:       policy,
		stopCh:       make(chan struct{}),
	}, nil
}

func seedAdminUser(store *SQLiteStore, cfg *Config) {
	ctx := context.Background()
	existing, _ := store.GetUserByEmail(ctx, cfg.Auth.AdminEmail)
	if existing != nil {
		return
	}

	hash, err := auth.HashPassword(cfg.Auth.AdminPassword)
	if err != nil {
		log.Printf("Warning: failed to hash admin password: %v", err)
		return
	}

	name := cfg.Auth.AdminName
	if name == "" {
		name = "Admin"
	}

	user := &User{
		Email:        cfg.Auth.AdminEmail,
		PasswordHash: hash,
		Name:         name,
		IsActive:     true,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		log.Printf("Warning: failed to create admin user: %v", err)
		return
	}

	// Assign admin role
	adminRoleID := "00000000-0000-0000-0000-000000000001"
	if err := store.AssignRole(ctx, user.ID, adminRoleID, ""); err != nil {
		log.Printf("Warning: failed to assign admin role: %v", err)
	}
}

// seedAgentToken creates a bootstrap agent token (and its cluster) if one with
// the same value does not already exist. Idempotent across restarts.
func seedAgentToken(store *SQLiteStore, cfg *Config) {
	ctx := context.Background()
	hash := hashToken(cfg.Bootstrap.AgentToken)
	if existing, _ := store.GetAgentTokenByHash(ctx, hash); existing != nil {
		return
	}

	cluster, err := store.GetClusterByName(ctx, cfg.Bootstrap.AgentCluster)
	if err != nil {
		log.Printf("Warning: failed to look up bootstrap cluster: %v", err)
		return
	}
	if cluster == nil {
		cluster = &Cluster{Name: cfg.Bootstrap.AgentCluster, Status: ClusterStatusPending}
		if err := store.CreateCluster(ctx, cluster); err != nil {
			log.Printf("Warning: failed to create bootstrap cluster: %v", err)
			return
		}
	}

	token := &AgentToken{
		ClusterID:   cluster.ID,
		TokenHash:   hash,
		TokenPrefix: cfg.Bootstrap.AgentToken[:min(len(cfg.Bootstrap.AgentToken), agentTokenPrefixLen)],
		Description: "bootstrap token (seeded from config)",
	}
	if err := store.CreateAgentToken(ctx, token); err != nil {
		log.Printf("Warning: failed to seed bootstrap agent token: %v", err)
	}
}

// AgentStore returns the server's agent store for external access.
func (s *Server) AgentStore() *AgentStore {
	return s.agentStore
}

// CommandQueue returns the server's command queue for external access.
func (s *Server) CommandQueue() *CommandQueue {
	return s.commandQueue
}

// Run starts both HTTP and gRPC servers and handles graceful shutdown.
func (s *Server) Run() error {
	errCh := make(chan error, 2)

	// Start disconnect checker in goroutine
	go s.runDisconnectChecker()

	// Start RBAC policy hot-reload watcher if enabled
	if s.policy != nil {
		s.policy.Watch(s.stopCh)
	}

	// Start gRPC server in goroutine
	go func() {
		if err := s.startGRPC(); err != nil {
			errCh <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Start HTTP server in goroutine
	go func() {
		if err := s.startHTTP(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for shutdown signal or error
	return s.waitForShutdown(errCh)
}

func (s *Server) startGRPC() error {
	addr := fmt.Sprintf(":%d", s.config.Server.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	log.Printf("gRPC server listening on %s", addr)
	return s.grpcServer.Serve(lis)
}

func (s *Server) startHTTP() error {
	log.Printf("HTTP server listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// runDisconnectChecker periodically checks for and marks disconnected agents.
func (s *Server) runDisconnectChecker() {
	ticker := time.NewTicker(DisconnectCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.agentStore.MarkDisconnected()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Server) waitForShutdown(errCh chan error) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
		return s.shutdown()
	}
}

func (s *Server) shutdown() error {
	// Signal disconnect checker to stop
	close(s.stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Gracefully stop gRPC server
	s.grpcServer.GracefulStop()
	log.Println("gRPC server stopped")

	// Gracefully shutdown HTTP server
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("HTTP server shutdown error: %w", err)
	}
	log.Println("HTTP server stopped")

	// Close database
	if s.store != nil {
		s.store.Close()
	}

	return nil
}

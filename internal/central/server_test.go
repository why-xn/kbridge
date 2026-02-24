package central

import (
	"net/http"
	"testing"
	"time"
)

func testServerConfig() *Config {
	cfg := DefaultConfig()
	cfg.Database.Path = ":memory:"
	cfg.Auth.JWTSecret = "test-secret-at-least-32-chars!!"
	return cfg
}

func TestNewServer(t *testing.T) {
	cfg := testServerConfig()
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if srv == nil {
		t.Fatal("expected non-nil server")
	}

	if srv.config != cfg {
		t.Error("expected config to be set")
	}

	if srv.httpServer == nil {
		t.Error("expected httpServer to be set")
	}

	if srv.grpcServer == nil {
		t.Error("expected grpcServer to be set")
	}

	if srv.agentStore == nil {
		t.Error("expected agentStore to be set")
	}

	if srv.store == nil {
		t.Error("expected store to be set")
	}

	if srv.stopCh == nil {
		t.Error("expected stopCh to be set")
	}
}

func TestServer_AgentStore(t *testing.T) {
	cfg := testServerConfig()
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	store := srv.AgentStore()
	if store == nil {
		t.Fatal("expected non-nil agent store")
	}

	// Verify default token is added
	if !store.ValidateToken("dev-token") {
		t.Error("expected dev-token to be valid")
	}
}

func TestServer_HTTPServerAddr(t *testing.T) {
	cfg := testServerConfig()
	cfg.Server.HTTPPort = 8888
	cfg.Server.GRPCPort = 9999

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedAddr := ":8888"
	if srv.httpServer.Addr != expectedAddr {
		t.Errorf("expected HTTP addr %q, got %q", expectedAddr, srv.httpServer.Addr)
	}
}

func TestServer_RunAndShutdown(t *testing.T) {
	// Use high ports to avoid conflicts
	cfg := testServerConfig()
	cfg.Server.HTTPPort = 18080
	cfg.Server.GRPCPort = 19090

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Run server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run()
	}()

	// Give servers time to start
	time.Sleep(100 * time.Millisecond)

	// Verify HTTP server is running
	resp, err := http.Get("http://localhost:18080/health")
	if err != nil {
		t.Fatalf("failed to connect to HTTP server: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Trigger shutdown by calling shutdown directly
	if err := srv.shutdown(); err != nil {
		t.Errorf("shutdown error: %v", err)
	}
}

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

	"google.golang.org/grpc"
)

// Server is the main central service that runs both HTTP and gRPC servers.
type Server struct {
	config     *Config
	httpServer *http.Server
	grpcServer *grpc.Server
}

// NewServer creates a new central server with the given configuration.
func NewServer(cfg *Config) *Server {
	httpHandler := NewHTTPServer()
	grpcHandler := NewGRPCServer()

	grpcSrv := grpc.NewServer()
	grpcHandler.RegisterWithServer(grpcSrv)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           httpHandler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &Server{
		config:     cfg,
		httpServer: httpSrv,
		grpcServer: grpcSrv,
	}
}

// Run starts both HTTP and gRPC servers and handles graceful shutdown.
func (s *Server) Run() error {
	errCh := make(chan error, 2)

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

	return nil
}

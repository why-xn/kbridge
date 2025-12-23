package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/why-xn/mk8s/internal/agent"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid config: %v", err)
	}

	log.Printf("mk8s-agent starting...")

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	a := agent.New(cfg)

	// Run agent in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Run(ctx)
	}()

	// Wait for shutdown signal or error
	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
		a.Stop()
	case err := <-errCh:
		if err != nil {
			log.Fatalf("Agent error: %v", err)
		}
	}

	log.Println("mk8s-agent stopped")
}

func loadConfig(configPath string) (*agent.Config, error) {
	if configPath != "" {
		return agent.LoadConfig(configPath)
	}

	// Try default config paths
	defaultPaths := []string{
		"configs/agent.yaml",
		"/etc/mk8s/agent.yaml",
	}

	for _, path := range defaultPaths {
		if _, err := os.Stat(path); err == nil {
			return agent.LoadConfig(path)
		}
	}

	// Use defaults if no config file found
	log.Println("No config file found, using defaults")
	return agent.DefaultConfig(), nil
}

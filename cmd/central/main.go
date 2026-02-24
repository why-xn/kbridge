package main

import (
	"flag"
	"log"
	"os"

	"github.com/why-xn/kbridge/internal/central"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	log.Println("kbridge-central starting...")

	server, err := central.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func loadConfig(path string) (*central.Config, error) {
	if path == "" {
		path = os.Getenv("KBRIDGE_CONFIG")
	}

	if path == "" {
		log.Println("No config file specified, using defaults")
		return central.DefaultConfig(), nil
	}

	return central.LoadConfig(path)
}

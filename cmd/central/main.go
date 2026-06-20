package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/why-xn/kbridge/internal/central"
	"github.com/why-xn/kbridge/internal/version"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("kbridge-central " + version.String())
		return
	}

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
		log.Println("No config file specified, using defaults with environment overrides")
		return central.DefaultConfigWithEnv(), nil
	}

	return central.LoadConfig(path)
}

// Package agent provides the kbridge agent implementation that connects to the central service.
package agent

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration for the agent.
type Config struct {
	Central CentralConfig `yaml:"central"`
	Cluster ClusterConfig `yaml:"cluster"`
}

// CentralConfig holds the central service connection configuration.
type CentralConfig struct {
	URL   string         `yaml:"url"`
	Token string         `yaml:"token"`
	TLS   AgentTLSConfig `yaml:"tls"`
}

// AgentTLSConfig configures the agent's gRPC client transport security.
//   - Enabled=false: plaintext.
//   - Enabled, Insecure=true: TLS without verifying the server certificate.
//   - Enabled, CAFile set: TLS verifying the server against the given CA.
//   - Enabled, CAFile empty: TLS verifying against system root CAs.
type AgentTLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CAFile   string `yaml:"ca_file"`
	Insecure bool   `yaml:"insecure"`
}

// ClusterConfig holds the cluster identification configuration.
type ClusterConfig struct {
	Name string `yaml:"name"`
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() *Config {
	return &Config{
		Central: CentralConfig{
			URL:   "localhost:9090",
			Token: "",
		},
		Cluster: ClusterConfig{
			Name: "default",
		},
	}
}

// DefaultConfigWithEnv returns a Config with defaults and environment variable overrides applied.
func DefaultConfigWithEnv() *Config {
	cfg := DefaultConfig()
	applyEnvOverrides(cfg)
	return cfg
}

// LoadConfig reads configuration from a YAML file at the given path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Apply environment variable overrides
	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides applies environment variable overrides to the config.
func applyEnvOverrides(cfg *Config) {
	if url := os.Getenv("KBRIDGE_CENTRAL_URL"); url != "" {
		cfg.Central.URL = url
	}
	if token := os.Getenv("KBRIDGE_AGENT_TOKEN"); token != "" {
		cfg.Central.Token = token
	}
	// Also support AGENT_TOKEN for backwards compatibility
	if token := os.Getenv("AGENT_TOKEN"); token != "" && cfg.Central.Token == "" {
		cfg.Central.Token = token
	}
	if name := os.Getenv("KBRIDGE_CLUSTER_NAME"); name != "" {
		cfg.Cluster.Name = name
	}
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.Central.URL == "" {
		return fmt.Errorf("central.url is required")
	}
	if c.Central.Token == "" {
		return fmt.Errorf("central.token is required (set via config or AGENT_TOKEN env var)")
	}
	if c.Cluster.Name == "" {
		return fmt.Errorf("cluster.name is required")
	}
	return nil
}

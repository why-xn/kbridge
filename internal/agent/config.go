// Package agent provides the kbridge agent implementation that connects to the central service.
package agent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration for the agent.
type Config struct {
	Central CentralConfig `yaml:"central"`
	Cluster ClusterConfig `yaml:"cluster"`
}

// CentralConfig holds the central service connection configuration.
type CentralConfig struct {
	URL       string         `yaml:"url"`
	Token     string         `yaml:"token"`
	TokenFile string         `yaml:"token_file"`
	TLS       AgentTLSConfig `yaml:"tls"`
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
	// Ignore error: no YAML file-based secrets to fail on; env FILE vars are allowed.
	_ = cfg.resolveSecrets()
	applyEnvOverrides(cfg)
	return cfg
}

// resolveSecret returns a secret from (highest precedence first): the file named
// by the <envName>_FILE env var, the <envName> env var, the fileVal YAML path,
// then the inline literal. File contents are trimmed. A non-empty file path that
// cannot be read is a fatal error (fail-closed).
func resolveSecret(inlineVal, fileVal, envName string) (string, error) {
	if p := os.Getenv(envName + "_FILE"); p != "" {
		return readSecretFile(p)
	}
	if v := os.Getenv(envName); v != "" {
		return v, nil
	}
	if fileVal != "" {
		return readSecretFile(fileVal)
	}
	return inlineVal, nil
}

func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading secret file %q: %w", path, err)
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "", fmt.Errorf("secret file %q is empty", path)
	}
	return s, nil
}

// resolveSecrets resolves the agent token from file/env/inline sources.
// Precedence: KBRIDGE_AGENT_TOKEN_FILE > KBRIDGE_AGENT_TOKEN > AGENT_TOKEN > token_file > inline.
func (c *Config) resolveSecrets() error {
	// Try KBRIDGE_AGENT_TOKEN_FILE first, then KBRIDGE_AGENT_TOKEN, then AGENT_TOKEN, then token_file, then inline.
	if p := os.Getenv("KBRIDGE_AGENT_TOKEN_FILE"); p != "" {
		v, err := readSecretFile(p)
		if err != nil {
			return err
		}
		c.Central.Token = v
		return nil
	}
	token, err := resolveSecret(c.Central.Token, c.Central.TokenFile, "KBRIDGE_AGENT_TOKEN")
	if err != nil {
		return err
	}
	if token != "" {
		c.Central.Token = token
		return nil
	}
	// Backwards-compatible AGENT_TOKEN fallback.
	if v := os.Getenv("AGENT_TOKEN"); v != "" {
		c.Central.Token = v
	}
	return nil
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

	if err := cfg.resolveSecrets(); err != nil {
		return nil, fmt.Errorf("resolving secrets: %w", err)
	}

	// Apply environment variable overrides (non-secret fields).
	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides applies non-secret environment variable overrides to the config.
func applyEnvOverrides(cfg *Config) {
	if url := os.Getenv("KBRIDGE_CENTRAL_URL"); url != "" {
		cfg.Central.URL = url
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

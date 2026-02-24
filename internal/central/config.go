// Package central provides the central service implementation for kbridge.
// It includes HTTP REST API for CLI communication and gRPC server for agent communication.
package central

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration for the central service.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Audit    AuditConfig    `yaml:"audit"`
}

// ServerConfig holds the server-related configuration.
type ServerConfig struct {
	HTTPPort int `yaml:"http_port"`
	GRPCPort int `yaml:"grpc_port"`
}

// DatabaseConfig holds the database connection configuration.
type DatabaseConfig struct {
	Driver string `yaml:"driver"` // "sqlite" or "postgres"
	Path   string `yaml:"path"`   // SQLite file path
}

// AuthConfig holds the authentication configuration.
type AuthConfig struct {
	JWTSecret            string        `yaml:"jwt_secret"`
	AccessTokenExpiryStr string        `yaml:"access_token_expiry"`
	AccessTokenExpiry    time.Duration `yaml:"-"`
	RefreshTokenExpiryStr string       `yaml:"refresh_token_expiry"`
	RefreshTokenExpiry   time.Duration `yaml:"-"`
	AdminEmail           string        `yaml:"admin_email"`
	AdminPassword        string        `yaml:"admin_password"`
	AdminName            string        `yaml:"admin_name"`
}

// AuditConfig holds the audit log configuration.
type AuditConfig struct {
	RetentionDays      int           `yaml:"retention_days"`
	CleanupIntervalStr string        `yaml:"cleanup_interval"`
	CleanupInterval    time.Duration `yaml:"-"`
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			HTTPPort: 8080,
			GRPCPort: 9090,
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			Path:   "kbridge.db",
		},
		Auth: AuthConfig{
			AccessTokenExpiryStr:  "24h",
			AccessTokenExpiry:     24 * time.Hour,
			RefreshTokenExpiryStr: "168h",
			RefreshTokenExpiry:    168 * time.Hour,
		},
		Audit: AuditConfig{
			RetentionDays:      90,
			CleanupIntervalStr: "24h",
			CleanupInterval:    24 * time.Hour,
		},
	}
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

	if err := cfg.parseDurations(); err != nil {
		return nil, fmt.Errorf("parsing duration fields: %w", err)
	}

	return cfg, nil
}

// parseDurations parses all string-based duration fields into time.Duration.
func (c *Config) parseDurations() error {
	var err error
	if c.Auth.AccessTokenExpiryStr != "" {
		c.Auth.AccessTokenExpiry, err = time.ParseDuration(c.Auth.AccessTokenExpiryStr)
		if err != nil {
			return fmt.Errorf("invalid access_token_expiry %q: %w", c.Auth.AccessTokenExpiryStr, err)
		}
	}
	if c.Auth.RefreshTokenExpiryStr != "" {
		c.Auth.RefreshTokenExpiry, err = time.ParseDuration(c.Auth.RefreshTokenExpiryStr)
		if err != nil {
			return fmt.Errorf("invalid refresh_token_expiry %q: %w", c.Auth.RefreshTokenExpiryStr, err)
		}
	}
	if c.Audit.CleanupIntervalStr != "" {
		c.Audit.CleanupInterval, err = time.ParseDuration(c.Audit.CleanupIntervalStr)
		if err != nil {
			return fmt.Errorf("invalid cleanup_interval %q: %w", c.Audit.CleanupIntervalStr, err)
		}
	}
	return nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if err := c.validateServer(); err != nil {
		return err
	}
	if err := c.validateDatabase(); err != nil {
		return err
	}
	return c.validateAuth()
}

func (c *Config) validateServer() error {
	if c.Server.HTTPPort <= 0 || c.Server.HTTPPort > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", c.Server.HTTPPort)
	}
	if c.Server.GRPCPort <= 0 || c.Server.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.Server.GRPCPort)
	}
	if c.Server.HTTPPort == c.Server.GRPCPort {
		return fmt.Errorf("HTTP and gRPC ports must be different")
	}
	return nil
}

func (c *Config) validateDatabase() error {
	if c.Database.Driver != "sqlite" && c.Database.Driver != "postgres" {
		return fmt.Errorf("invalid database driver %q: must be sqlite or postgres", c.Database.Driver)
	}
	return nil
}

func (c *Config) validateAuth() error {
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("jwt_secret must not be empty")
	}
	if c.Auth.AccessTokenExpiry <= 0 {
		return fmt.Errorf("access_token_expiry must be greater than zero")
	}
	if c.Auth.RefreshTokenExpiry <= 0 {
		return fmt.Errorf("refresh_token_expiry must be greater than zero")
	}
	return nil
}

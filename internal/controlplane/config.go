// Package control plane provides the control plane implementation for kbridge.
// It includes HTTP REST API for CLI communication and gRPC server for agent communication.
package controlplane

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const devDefaultJWTSecret = "dev-secret-change-in-production!!"

// Config holds the complete configuration for the control plane.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Audit     AuditConfig     `yaml:"audit"`
	Bootstrap BootstrapConfig `yaml:"bootstrap"`
	RBAC      RBACConfig      `yaml:"rbac"`
	Grants    GrantsConfig    `yaml:"grants"`
	TLS       TLSConfig       `yaml:"tls"`
	Streams   StreamsConfig   `yaml:"streams"`
}

// TLSConfig configures TLS for the control plane HTTP and gRPC servers. When enabled,
// both servers present the same certificate.
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// StreamsConfig limits concurrent streaming sessions.
type StreamsConfig struct {
	MaxConcurrent int `yaml:"max_concurrent"`
}

// GrantsConfig bounds just-in-time access grants. MaxDuration is the ceiling on
// how long any single grant can run, so a request for a thousand hours is
// refused rather than quietly becoming standing access.
type GrantsConfig struct {
	MaxDurationStr     string        `yaml:"max_duration"`
	MaxDuration        time.Duration `yaml:"-"`
	DefaultDurationStr string        `yaml:"default_duration"`
	DefaultDuration    time.Duration `yaml:"-"`
	// AllowSelfApproval lets a requester approve their own grant. Off by
	// default: a second pair of eyes is the whole point.
	AllowSelfApproval bool `yaml:"allow_self_approval"`
}

// RBACConfig configures policy-file-based access control. When PolicyFile is
// empty, RBAC enforcement is disabled (all authenticated users are allowed).
type RBACConfig struct {
	PolicyFile string `yaml:"policy_file"`
}

// BootstrapConfig optionally seeds an agent token on startup for development
// and bootstrapping. When AgentToken is empty no token is seeded; in production
// tokens should be created via the admin API.
type BootstrapConfig struct {
	AgentToken   string `yaml:"agent_token"`
	AgentCluster string `yaml:"agent_cluster"`
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
	JWTSecret             string        `yaml:"jwt_secret"`
	JWTSecretFile         string        `yaml:"jwt_secret_file"`
	TokenPepper           string        `yaml:"token_pepper"`
	TokenPepperFile       string        `yaml:"token_pepper_file"`
	AccessTokenExpiryStr  string        `yaml:"access_token_expiry"`
	AccessTokenExpiry     time.Duration `yaml:"-"`
	RefreshTokenExpiryStr string        `yaml:"refresh_token_expiry"`
	RefreshTokenExpiry    time.Duration `yaml:"-"`
	AdminEmail            string        `yaml:"admin_email"`
	AdminPassword         string        `yaml:"admin_password"`
	AdminPasswordFile     string        `yaml:"admin_password_file"`
	AdminName             string        `yaml:"admin_name"`
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
			AccessTokenExpiryStr:  "1h",
			AccessTokenExpiry:     time.Hour,
			RefreshTokenExpiryStr: "168h",
			RefreshTokenExpiry:    168 * time.Hour,
		},
		Audit: AuditConfig{
			RetentionDays:      90,
			CleanupIntervalStr: "24h",
			CleanupInterval:    24 * time.Hour,
		},
		Streams: StreamsConfig{MaxConcurrent: 50},
		Grants: GrantsConfig{
			MaxDurationStr:     "8h",
			MaxDuration:        8 * time.Hour,
			DefaultDurationStr: "1h",
			DefaultDuration:    time.Hour,
		},
	}
}

// DefaultConfigWithEnv returns a Config with defaults and env-variable secret
// overrides applied. It mirrors agent.DefaultConfigWithEnv so control plane can be
// started with no config file when secrets are injected via KBRIDGE_JWT_SECRET
// (or _FILE) and friends.
func DefaultConfigWithEnv() *Config {
	cfg := DefaultConfig()
	// Ignore error: no YAML file paths set, only env/inline sources in scope.
	_ = cfg.resolveSecrets()
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

	if err := cfg.parseDurations(); err != nil {
		return nil, fmt.Errorf("parsing duration fields: %w", err)
	}

	if err := cfg.resolveSecrets(); err != nil {
		return nil, fmt.Errorf("resolving secrets: %w", err)
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
	if c.Grants.MaxDurationStr != "" {
		c.Grants.MaxDuration, err = time.ParseDuration(c.Grants.MaxDurationStr)
		if err != nil {
			return fmt.Errorf("invalid grants.max_duration %q: %w", c.Grants.MaxDurationStr, err)
		}
	}
	if c.Grants.DefaultDurationStr != "" {
		c.Grants.DefaultDuration, err = time.ParseDuration(c.Grants.DefaultDurationStr)
		if err != nil {
			return fmt.Errorf("invalid grants.default_duration %q: %w", c.Grants.DefaultDurationStr, err)
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
	if err := c.validateAuth(); err != nil {
		return err
	}
	if err := c.validateGrants(); err != nil {
		return err
	}
	return c.validateTLS()
}

// Built-in grant bounds, used when a config leaves them unset.
const (
	defaultGrantMaxDuration     = 8 * time.Hour
	defaultGrantDefaultDuration = time.Hour
)

// EffectiveMaxDuration is the ceiling on a grant's window. Zero means unset, so
// a config that predates just-in-time access (or a hand-built one) still gets
// a sane bound rather than being rejected.
func (g GrantsConfig) EffectiveMaxDuration() time.Duration {
	if g.MaxDuration <= 0 {
		return defaultGrantMaxDuration
	}
	return g.MaxDuration
}

// EffectiveDefaultDuration is the window used when a request names none.
func (g GrantsConfig) EffectiveDefaultDuration() time.Duration {
	if g.DefaultDuration <= 0 {
		return defaultGrantDefaultDuration
	}
	return g.DefaultDuration
}

// validateGrants rejects bounds that are self-contradictory. Unset (zero) is
// fine and falls back to the built-in defaults; only a negative value or a
// default past the ceiling is an error.
func (c *Config) validateGrants() error {
	if c.Grants.MaxDuration < 0 {
		return fmt.Errorf("grants.max_duration must not be negative, got %s", c.Grants.MaxDuration)
	}
	if c.Grants.DefaultDuration < 0 {
		return fmt.Errorf("grants.default_duration must not be negative, got %s", c.Grants.DefaultDuration)
	}
	if c.Grants.EffectiveDefaultDuration() > c.Grants.EffectiveMaxDuration() {
		return fmt.Errorf("grants.default_duration (%s) exceeds grants.max_duration (%s)",
			c.Grants.EffectiveDefaultDuration(), c.Grants.EffectiveMaxDuration())
	}
	return nil
}

func (c *Config) validateTLS() error {
	if !c.TLS.Enabled {
		return nil
	}
	if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
		return fmt.Errorf("tls.cert_file and tls.key_file are required when tls is enabled")
	}
	return nil
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
	if err := c.validateSecrets(); err != nil {
		return err
	}
	if c.Auth.AccessTokenExpiry <= 0 {
		return fmt.Errorf("access_token_expiry must be greater than zero")
	}
	if c.Auth.RefreshTokenExpiry <= 0 {
		return fmt.Errorf("refresh_token_expiry must be greater than zero")
	}
	return nil
}

func (c *Config) validateSecrets() error {
	switch {
	case c.Auth.JWTSecret == "":
		return fmt.Errorf("jwt_secret must be set")
	case len(c.Auth.JWTSecret) < 32:
		return fmt.Errorf("jwt_secret must be at least 32 characters")
	case c.Auth.JWTSecret == devDefaultJWTSecret:
		return fmt.Errorf("jwt_secret is the shipped development default; generate one with: openssl rand -hex 32")
	}
	if c.Auth.AdminPassword == "admin123" || (c.Auth.AdminPassword != "" && len(c.Auth.AdminPassword) < 8) {
		slog.Warn("admin_password is weak or a known default; change it after first login")
	}
	return nil
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

// resolveSecrets resolves every sensitive field from file/env/inline sources.
func (c *Config) resolveSecrets() error {
	var err error
	if c.Auth.JWTSecret, err = resolveSecret(c.Auth.JWTSecret, c.Auth.JWTSecretFile, "KBRIDGE_JWT_SECRET"); err != nil {
		return err
	}
	if c.Auth.TokenPepper, err = resolveSecret(c.Auth.TokenPepper, c.Auth.TokenPepperFile, "KBRIDGE_TOKEN_PEPPER"); err != nil {
		return err
	}
	if c.Auth.AdminPassword, err = resolveSecret(c.Auth.AdminPassword, c.Auth.AdminPasswordFile, "KBRIDGE_ADMIN_PASSWORD"); err != nil {
		return err
	}
	return nil
}

// AgentTokenPepper returns the secret used to HMAC agent tokens at rest. A
// dedicated token_pepper is preferred for key separation; when unset it falls
// back to jwt_secret, which Validate already requires to be non-empty.
func (c *Config) AgentTokenPepper() string {
	if c.Auth.TokenPepper != "" {
		return c.Auth.TokenPepper
	}
	return c.Auth.JWTSecret
}

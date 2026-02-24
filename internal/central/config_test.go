package central

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// validConfig returns a fully valid Config for use in tests.
func validConfig() Config {
	return Config{
		Server:   ServerConfig{HTTPPort: 8080, GRPCPort: 9090},
		Database: DatabaseConfig{Driver: "sqlite", Path: "kbridge.db"},
		Auth: AuthConfig{
			JWTSecret:          "test-secret",
			AccessTokenExpiry:  24 * time.Hour,
			RefreshTokenExpiry: 168 * time.Hour,
		},
		Audit: AuditConfig{
			RetentionDays:   90,
			CleanupInterval: 24 * time.Hour,
		},
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.HTTPPort != 8080 {
		t.Errorf("expected HTTPPort=8080, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Server.GRPCPort != 9090 {
		t.Errorf("expected GRPCPort=9090, got %d", cfg.Server.GRPCPort)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("expected Driver=sqlite, got %s", cfg.Database.Driver)
	}
	if cfg.Database.Path != "kbridge.db" {
		t.Errorf("expected Path=kbridge.db, got %s", cfg.Database.Path)
	}
	if cfg.Auth.AccessTokenExpiry != 24*time.Hour {
		t.Errorf("expected AccessTokenExpiry=24h, got %v", cfg.Auth.AccessTokenExpiry)
	}
	if cfg.Auth.RefreshTokenExpiry != 168*time.Hour {
		t.Errorf("expected RefreshTokenExpiry=168h, got %v", cfg.Auth.RefreshTokenExpiry)
	}
	if cfg.Audit.RetentionDays != 90 {
		t.Errorf("expected RetentionDays=90, got %d", cfg.Audit.RetentionDays)
	}
	if cfg.Audit.CleanupInterval != 24*time.Hour {
		t.Errorf("expected CleanupInterval=24h, got %v", cfg.Audit.CleanupInterval)
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantHTTP int
		wantGRPC int
		wantErr  bool
	}{
		{
			name: "valid config",
			content: `server:
  http_port: 8081
  grpc_port: 9091`,
			wantHTTP: 8081,
			wantGRPC: 9091,
			wantErr:  false,
		},
		{
			name: "partial config uses defaults",
			content: `server:
  http_port: 8082`,
			wantHTTP: 8082,
			wantGRPC: 9090,
			wantErr:  false,
		},
		{
			name:     "empty config uses defaults",
			content:  "",
			wantHTTP: 8080,
			wantGRPC: 9090,
			wantErr:  false,
		},
		{
			name:     "invalid yaml",
			content:  "server:\n  http_port: [invalid",
			wantHTTP: 0,
			wantGRPC: 0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if err := os.WriteFile(configPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test config: %v", err)
			}

			cfg, err := LoadConfig(configPath)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.Server.HTTPPort != tt.wantHTTP {
				t.Errorf("expected HTTPPort=%d, got %d", tt.wantHTTP, cfg.Server.HTTPPort)
			}
			if cfg.Server.GRPCPort != tt.wantGRPC {
				t.Errorf("expected GRPCPort=%d, got %d", tt.wantGRPC, cfg.Server.GRPCPort)
			}
		})
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestLoadConfig_WithDatabaseAndAuth(t *testing.T) {
	tests := []struct {
		name                   string
		content                string
		wantDriver             string
		wantPath               string
		wantJWTSecret          string
		wantAccessTokenExpiry  time.Duration
		wantRefreshTokenExpiry time.Duration
		wantAdminEmail         string
		wantAdminPassword      string
		wantAdminName          string
		wantRetentionDays      int
		wantCleanupInterval    time.Duration
		wantErr                bool
	}{
		{
			name: "full config with all sections",
			content: `server:
  http_port: 8080
  grpc_port: 9090
database:
  driver: postgres
  path: /var/lib/kbridge/data.db
auth:
  jwt_secret: my-secret-key
  access_token_expiry: "1h"
  refresh_token_expiry: "72h"
  admin_email: admin@example.com
  admin_password: secret123
  admin_name: Admin User
audit:
  retention_days: 30
  cleanup_interval: "12h"`,
			wantDriver:             "postgres",
			wantPath:               "/var/lib/kbridge/data.db",
			wantJWTSecret:          "my-secret-key",
			wantAccessTokenExpiry:  1 * time.Hour,
			wantRefreshTokenExpiry: 72 * time.Hour,
			wantAdminEmail:         "admin@example.com",
			wantAdminPassword:      "secret123",
			wantAdminName:          "Admin User",
			wantRetentionDays:      30,
			wantCleanupInterval:    12 * time.Hour,
			wantErr:                false,
		},
		{
			name:                   "missing sections use defaults",
			content:                ``,
			wantDriver:             "sqlite",
			wantPath:               "kbridge.db",
			wantJWTSecret:          "",
			wantAccessTokenExpiry:  24 * time.Hour,
			wantRefreshTokenExpiry: 168 * time.Hour,
			wantAdminEmail:         "",
			wantAdminPassword:      "",
			wantAdminName:          "",
			wantRetentionDays:      90,
			wantCleanupInterval:    24 * time.Hour,
			wantErr:                false,
		},
		{
			name: "invalid access token expiry",
			content: `auth:
  jwt_secret: secret
  access_token_expiry: "not-a-duration"`,
			wantErr: true,
		},
		{
			name: "invalid refresh token expiry",
			content: `auth:
  jwt_secret: secret
  refresh_token_expiry: "bad"`,
			wantErr: true,
		},
		{
			name: "invalid cleanup interval",
			content: `audit:
  cleanup_interval: "bad"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")

			if err := os.WriteFile(configPath, []byte(tt.content), 0644); err != nil {
				t.Fatalf("failed to write test config: %v", err)
			}

			cfg, err := LoadConfig(configPath)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.Database.Driver != tt.wantDriver {
				t.Errorf("expected Driver=%s, got %s", tt.wantDriver, cfg.Database.Driver)
			}
			if cfg.Database.Path != tt.wantPath {
				t.Errorf("expected Path=%s, got %s", tt.wantPath, cfg.Database.Path)
			}
			if cfg.Auth.JWTSecret != tt.wantJWTSecret {
				t.Errorf("expected JWTSecret=%s, got %s", tt.wantJWTSecret, cfg.Auth.JWTSecret)
			}
			if cfg.Auth.AccessTokenExpiry != tt.wantAccessTokenExpiry {
				t.Errorf("expected AccessTokenExpiry=%v, got %v", tt.wantAccessTokenExpiry, cfg.Auth.AccessTokenExpiry)
			}
			if cfg.Auth.RefreshTokenExpiry != tt.wantRefreshTokenExpiry {
				t.Errorf("expected RefreshTokenExpiry=%v, got %v", tt.wantRefreshTokenExpiry, cfg.Auth.RefreshTokenExpiry)
			}
			if cfg.Auth.AdminEmail != tt.wantAdminEmail {
				t.Errorf("expected AdminEmail=%s, got %s", tt.wantAdminEmail, cfg.Auth.AdminEmail)
			}
			if cfg.Auth.AdminPassword != tt.wantAdminPassword {
				t.Errorf("expected AdminPassword=%s, got %s", tt.wantAdminPassword, cfg.Auth.AdminPassword)
			}
			if cfg.Auth.AdminName != tt.wantAdminName {
				t.Errorf("expected AdminName=%s, got %s", tt.wantAdminName, cfg.Auth.AdminName)
			}
			if cfg.Audit.RetentionDays != tt.wantRetentionDays {
				t.Errorf("expected RetentionDays=%d, got %d", tt.wantRetentionDays, cfg.Audit.RetentionDays)
			}
			if cfg.Audit.CleanupInterval != tt.wantCleanupInterval {
				t.Errorf("expected CleanupInterval=%v, got %v", tt.wantCleanupInterval, cfg.Audit.CleanupInterval)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "invalid HTTP port - zero",
			modify:  func(c *Config) { c.Server.HTTPPort = 0 },
			wantErr: true,
		},
		{
			name:    "invalid HTTP port - negative",
			modify:  func(c *Config) { c.Server.HTTPPort = -1 },
			wantErr: true,
		},
		{
			name:    "invalid HTTP port - too high",
			modify:  func(c *Config) { c.Server.HTTPPort = 65536 },
			wantErr: true,
		},
		{
			name:    "invalid gRPC port - zero",
			modify:  func(c *Config) { c.Server.GRPCPort = 0 },
			wantErr: true,
		},
		{
			name:    "same port for HTTP and gRPC",
			modify:  func(c *Config) { c.Server.GRPCPort = 8080 },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestConfig_Validate_Database(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid full config",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "invalid database driver",
			modify:  func(c *Config) { c.Database.Driver = "mysql" },
			wantErr: true,
		},
		{
			name:    "empty database driver",
			modify:  func(c *Config) { c.Database.Driver = "" },
			wantErr: true,
		},
		{
			name:    "postgres driver is valid",
			modify:  func(c *Config) { c.Database.Driver = "postgres" },
			wantErr: false,
		},
		{
			name:    "empty jwt secret",
			modify:  func(c *Config) { c.Auth.JWTSecret = "" },
			wantErr: true,
		},
		{
			name:    "zero access token expiry",
			modify:  func(c *Config) { c.Auth.AccessTokenExpiry = 0 },
			wantErr: true,
		},
		{
			name:    "negative access token expiry",
			modify:  func(c *Config) { c.Auth.AccessTokenExpiry = -1 * time.Hour },
			wantErr: true,
		},
		{
			name:    "zero refresh token expiry",
			modify:  func(c *Config) { c.Auth.RefreshTokenExpiry = 0 },
			wantErr: true,
		},
		{
			name:    "negative refresh token expiry",
			modify:  func(c *Config) { c.Auth.RefreshTokenExpiry = -1 * time.Hour },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

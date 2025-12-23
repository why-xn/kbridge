package central

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.HTTPPort != 8080 {
		t.Errorf("expected HTTPPort=8080, got %d", cfg.Server.HTTPPort)
	}
	if cfg.Server.GRPCPort != 9090 {
		t.Errorf("expected GRPCPort=9090, got %d", cfg.Server.GRPCPort)
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

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				Server: ServerConfig{HTTPPort: 8080, GRPCPort: 9090},
			},
			wantErr: false,
		},
		{
			name: "invalid HTTP port - zero",
			config: Config{
				Server: ServerConfig{HTTPPort: 0, GRPCPort: 9090},
			},
			wantErr: true,
		},
		{
			name: "invalid HTTP port - negative",
			config: Config{
				Server: ServerConfig{HTTPPort: -1, GRPCPort: 9090},
			},
			wantErr: true,
		},
		{
			name: "invalid HTTP port - too high",
			config: Config{
				Server: ServerConfig{HTTPPort: 65536, GRPCPort: 9090},
			},
			wantErr: true,
		},
		{
			name: "invalid gRPC port - zero",
			config: Config{
				Server: ServerConfig{HTTPPort: 8080, GRPCPort: 0},
			},
			wantErr: true,
		},
		{
			name: "same port for HTTP and gRPC",
			config: Config{
				Server: ServerConfig{HTTPPort: 8080, GRPCPort: 8080},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

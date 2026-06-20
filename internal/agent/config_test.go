package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSecret(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "s")
	if err := os.WriteFile(secretFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("file env wins and is trimmed", func(t *testing.T) {
		t.Setenv("X_SECRET_FILE", secretFile)
		t.Setenv("X_SECRET", "from-env")
		got, err := resolveSecret("from-inline", "ignored-file-yaml", "X_SECRET")
		if err != nil || got != "from-file" {
			t.Fatalf("got %q err %v, want from-file", got, err)
		}
	})
	t.Run("literal env beats yaml", func(t *testing.T) {
		t.Setenv("X_SECRET", "from-env")
		got, _ := resolveSecret("from-inline", "", "X_SECRET")
		if got != "from-env" {
			t.Fatalf("got %q want from-env", got)
		}
	})
	t.Run("yaml file beats inline", func(t *testing.T) {
		got, _ := resolveSecret("from-inline", secretFile, "X_SECRET_UNSET")
		if got != "from-file" {
			t.Fatalf("got %q want from-file", got)
		}
	})
	t.Run("inline fallback", func(t *testing.T) {
		got, _ := resolveSecret("from-inline", "", "X_SECRET_UNSET")
		if got != "from-inline" {
			t.Fatalf("got %q want from-inline", got)
		}
	})
	t.Run("missing file path is fatal", func(t *testing.T) {
		_, err := resolveSecret("", filepath.Join(dir, "nope"), "X_SECRET_UNSET")
		if err == nil {
			t.Fatal("expected error for missing secret file")
		}
	})
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	if cfg.Central.URL != "localhost:9090" {
		t.Errorf("expected default URL 'localhost:9090', got %q", cfg.Central.URL)
	}

	if cfg.Cluster.Name != "default" {
		t.Errorf("expected default cluster name 'default', got %q", cfg.Cluster.Name)
	}

}

func TestLoadConfig(t *testing.T) {
	// Create temp config file
	content := `
central:
  url: central.example.com:9090
  token: test-token
cluster:
  name: production
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Central.URL != "central.example.com:9090" {
		t.Errorf("expected URL 'central.example.com:9090', got %q", cfg.Central.URL)
	}

	if cfg.Central.Token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", cfg.Central.Token)
	}

	if cfg.Cluster.Name != "production" {
		t.Errorf("expected cluster name 'production', got %q", cfg.Cluster.Name)
	}
}

func TestLoadConfig_NonExistent(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")
	if err := os.WriteFile(configPath, []byte("invalid: yaml: content"), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadConfig_EnvVar(t *testing.T) {
	// Set environment variable
	os.Setenv("AGENT_TOKEN", "env-token")
	defer os.Unsetenv("AGENT_TOKEN")

	// Create config without token
	content := `
central:
  url: localhost:9090
cluster:
  name: test
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Central.Token != "env-token" {
		t.Errorf("expected token from env 'env-token', got %q", cfg.Central.Token)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Central: CentralConfig{URL: "localhost:9090", Token: "token"},
				Cluster: ClusterConfig{Name: "test"},
			},
			wantErr: false,
		},
		{
			name: "missing URL",
			config: &Config{
				Central: CentralConfig{URL: "", Token: "token"},
				Cluster: ClusterConfig{Name: "test"},
			},
			wantErr: true,
		},
		{
			name: "missing token",
			config: &Config{
				Central: CentralConfig{URL: "localhost:9090", Token: ""},
				Cluster: ClusterConfig{Name: "test"},
			},
			wantErr: true,
		},
		{
			name: "missing cluster name",
			config: &Config{
				Central: CentralConfig{URL: "localhost:9090", Token: "token"},
				Cluster: ClusterConfig{Name: ""},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultConfigWithEnv(t *testing.T) {
	// Set environment variables
	os.Setenv("KBRIDGE_CENTRAL_URL", "central.example.com:9090")
	os.Setenv("KBRIDGE_AGENT_TOKEN", "env-agent-token")
	os.Setenv("KBRIDGE_CLUSTER_NAME", "env-cluster")
	defer func() {
		os.Unsetenv("KBRIDGE_CENTRAL_URL")
		os.Unsetenv("KBRIDGE_AGENT_TOKEN")
		os.Unsetenv("KBRIDGE_CLUSTER_NAME")
	}()

	cfg := DefaultConfigWithEnv()

	if cfg.Central.URL != "central.example.com:9090" {
		t.Errorf("expected URL 'central.example.com:9090', got %q", cfg.Central.URL)
	}

	if cfg.Central.Token != "env-agent-token" {
		t.Errorf("expected token 'env-agent-token', got %q", cfg.Central.Token)
	}

	if cfg.Cluster.Name != "env-cluster" {
		t.Errorf("expected cluster name 'env-cluster', got %q", cfg.Cluster.Name)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	// Set environment variable that should override config file
	os.Setenv("KBRIDGE_CENTRAL_URL", "override.example.com:9090")
	os.Setenv("KBRIDGE_CLUSTER_NAME", "override-cluster")
	defer func() {
		os.Unsetenv("KBRIDGE_CENTRAL_URL")
		os.Unsetenv("KBRIDGE_CLUSTER_NAME")
	}()

	// Create config file
	content := `
central:
  url: original.example.com:9090
  token: file-token
cluster:
  name: file-cluster
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Environment variables should override file values
	if cfg.Central.URL != "override.example.com:9090" {
		t.Errorf("expected URL to be overridden to 'override.example.com:9090', got %q", cfg.Central.URL)
	}

	if cfg.Cluster.Name != "override-cluster" {
		t.Errorf("expected cluster name to be overridden to 'override-cluster', got %q", cfg.Cluster.Name)
	}

	// Token should still come from file since not overridden
	if cfg.Central.Token != "file-token" {
		t.Errorf("expected token 'file-token', got %q", cfg.Central.Token)
	}
}

func TestLoadConfig_KBRIDGEAgentTokenOverridesAgentToken(t *testing.T) {
	// KBRIDGE_AGENT_TOKEN should take precedence over AGENT_TOKEN
	os.Setenv("AGENT_TOKEN", "legacy-token")
	os.Setenv("KBRIDGE_AGENT_TOKEN", "new-token")
	defer func() {
		os.Unsetenv("AGENT_TOKEN")
		os.Unsetenv("KBRIDGE_AGENT_TOKEN")
	}()

	// Create minimal config file
	content := `
central:
  url: localhost:9090
cluster:
  name: test
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "agent.yaml")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// KBRIDGE_AGENT_TOKEN should win
	if cfg.Central.Token != "new-token" {
		t.Errorf("expected token 'new-token', got %q", cfg.Central.Token)
	}
}

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

	if cfg.ControlPlane.URL != "localhost:9090" {
		t.Errorf("expected default URL 'localhost:9090', got %q", cfg.ControlPlane.URL)
	}

	if cfg.Cluster.Name != "default" {
		t.Errorf("expected default cluster name 'default', got %q", cfg.Cluster.Name)
	}

}

func TestLoadConfig(t *testing.T) {
	// Create temp config file
	content := `
control_plane:
  url: control-plane.example.com:9090
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

	if cfg.ControlPlane.URL != "control-plane.example.com:9090" {
		t.Errorf("expected URL 'control-plane.example.com:9090', got %q", cfg.ControlPlane.URL)
	}

	if cfg.ControlPlane.Token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", cfg.ControlPlane.Token)
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
control_plane:
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

	if cfg.ControlPlane.Token != "env-token" {
		t.Errorf("expected token from env 'env-token', got %q", cfg.ControlPlane.Token)
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
				ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: "token"},
				Cluster: ClusterConfig{Name: "test"},
				Kubernetes: KubernetesConfig{Server: "https://k", TokenFile: "/t"},
				Sync:         SyncConfig{Interval: time.Second},
			},
			wantErr: false,
		},
		{
			name: "kubeconfig needs no server or token file",
			config: &Config{
				ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   KubernetesConfig{Kubeconfig: "/kc"},
				Sync:         SyncConfig{Interval: time.Second},
			},
		},
		{
			name: "zero sync interval",
			config: &Config{
				ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: "token"},
				Cluster:      ClusterConfig{Name: "test"},
				Kubernetes:   KubernetesConfig{Kubeconfig: "/kc"},
			},
			wantErr: true,
		},
		{
			name: "missing URL",
			config: &Config{
				ControlPlane: ControlPlaneConfig{URL: "", Token: "token"},
				Cluster: ClusterConfig{Name: "test"},
			},
			wantErr: true,
		},
		{
			name: "missing token",
			config: &Config{
				ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: ""},
				Cluster: ClusterConfig{Name: "test"},
			},
			wantErr: true,
		},
		{
			name: "missing cluster name",
			config: &Config{
				ControlPlane: ControlPlaneConfig{URL: "localhost:9090", Token: "token"},
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
	os.Setenv("KBRIDGE_CONTROL_PLANE_URL", "control-plane.example.com:9090")
	os.Setenv("KBRIDGE_AGENT_TOKEN", "env-agent-token")
	os.Setenv("KBRIDGE_CLUSTER_NAME", "env-cluster")
	defer func() {
		os.Unsetenv("KBRIDGE_CONTROL_PLANE_URL")
		os.Unsetenv("KBRIDGE_AGENT_TOKEN")
		os.Unsetenv("KBRIDGE_CLUSTER_NAME")
	}()

	cfg := DefaultConfigWithEnv()

	if cfg.ControlPlane.URL != "control-plane.example.com:9090" {
		t.Errorf("expected URL 'control-plane.example.com:9090', got %q", cfg.ControlPlane.URL)
	}

	if cfg.ControlPlane.Token != "env-agent-token" {
		t.Errorf("expected token 'env-agent-token', got %q", cfg.ControlPlane.Token)
	}

	if cfg.Cluster.Name != "env-cluster" {
		t.Errorf("expected cluster name 'env-cluster', got %q", cfg.Cluster.Name)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	// Set environment variable that should override config file
	os.Setenv("KBRIDGE_CONTROL_PLANE_URL", "override.example.com:9090")
	os.Setenv("KBRIDGE_CLUSTER_NAME", "override-cluster")
	defer func() {
		os.Unsetenv("KBRIDGE_CONTROL_PLANE_URL")
		os.Unsetenv("KBRIDGE_CLUSTER_NAME")
	}()

	// Create config file
	content := `
control_plane:
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
	if cfg.ControlPlane.URL != "override.example.com:9090" {
		t.Errorf("expected URL to be overridden to 'override.example.com:9090', got %q", cfg.ControlPlane.URL)
	}

	if cfg.Cluster.Name != "override-cluster" {
		t.Errorf("expected cluster name to be overridden to 'override-cluster', got %q", cfg.Cluster.Name)
	}

	// Token should still come from file since not overridden
	if cfg.ControlPlane.Token != "file-token" {
		t.Errorf("expected token 'file-token', got %q", cfg.ControlPlane.Token)
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
control_plane:
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
	if cfg.ControlPlane.Token != "new-token" {
		t.Errorf("expected token 'new-token', got %q", cfg.ControlPlane.Token)
	}
}

func TestKubernetesConfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	k := DefaultConfig().Kubernetes
	want := KubernetesConfig{Server: "https://10.0.0.1:443", CAFile: "/var/run/kbridge/sa/ca.crt", TokenFile: "/var/run/kbridge/sa/token"}
	if k != want {
		t.Fatalf("defaults = %+v, want %+v", k, want)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "fd00::1")
	if got := DefaultConfig().Kubernetes.Server; got != "https://[fd00::1]:443" {
		t.Fatalf("ipv6 server = %q", got)
	}
}

func TestKubernetesConfig_YAMLAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.yaml")
	y := "control_plane:\n  token: t\nkubernetes:\n  server: https://yaml:1\n  ca_file: /yca\nprotected_namespaces: [a, b]\n"
	if err := os.WriteFile(path, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kubernetes.Server != "https://yaml:1" || cfg.Kubernetes.CAFile != "/yca" || len(cfg.ProtectedNamespaces) != 2 {
		t.Fatalf("yaml not applied: %+v %v", cfg.Kubernetes, cfg.ProtectedNamespaces)
	}
	t.Setenv("KBRIDGE_KUBERNETES_SERVER", "https://env:2")
	t.Setenv("KBRIDGE_KUBERNETES_CA_FILE", "/eca")
	t.Setenv("KBRIDGE_KUBERNETES_TOKEN_FILE", "/etok")
	t.Setenv("KBRIDGE_KUBECONFIG", "/ekc")
	t.Setenv("KBRIDGE_PROTECTED_NAMESPACES", "x,y,z")
	cfg, _ = LoadConfig(path)
	want := KubernetesConfig{Server: "https://env:2", CAFile: "/eca", TokenFile: "/etok", Kubeconfig: "/ekc"}
	if cfg.Kubernetes != want || len(cfg.ProtectedNamespaces) != 3 {
		t.Fatalf("env not applied: %+v %v", cfg.Kubernetes, cfg.ProtectedNamespaces)
	}
}

func TestKubernetesConfig_Validate(t *testing.T) {
	tests := []struct {
		name string
		k    KubernetesConfig
		want string
	}{
		{"no server", KubernetesConfig{TokenFile: "/t"}, "kubernetes.server is required when no kubeconfig is set"},
		{"no token file", KubernetesConfig{Server: "https://k"}, "kubernetes.token_file is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ControlPlane: ControlPlaneConfig{URL: "u", Token: "t"}, Cluster: ClusterConfig{Name: "c"}, Kubernetes: tt.k}
			if err := cfg.Validate(); err == nil || err.Error() != tt.want {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSyncConfig_DefaultYAMLAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.yaml")
	if err := os.WriteFile(path, []byte("control_plane:\n  token: t\nsync:\n  interval: 15s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, env string
		want      time.Duration
	}{
		{"yaml", "", 15 * time.Second},
		{"env", "2m", 2 * time.Minute},
		{"bad env refused by validate", "soon", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KBRIDGE_SYNC_INTERVAL", tt.env)
			cfg, err := LoadConfig(path)
			if err != nil || cfg.Sync.Interval != tt.want {
				t.Fatalf("interval = %v, err %v, want %v", cfg.Sync.Interval, err, tt.want)
			}
		})
	}
	if DefaultConfig().Sync.Interval != DefaultSyncInterval {
		t.Errorf("default interval = %v", DefaultConfig().Sync.Interval)
	}
}

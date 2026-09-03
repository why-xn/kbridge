package controlplane

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfig_GrantDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Grants.MaxDuration != 8*time.Hour {
		t.Errorf("max_duration = %v, want 8h", cfg.Grants.MaxDuration)
	}
	if cfg.Grants.DefaultDuration != time.Hour {
		t.Errorf("default_duration = %v, want 1h", cfg.Grants.DefaultDuration)
	}
	if cfg.Grants.AllowSelfApproval {
		t.Error("self-approval must be off by default: a second pair of eyes is the point")
	}
}

func TestConfig_ParsesGrantDurations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-plane.yaml")
	doc := `
server: {http_port: 8080, grpc_port: 9090}
auth:
  jwt_secret: "a-secret-that-is-at-least-32-characters"
grants:
  max_duration: 4h
  default_duration: 15m
  allow_self_approval: true
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Grants.MaxDuration != 4*time.Hour {
		t.Errorf("max_duration = %v, want 4h", cfg.Grants.MaxDuration)
	}
	if cfg.Grants.DefaultDuration != 15*time.Minute {
		t.Errorf("default_duration = %v, want 15m", cfg.Grants.DefaultDuration)
	}
	if !cfg.Grants.AllowSelfApproval {
		t.Error("allow_self_approval should be honoured when set")
	}
}

func TestConfig_RejectsBadGrantDurations(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"unparseable max", "grants:\n  max_duration: soon\n"},
		{"unparseable default", "grants:\n  default_duration: later\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "control-plane.yaml")
			if err := os.WriteFile(path, []byte(tt.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("expected a parse error")
			}
		})
	}
}

func TestConfig_ValidateGrants(t *testing.T) {
	tests := []struct {
		name    string
		grants  GrantsConfig
		wantErr bool
	}{
		{"defaults are valid", GrantsConfig{MaxDuration: 8 * time.Hour, DefaultDuration: time.Hour}, false},
		{"equal bounds are valid", GrantsConfig{MaxDuration: time.Hour, DefaultDuration: time.Hour}, false},
		// Zero means unset: a config written before grants existed must stay
		// valid rather than failing to start.
		{"unset bounds fall back", GrantsConfig{}, false},
		{"unset max with a small default", GrantsConfig{DefaultDuration: 30 * time.Minute}, false},
		{"negative max", GrantsConfig{MaxDuration: -time.Hour, DefaultDuration: time.Hour}, true},
		{"negative default", GrantsConfig{MaxDuration: time.Hour, DefaultDuration: -time.Minute}, true},
		{"default past the ceiling", GrantsConfig{MaxDuration: time.Hour, DefaultDuration: 2 * time.Hour}, true},
		{"unset max with a default past the built-in ceiling", GrantsConfig{DefaultDuration: 100 * time.Hour}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.JWTSecret = "a-secret-that-is-at-least-32-characters"
			cfg.Grants = tt.grants
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected a validation error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestGrantsConfig_EffectiveBounds(t *testing.T) {
	tests := []struct {
		name        string
		cfg         GrantsConfig
		wantMax     time.Duration
		wantDefault time.Duration
	}{
		{"explicit values are used", GrantsConfig{MaxDuration: 4 * time.Hour, DefaultDuration: 15 * time.Minute},
			4 * time.Hour, 15 * time.Minute},
		{"unset falls back", GrantsConfig{}, 8 * time.Hour, time.Hour},
		{"partially set", GrantsConfig{MaxDuration: 2 * time.Hour}, 2 * time.Hour, time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.EffectiveMaxDuration(); got != tt.wantMax {
				t.Errorf("EffectiveMaxDuration() = %v, want %v", got, tt.wantMax)
			}
			if got := tt.cfg.EffectiveDefaultDuration(); got != tt.wantDefault {
				t.Errorf("EffectiveDefaultDuration() = %v, want %v", got, tt.wantDefault)
			}
		})
	}
}

// TestGrantService_UnsetLimitsStillBound proves a service built from a config
// that predates grants still refuses an absurd window.
func TestGrantService_UnsetLimitsStillBound(t *testing.T) {
	store := newTestStore(t)
	svc := NewGrantService(store, NewAuditRecorder(store), GrantsConfig{})
	_, err := svc.Request(context.Background(), "dev@corp.com", "", "prod-eu", "",
		100*time.Hour, "INC-4521 far too long")
	if err == nil {
		t.Fatal("an unset ceiling must still fall back to a bound, not become unlimited")
	}
}

func TestConfig_ValidateNotify(t *testing.T) {
	tests := []struct {
		name    string
		hook    NotifyConfig
		wantErr string
	}{
		{"slack", NotifyConfig{URL: "https://hooks.slack.com/x", Format: "slack"}, ""},
		{"google chat", NotifyConfig{URL: "https://chat.googleapis.com/x", Format: "google-chat"}, ""},
		{"json with secret and filter", NotifyConfig{URL: "https://example.com/hook", Format: "json",
			Secret: "s", Events: []string{AuditStatusGrantRequested}}, ""},
		{"missing url", NotifyConfig{Format: "slack"}, "url must start with"},
		{"non-http url", NotifyConfig{URL: "ftp://x", Format: "slack"}, "url must start with"},
		{"unknown format", NotifyConfig{URL: "https://x", Format: "teams"}, "format must be"},
		{"unknown event", NotifyConfig{URL: "https://x", Format: "json", Events: []string{"grant-expired"}}, "unknown event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.JWTSecret = "a-secret-that-is-at-least-32-characters"
			cfg.Grants.Notify = []NotifyConfig{tt.hook}
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ParsesNotify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control-plane.yaml")
	doc := `
auth:
  jwt_secret: "a-secret-that-is-at-least-32-characters"
grants:
  notify:
    - url: https://hooks.slack.com/services/T/B/x
      format: slack
    - url: https://example.com/kbridge
      format: json
      secret: hunter2
      events: [grant-requested, grant-approved]
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Grants.Notify) != 2 {
		t.Fatalf("hooks = %d, want 2", len(cfg.Grants.Notify))
	}
	if cfg.Grants.Notify[1].Secret != "hunter2" || len(cfg.Grants.Notify[1].Events) != 2 {
		t.Errorf("second hook not parsed fully: %+v", cfg.Grants.Notify[1])
	}
}

// writeSecret writes one secret to a temp file and returns its path.
func writeSecret(t *testing.T, name, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNotifyConfig_ResolvesFromFiles(t *testing.T) {
	urlFile := writeSecret(t, "url", "https://hooks.slack.com/services/T/B/from-file")
	secretFile := writeSecret(t, "secret", "hunter2-from-file")

	dir := t.TempDir()
	path := filepath.Join(dir, "control-plane.yaml")
	doc := fmt.Sprintf(`
auth:
  jwt_secret: "a-secret-that-is-at-least-32-characters"
grants:
  notify:
    - url_file: %q
      format: slack
    - url: https://example.com/hook
      format: json
      secret_file: %q
`, urlFile, secretFile)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.Grants.Notify[0].URL; got != "https://hooks.slack.com/services/T/B/from-file" {
		t.Errorf("url from file = %q", got)
	}
	if got := cfg.Grants.Notify[1].Secret; got != "hunter2-from-file" {
		t.Errorf("secret from file = %q, want it trimmed and read", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("a hook whose url came from a file must validate: %v", err)
	}
}

func TestNotifyConfig_ResolvesFromEnv(t *testing.T) {
	// Env wins over inline, and _FILE wins over env, matching every other
	// secret. Hooks are indexed by position.
	t.Setenv("KBRIDGE_GRANTS_NOTIFY_0_URL", "https://from-env.example.com/hook")
	t.Setenv("KBRIDGE_GRANTS_NOTIFY_1_SECRET_FILE", writeSecret(t, "s", "from-env-file"))

	cfg := DefaultConfig()
	cfg.Grants.Notify = []NotifyConfig{
		{URL: "https://inline.example.com", Format: "json"},
		{URL: "https://example.com", Format: "json", Secret: "inline"},
	}
	if err := cfg.resolveSecrets(); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := cfg.Grants.Notify[0].URL; got != "https://from-env.example.com/hook" {
		t.Errorf("hook 0 url = %q, want the env value", got)
	}
	if got := cfg.Grants.Notify[1].Secret; got != "from-env-file" {
		t.Errorf("hook 1 secret = %q, want the _FILE env value", got)
	}
}

func TestNotifyConfig_FileErrors(t *testing.T) {
	tests := []struct {
		name string
		hook NotifyConfig
		want string
	}{
		{"missing url file", NotifyConfig{URLFile: "/nonexistent/url", Format: "slack"}, "reading secret file"},
		{"empty secret file", NotifyConfig{URL: "https://x", Format: "json", SecretFile: writeSecret(t, "e", "   ")}, "is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Grants.Notify = []NotifyConfig{tt.hook}
			err := cfg.resolveSecrets()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to contain %q", err, tt.want)
			}
			if err != nil && !strings.Contains(err.Error(), "grants.notify[0]") {
				t.Errorf("error should name the hook: %v", err)
			}
		})
	}
}

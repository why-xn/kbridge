package controlplane

import (
	"context"
	"os"
	"path/filepath"
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

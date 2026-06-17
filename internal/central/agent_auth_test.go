package central

import (
	"context"
	"errors"
	"testing"
	"time"
)

// seedClusterToken creates a cluster and an agent token bound to it, returning
// the cluster and the plaintext token.
func seedClusterToken(t *testing.T, store *SQLiteStore, clusterName, plaintext string, mutate func(*AgentToken)) *Cluster {
	t.Helper()
	ctx := context.Background()
	cluster := &Cluster{Name: clusterName, Status: ClusterStatusPending}
	if err := store.CreateCluster(ctx, cluster); err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	tok := &AgentToken{
		ClusterID:   cluster.ID,
		TokenHash:   hashToken(plaintext),
		TokenPrefix: plaintext[:5],
	}
	if mutate != nil {
		mutate(tok)
	}
	if err := store.CreateAgentToken(ctx, tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return cluster
}

func TestAgentAuthenticator_Authenticate(t *testing.T) {
	t.Run("valid token resolves cluster", func(t *testing.T) {
		store := newTestStore(t)
		cluster := seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store)

		got, err := authn.Authenticate(context.Background(), "secret-token", "prod")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.ID != cluster.ID {
			t.Fatalf("expected cluster %q, got %+v", cluster.ID, got)
		}
	})

	t.Run("unknown token is invalid", func(t *testing.T) {
		store := newTestStore(t)
		authn := NewAgentAuthenticator(store)
		_, err := authn.Authenticate(context.Background(), "nope", "prod")
		if !errors.Is(err, ErrInvalidAgentToken) {
			t.Fatalf("want ErrInvalidAgentToken, got %v", err)
		}
	})

	t.Run("revoked token is rejected", func(t *testing.T) {
		store := newTestStore(t)
		seedClusterToken(t, store, "prod", "secret-token", func(at *AgentToken) {
			at.IsRevoked = true
		})
		authn := NewAgentAuthenticator(store)
		_, err := authn.Authenticate(context.Background(), "secret-token", "prod")
		if !errors.Is(err, ErrRevokedAgentToken) {
			t.Fatalf("want ErrRevokedAgentToken, got %v", err)
		}
	})

	t.Run("expired token is rejected", func(t *testing.T) {
		store := newTestStore(t)
		past := time.Now().Add(-time.Hour)
		seedClusterToken(t, store, "prod", "secret-token", func(at *AgentToken) {
			at.ExpiresAt = &past
		})
		authn := NewAgentAuthenticator(store)
		_, err := authn.Authenticate(context.Background(), "secret-token", "prod")
		if !errors.Is(err, ErrExpiredAgentToken) {
			t.Fatalf("want ErrExpiredAgentToken, got %v", err)
		}
	})

	t.Run("cluster name mismatch is rejected", func(t *testing.T) {
		store := newTestStore(t)
		seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store)
		_, err := authn.Authenticate(context.Background(), "secret-token", "staging")
		if !errors.Is(err, ErrClusterMismatch) {
			t.Fatalf("want ErrClusterMismatch, got %v", err)
		}
	})

	t.Run("empty requested cluster accepts bound cluster", func(t *testing.T) {
		store := newTestStore(t)
		cluster := seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store)
		got, err := authn.Authenticate(context.Background(), "secret-token", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != cluster.ID {
			t.Fatalf("expected cluster %q, got %+v", cluster.ID, got)
		}
	})
}

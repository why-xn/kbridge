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
		TokenHash:   hashAgentToken(testPepper, plaintext),
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
		authn := NewAgentAuthenticator(store, testPepper)

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
		authn := NewAgentAuthenticator(store, testPepper)
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
		authn := NewAgentAuthenticator(store, testPepper)
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
		authn := NewAgentAuthenticator(store, testPepper)
		_, err := authn.Authenticate(context.Background(), "secret-token", "prod")
		if !errors.Is(err, ErrExpiredAgentToken) {
			t.Fatalf("want ErrExpiredAgentToken, got %v", err)
		}
	})

	t.Run("cluster name mismatch is rejected", func(t *testing.T) {
		store := newTestStore(t)
		seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store, testPepper)
		_, err := authn.Authenticate(context.Background(), "secret-token", "staging")
		if !errors.Is(err, ErrClusterMismatch) {
			t.Fatalf("want ErrClusterMismatch, got %v", err)
		}
	})

	t.Run("empty requested cluster accepts bound cluster", func(t *testing.T) {
		store := newTestStore(t)
		cluster := seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store, testPepper)
		got, err := authn.Authenticate(context.Background(), "secret-token", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != cluster.ID {
			t.Fatalf("expected cluster %q, got %+v", cluster.ID, got)
		}
	})

	t.Run("token sealed under a different pepper does not authenticate", func(t *testing.T) {
		store := newTestStore(t)
		seedClusterToken(t, store, "prod", "secret-token", nil)
		// Verifier configured with a different pepper than the one used to seal.
		authn := NewAgentAuthenticator(store, "a-different-pepper")
		_, err := authn.Authenticate(context.Background(), "secret-token", "prod")
		if !errors.Is(err, ErrInvalidAgentToken) {
			t.Fatalf("want ErrInvalidAgentToken, got %v", err)
		}
	})
}

func TestAgentAuthenticator_RecordsLastUsed(t *testing.T) {
	ctx := context.Background()

	t.Run("successful auth stamps last_used_at", func(t *testing.T) {
		store := newTestStore(t)
		cluster := seedClusterToken(t, store, "prod", "secret-token", nil)
		authn := NewAgentAuthenticator(store, testPepper)

		before := time.Now().Add(-time.Second)
		if _, err := authn.Authenticate(ctx, "secret-token", "prod"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		tokens, err := store.ListAgentTokensByCluster(ctx, cluster.ID)
		if err != nil || len(tokens) != 1 {
			t.Fatalf("list tokens: %v (n=%d)", err, len(tokens))
		}
		if tokens[0].LastUsedAt == nil {
			t.Fatal("expected last_used_at to be set after successful auth")
		}
		if tokens[0].LastUsedAt.Before(before) {
			t.Fatalf("last_used_at %v predates the auth call", tokens[0].LastUsedAt)
		}
	})

	t.Run("failed auth does not stamp last_used_at", func(t *testing.T) {
		store := newTestStore(t)
		cluster := seedClusterToken(t, store, "prod", "secret-token", func(at *AgentToken) {
			at.IsRevoked = true
		})
		authn := NewAgentAuthenticator(store, testPepper)

		if _, err := authn.Authenticate(ctx, "secret-token", "prod"); !errors.Is(err, ErrRevokedAgentToken) {
			t.Fatalf("want ErrRevokedAgentToken, got %v", err)
		}

		tokens, err := store.ListAgentTokensByCluster(ctx, cluster.ID)
		if err != nil || len(tokens) != 1 {
			t.Fatalf("list tokens: %v (n=%d)", err, len(tokens))
		}
		if tokens[0].LastUsedAt != nil {
			t.Fatalf("expected last_used_at to remain nil, got %v", tokens[0].LastUsedAt)
		}
	})
}

func TestHashAgentToken(t *testing.T) {
	t.Run("deterministic for the same pepper and token", func(t *testing.T) {
		if hashAgentToken("pepper", "tok") != hashAgentToken("pepper", "tok") {
			t.Fatal("expected stable digest")
		}
	})

	t.Run("pepper changes the digest", func(t *testing.T) {
		if hashAgentToken("pepper-a", "tok") == hashAgentToken("pepper-b", "tok") {
			t.Fatal("expected different peppers to yield different digests")
		}
	})

	t.Run("differs from an unkeyed sha256 of the token", func(t *testing.T) {
		if hashAgentToken("pepper", "tok") == hashToken("tok") {
			t.Fatal("HMAC digest must not equal the bare sha256 digest")
		}
	})
}

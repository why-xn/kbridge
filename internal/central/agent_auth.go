package central

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// Agent token authentication errors.
var (
	ErrInvalidAgentToken = errors.New("invalid agent token")
	ErrRevokedAgentToken = errors.New("agent token revoked")
	ErrExpiredAgentToken = errors.New("agent token expired")
	ErrClusterMismatch   = errors.New("token not valid for requested cluster")
)

// AgentAuthenticator validates agent registration tokens against the persistent
// store and resolves the cluster a token is bound to.
type AgentAuthenticator struct {
	store  Store
	pepper string
}

// NewAgentAuthenticator creates an AgentAuthenticator backed by the given store.
// pepper is the server-side secret used to HMAC tokens for lookup; it must match
// the pepper used when tokens were created.
func NewAgentAuthenticator(store Store, pepper string) *AgentAuthenticator {
	return &AgentAuthenticator{store: store, pepper: pepper}
}

// hashAgentToken derives the at-rest digest of an agent token as
// HMAC-SHA256(pepper, token). Using a keyed MAC instead of a bare hash means a
// stolen database alone cannot be used to verify guessed tokens — the attacker
// also needs the pepper, which lives in config, not the database.
func hashAgentToken(pepper, token string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// Authenticate verifies a plaintext agent token and returns the cluster it is
// bound to. requestedCluster, when non-empty, must match the token's cluster.
func (a *AgentAuthenticator) Authenticate(ctx context.Context, plaintext, requestedCluster string) (*Cluster, error) {
	token, err := a.store.GetAgentTokenByHash(ctx, hashAgentToken(a.pepper, plaintext))
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, ErrInvalidAgentToken
	}
	if token.IsRevoked {
		return nil, ErrRevokedAgentToken
	}
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		return nil, ErrExpiredAgentToken
	}

	cluster, err := a.store.GetClusterByID(ctx, token.ClusterID)
	if err != nil {
		return nil, err
	}
	if cluster == nil {
		return nil, ErrInvalidAgentToken
	}
	if requestedCluster != "" && requestedCluster != cluster.Name {
		return nil, ErrClusterMismatch
	}

	// Record last use for staleness detection. Best-effort: a failed touch must
	// not deny an otherwise valid agent.
	_ = a.store.TouchAgentToken(ctx, token.ID, time.Now().UTC())

	return cluster, nil
}

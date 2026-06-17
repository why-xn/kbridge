package central

import (
	"context"
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
	store Store
}

// NewAgentAuthenticator creates an AgentAuthenticator backed by the given store.
func NewAgentAuthenticator(store Store) *AgentAuthenticator {
	return &AgentAuthenticator{store: store}
}

// Authenticate verifies a plaintext agent token and returns the cluster it is
// bound to. requestedCluster, when non-empty, must match the token's cluster.
func (a *AgentAuthenticator) Authenticate(ctx context.Context, plaintext, requestedCluster string) (*Cluster, error) {
	token, err := a.store.GetAgentTokenByHash(ctx, hashToken(plaintext))
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
	return cluster, nil
}

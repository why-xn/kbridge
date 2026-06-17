package central

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// agentTokenPrefixLen is how many leading characters of a token are stored
// in plaintext for identification in listings.
const agentTokenPrefixLen = 13

// ClusterStatusPending marks a cluster that has been provisioned (e.g. via an
// agent token) but whose agent has not yet registered.
const ClusterStatusPending = "pending"

// AdminHandlers provides HTTP handlers for admin operations.
type AdminHandlers struct {
	store Store
}

// NewAdminHandlers creates a new AdminHandlers instance.
func NewAdminHandlers(store Store) *AdminHandlers {
	return &AdminHandlers{store: store}
}

type createAgentTokenRequest struct {
	ClusterName   string `json:"cluster_name" binding:"required"`
	Description   string `json:"description,omitempty"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

// agentTokenResponse is the metadata view of a token (never includes the secret).
type agentTokenResponse struct {
	ID          string     `json:"id"`
	ClusterName string     `json:"cluster_name"`
	TokenPrefix string     `json:"token_prefix"`
	Description string     `json:"description,omitempty"`
	IsRevoked   bool       `json:"is_revoked"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// createAgentTokenResponse includes the plaintext token, shown only once at creation.
type createAgentTokenResponse struct {
	agentTokenResponse
	Token string `json:"token"`
}

// HandleCreateAgentToken generates a new agent token for a cluster, creating
// the cluster record if it does not yet exist. The plaintext token is returned
// once and never stored.
func (h *AdminHandlers) HandleCreateAgentToken(c *gin.Context) {
	var req createAgentTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx := c.Request.Context()
	cluster, err := h.findOrCreateCluster(ctx, req.ClusterName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	plaintext, prefix, err := generateAgentToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		exp := time.Now().UTC().Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &exp
	}

	token := &AgentToken{
		ClusterID:   cluster.ID,
		TokenHash:   hashToken(plaintext),
		TokenPrefix: prefix,
		Description: req.Description,
		ExpiresAt:   expiresAt,
	}
	if err := h.store.CreateAgentToken(ctx, token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusCreated, createAgentTokenResponse{
		agentTokenResponse: toAgentTokenResponse(token, cluster.Name),
		Token:              plaintext,
	})
}

// HandleListAgentTokens lists agent tokens, optionally filtered by ?cluster=<name>.
func (h *AdminHandlers) HandleListAgentTokens(c *gin.Context) {
	ctx := c.Request.Context()

	if name := c.Query("cluster"); name != "" {
		cluster, err := h.store.GetClusterByName(ctx, name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if cluster == nil {
			c.JSON(http.StatusOK, gin.H{"tokens": []agentTokenResponse{}})
			return
		}
		tokens, err := h.store.ListAgentTokensByCluster(ctx, cluster.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"tokens": toAgentTokenResponses(tokens, cluster.Name)})
		return
	}

	clusters, err := h.store.ListClusters(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	out := []agentTokenResponse{}
	for _, cluster := range clusters {
		tokens, err := h.store.ListAgentTokensByCluster(ctx, cluster.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		out = append(out, toAgentTokenResponses(tokens, cluster.Name)...)
	}
	c.JSON(http.StatusOK, gin.H{"tokens": out})
}

// HandleRevokeAgentToken revokes an agent token by ID. Revocation is idempotent.
func (h *AdminHandlers) HandleRevokeAgentToken(c *gin.Context) {
	id := c.Param("id")
	if err := h.store.RevokeAgentToken(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "token revoked"})
}

func (h *AdminHandlers) findOrCreateCluster(ctx context.Context, name string) (*Cluster, error) {
	cluster, err := h.store.GetClusterByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if cluster != nil {
		return cluster, nil
	}
	cluster = &Cluster{Name: name, Status: ClusterStatusPending}
	if err := h.store.CreateCluster(ctx, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// generateAgentToken returns a new plaintext token and its display prefix.
func generateAgentToken() (plaintext, prefix string, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating token: %w", err)
	}
	plaintext = "kbat_" + hex.EncodeToString(b)
	return plaintext, plaintext[:agentTokenPrefixLen], nil
}

func toAgentTokenResponse(t *AgentToken, clusterName string) agentTokenResponse {
	return agentTokenResponse{
		ID:          t.ID,
		ClusterName: clusterName,
		TokenPrefix: t.TokenPrefix,
		Description: t.Description,
		IsRevoked:   t.IsRevoked,
		LastUsedAt:  t.LastUsedAt,
		ExpiresAt:   t.ExpiresAt,
		CreatedAt:   t.CreatedAt,
	}
}

func toAgentTokenResponses(tokens []*AgentToken, clusterName string) []agentTokenResponse {
	out := make([]agentTokenResponse, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toAgentTokenResponse(t, clusterName))
	}
	return out
}

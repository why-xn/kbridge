package central

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
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

type createUserRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Name     string `json:"name" binding:"required"`
	Password string `json:"password" binding:"required"`
	IsActive *bool  `json:"is_active"`
}

type updateUserRequest struct {
	Name     *string `json:"name"`
	IsActive *bool   `json:"is_active"`
	Password *string `json:"password"`
}

// HandleListUsers lists all users.
func (h *AdminHandlers) HandleListUsers(c *gin.Context) {
	users, err := h.store.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// HandleCreateUser creates a new user with a hashed password.
func (h *AdminHandlers) HandleCreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx := c.Request.Context()
	if existing, _ := h.store.GetUserByEmail(ctx, req.Email); existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already in use"})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	user := &User{
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: hash,
		IsActive:     req.IsActive == nil || *req.IsActive,
	}
	if err := h.store.CreateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, user)
}

// HandleUpdateUser updates a user's name, active state, and/or password.
func (h *AdminHandlers) HandleUpdateUser(c *gin.Context) {
	var req updateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx := c.Request.Context()
	user, err := h.store.GetUserByID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if user == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if req.Name != nil {
		user.Name = *req.Name
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}
	if req.Password != nil {
		hash, err := auth.HashPassword(*req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		user.PasswordHash = hash
	}

	if err := h.store.UpdateUser(ctx, user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// HandleDeleteUser deletes a user by ID.
func (h *AdminHandlers) HandleDeleteUser(c *gin.Context) {
	if err := h.store.DeleteUser(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

// defaultAuditPerPage and maxAuditPerPage bound audit query page sizes.
const (
	defaultAuditPerPage = 50
	maxAuditPerPage     = 200
)

// HandleListAuditLogs returns audit logs filtered by query parameters:
// user, cluster, status, from, to (RFC3339), page, per_page.
func (h *AdminHandlers) HandleListAuditLogs(c *gin.Context) {
	filter := AuditLogFilter{
		UserEmail:   c.Query("user"),
		ClusterName: c.Query("cluster"),
		Status:      c.Query("status"),
		Page:        atoiDefault(c.Query("page"), 1),
		PerPage:     clampPerPage(atoiDefault(c.Query("per_page"), defaultAuditPerPage)),
	}

	from, err := parseTimeParam(c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'from' timestamp (use RFC3339)"})
		return
	}
	to, err := parseTimeParam(c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'to' timestamp (use RFC3339)"})
		return
	}
	filter.From, filter.To = from, to

	logs, total, err := h.store.ListAuditLogs(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"logs":     logs,
		"total":    total,
		"page":     filter.Page,
		"per_page": filter.PerPage,
	})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func clampPerPage(n int) int {
	if n > maxAuditPerPage {
		return maxAuditPerPage
	}
	return n
}

func parseTimeParam(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
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

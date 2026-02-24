package central

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/why-xn/kbridge/internal/auth"
)

// AuthHandlers provides HTTP handlers for authentication.
type AuthHandlers struct {
	store         Store
	jwtManager    *auth.JWTManager
	refreshExpiry time.Duration
}

// NewAuthHandlers creates a new AuthHandlers instance.
func NewAuthHandlers(store Store, jm *auth.JWTManager, refreshExpiry time.Duration) *AuthHandlers {
	return &AuthHandlers{
		store:         store,
		jwtManager:    jm,
		refreshExpiry: refreshExpiry,
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

// HandleLogin authenticates a user and returns JWT tokens.
func (h *AuthHandlers) HandleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	user, err := h.store.GetUserByEmail(c.Request.Context(), req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if !user.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "account is disabled"})
		return
	}

	h.issueTokens(c, user)
}

// HandleRefresh exchanges a refresh token for new access and refresh tokens.
func (h *AuthHandlers) HandleRefresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash := hashToken(req.RefreshToken)
	rt, err := h.store.GetRefreshTokenByHash(c.Request.Context(), hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if rt == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	if time.Now().After(rt.ExpiresAt) {
		h.store.DeleteRefreshToken(c.Request.Context(), rt.ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token expired"})
		return
	}

	// Delete the used refresh token (rotation)
	h.store.DeleteRefreshToken(c.Request.Context(), rt.ID)

	user, err := h.store.GetUserByID(c.Request.Context(), rt.UserID)
	if err != nil || user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found"})
		return
	}

	h.issueTokens(c, user)
}

// HandleLogout invalidates a refresh token.
func (h *AuthHandlers) HandleLogout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash := hashToken(req.RefreshToken)
	h.store.DeleteRefreshToken(c.Request.Context(), hash)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// HandleChangePassword changes the authenticated user's password.
func (h *AuthHandlers) HandleChangePassword(c *gin.Context) {
	claims := auth.GetUserFromContext(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	user, err := h.store.GetUserByID(c.Request.Context(), claims.UserID)
	if err != nil || user == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if !auth.CheckPassword(req.CurrentPassword, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	user.PasswordHash = newHash
	if err := h.store.UpdateUser(c.Request.Context(), user); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password changed"})
}

func (h *AuthHandlers) issueTokens(c *gin.Context, user *User) {
	roles, _ := h.store.ListRolesByUser(c.Request.Context(), user.ID)
	roleNames := make([]string, len(roles))
	for i, r := range roles {
		roleNames[i] = r.Name
	}

	claims := &auth.UserClaims{
		UserID: user.ID,
		Email:  user.Email,
		Name:   user.Name,
		Roles:  roleNames,
	}

	accessToken, err := h.jwtManager.GenerateAccessToken(claims)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	plaintext, refreshHash, err := h.jwtManager.GenerateRefreshToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	rt := &RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		TokenHash: refreshHash,
		ExpiresAt: time.Now().Add(h.refreshExpiry),
	}
	if err := h.store.CreateRefreshToken(c.Request.Context(), rt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, tokenResponse{
		AccessToken:  accessToken,
		RefreshToken: plaintext,
		ExpiresIn:    int(h.refreshExpiry.Seconds()),
	})
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

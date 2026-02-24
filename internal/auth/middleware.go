package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const userContextKey = "user"

// AuthMiddleware validates the JWT Bearer token from the Authorization header.
func AuthMiddleware(jm *JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		token := strings.TrimPrefix(header, "Bearer ")
		if token == header {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}

		claims, err := jm.ValidateAccessToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(userContextKey, claims)
		c.Next()
	}
}

// AdminRequired checks that the authenticated user has the admin role.
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := GetUserFromContext(c)
		if claims == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
			return
		}

		for _, role := range claims.Roles {
			if role == "admin" {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin role required"})
	}
}

// GetUserFromContext retrieves the UserClaims from the Gin context.
func GetUserFromContext(c *gin.Context) *UserClaims {
	val, exists := c.Get(userContextKey)
	if !exists {
		return nil
	}
	claims, ok := val.(*UserClaims)
	if !ok {
		return nil
	}
	return claims
}

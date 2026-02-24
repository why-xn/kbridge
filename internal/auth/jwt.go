package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// UserClaims holds the custom claims embedded in access tokens.
type UserClaims struct {
	UserID string   `json:"user_id"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

// JWTManager handles JWT token creation and validation.
type JWTManager struct {
	secret       []byte
	accessExpiry time.Duration
}

// NewJWTManager creates a new JWT manager.
func NewJWTManager(secret string, accessExpiry time.Duration) *JWTManager {
	return &JWTManager{
		secret:       []byte(secret),
		accessExpiry: accessExpiry,
	}
}

// GenerateAccessToken creates a signed JWT with the given claims.
func (m *JWTManager) GenerateAccessToken(claims *UserClaims) (string, error) {
	now := time.Now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(now.Add(m.accessExpiry)),
		IssuedAt:  jwt.NewNumericDate(now),
		Issuer:    "kbridge",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// ValidateAccessToken parses and validates a JWT, returning the claims.
func (m *JWTManager) ValidateAccessToken(tokenStr string) (*UserClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &UserClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parsing token: %w", err)
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

// GenerateRefreshToken returns a random plaintext token and its SHA-256 hash.
func (m *JWTManager) GenerateRefreshToken() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating random bytes: %w", err)
	}
	plaintext = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return plaintext, hash, nil
}

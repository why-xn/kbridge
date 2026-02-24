package auth

import (
	"testing"
	"time"
)

func TestGenerateAccessToken(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	claims := &UserClaims{
		UserID: "user-123",
		Email:  "test@example.com",
		Name:   "Test User",
		Roles:  []string{"admin"},
	}

	token, err := jm.GenerateAccessToken(claims)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
}

func TestValidateAccessToken(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	claims := &UserClaims{
		UserID: "user-123",
		Email:  "test@example.com",
		Name:   "Test User",
		Roles:  []string{"admin"},
	}

	token, _ := jm.GenerateAccessToken(claims)
	parsed, err := jm.ValidateAccessToken(token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parsed.UserID != "user-123" {
		t.Errorf("expected user-123, got %q", parsed.UserID)
	}
	if parsed.Email != "test@example.com" {
		t.Errorf("expected test@example.com, got %q", parsed.Email)
	}
	if parsed.Name != "Test User" {
		t.Errorf("expected Test User, got %q", parsed.Name)
	}
	if len(parsed.Roles) != 1 || parsed.Roles[0] != "admin" {
		t.Errorf("expected [admin], got %v", parsed.Roles)
	}
}

func TestValidateAccessToken_Expired(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", -1*time.Hour)
	claims := &UserClaims{UserID: "user-123", Email: "test@example.com", Name: "Test", Roles: []string{}}
	token, _ := jm.GenerateAccessToken(claims)
	_, err := jm.ValidateAccessToken(token)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestValidateAccessToken_WrongSecret(t *testing.T) {
	jm1 := NewJWTManager("secret-one-at-least-32-chars!!!", 24*time.Hour)
	jm2 := NewJWTManager("secret-two-at-least-32-chars!!!", 24*time.Hour)
	claims := &UserClaims{UserID: "user-123", Email: "test@example.com", Name: "Test", Roles: []string{}}
	token, _ := jm1.GenerateAccessToken(claims)
	_, err := jm2.ValidateAccessToken(token)
	if err == nil {
		t.Error("expected error for wrong secret")
	}
}

func TestGenerateRefreshToken(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	plaintext, hash, err := jm.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plaintext == "" || hash == "" {
		t.Error("tokens should not be empty")
	}
	if plaintext == hash {
		t.Error("plaintext and hash should differ")
	}
}

func TestGenerateRefreshToken_Uniqueness(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	p1, _, _ := jm.GenerateRefreshToken()
	p2, _, _ := jm.GenerateRefreshToken()
	if p1 == p2 {
		t.Error("two refresh tokens should not be equal")
	}
}

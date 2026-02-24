package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestAuthMiddleware(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)

	tests := []struct {
		name       string
		authHeader string
		wantCode   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"invalid format", "Basic abc123", http.StatusUnauthorized},
		{"invalid token", "Bearer invalid.jwt.token", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, r := gin.CreateTestContext(w)

			r.Use(AuthMiddleware(jm))
			r.GET("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			c.Request, _ = http.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}
			r.ServeHTTP(w, c.Request)

			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d", tt.wantCode, w.Code)
			}
		})
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	jm := NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	claims := &UserClaims{
		UserID: "user-123",
		Email:  "test@example.com",
		Name:   "Test User",
		Roles:  []string{"admin"},
	}
	token, _ := jm.GenerateAccessToken(claims)

	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	var gotClaims *UserClaims
	r.Use(AuthMiddleware(jm))
	r.GET("/test", func(c *gin.Context) {
		gotClaims = GetUserFromContext(c)
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if gotClaims == nil {
		t.Fatal("expected claims in context")
	}
	if gotClaims.UserID != "user-123" {
		t.Errorf("expected user-123, got %q", gotClaims.UserID)
	}
}

func TestAdminRequired(t *testing.T) {
	tests := []struct {
		name     string
		roles    []string
		wantCode int
	}{
		{"admin role", []string{"admin"}, http.StatusOK},
		{"viewer role only", []string{"viewer"}, http.StatusForbidden},
		{"no roles", []string{}, http.StatusForbidden},
		{"multiple roles including admin", []string{"viewer", "admin"}, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			_, r := gin.CreateTestContext(w)

			r.Use(func(c *gin.Context) {
				c.Set(userContextKey, &UserClaims{
					UserID: "user-123",
					Roles:  tt.roles,
				})
				c.Next()
			})
			r.Use(AdminRequired())
			r.GET("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req, _ := http.NewRequest("GET", "/test", nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d", tt.wantCode, w.Code)
			}
		})
	}
}

func TestAdminRequired_NoUser(t *testing.T) {
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	r.Use(AdminRequired())
	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestGetUserFromContext_NoUser(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if got := GetUserFromContext(c); got != nil {
		t.Error("expected nil when no user in context")
	}
}

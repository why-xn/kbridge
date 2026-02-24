package central

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/why-xn/kbridge/internal/auth"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestAuthHandlers(t *testing.T) (*AuthHandlers, *SQLiteStore) {
	t.Helper()
	store := newTestStore(t)

	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", 24*time.Hour)
	ah := NewAuthHandlers(store, jm, 7*24*time.Hour)
	return ah, store
}

func seedTestUser(t *testing.T, store *SQLiteStore) *User {
	t.Helper()
	hash, _ := auth.HashPassword("password123")
	user := &User{
		ID:           uuid.New().String(),
		Email:        "test@example.com",
		PasswordHash: hash,
		Name:         "Test User",
		IsActive:     true,
	}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Assign admin role
	store.AssignRole(context.Background(), user.ID, "00000000-0000-0000-0000-000000000001", "")
	return user
}

func TestAuthHandler_Login(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]string
		setup    func(*SQLiteStore)
		wantCode int
	}{
		{
			name:     "valid credentials",
			body:     map[string]string{"email": "test@example.com", "password": "password123"},
			wantCode: http.StatusOK,
		},
		{
			name:     "wrong password",
			body:     map[string]string{"email": "test@example.com", "password": "wrongpass"},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "non-existent user",
			body:     map[string]string{"email": "nobody@example.com", "password": "password123"},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "disabled user",
			body: map[string]string{"email": "disabled@example.com", "password": "password123"},
			setup: func(store *SQLiteStore) {
				hash, _ := auth.HashPassword("password123")
				store.CreateUser(context.Background(), &User{
					ID:           uuid.New().String(),
					Email:        "disabled@example.com",
					PasswordHash: hash,
					Name:         "Disabled",
					IsActive:     false,
				})
			},
			wantCode: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ah, store := newTestAuthHandlers(t)
			seedTestUser(t, store)
			if tt.setup != nil {
				tt.setup(store)
			}

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			_, r := gin.CreateTestContext(w)
			r.POST("/auth/login", ah.HandleLogin)

			req, _ := http.NewRequest("POST", "/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d; body: %s", tt.wantCode, w.Code, w.Body.String())
			}

			if tt.wantCode == http.StatusOK {
				var resp tokenResponse
				json.Unmarshal(w.Body.Bytes(), &resp)
				if resp.AccessToken == "" {
					t.Error("expected access_token in response")
				}
				if resp.RefreshToken == "" {
					t.Error("expected refresh_token in response")
				}
			}
		})
	}
}

func TestAuthHandler_Refresh(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*testing.T, *AuthHandlers, *SQLiteStore) string
		wantCode int
	}{
		{
			name: "valid refresh token",
			setup: func(t *testing.T, ah *AuthHandlers, store *SQLiteStore) string {
				user := seedTestUser(t, store)
				plain, hash, _ := ah.jwtManager.GenerateRefreshToken()
				store.CreateRefreshToken(context.Background(), &RefreshToken{
					ID:        uuid.New().String(),
					UserID:    user.ID,
					TokenHash: hash,
					ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
				})
				return plain
			},
			wantCode: http.StatusOK,
		},
		{
			name: "invalid refresh token",
			setup: func(t *testing.T, ah *AuthHandlers, store *SQLiteStore) string {
				return "invalid-token"
			},
			wantCode: http.StatusUnauthorized,
		},
		{
			name: "expired refresh token",
			setup: func(t *testing.T, ah *AuthHandlers, store *SQLiteStore) string {
				user := seedTestUser(t, store)
				plain, hash, _ := ah.jwtManager.GenerateRefreshToken()
				store.CreateRefreshToken(context.Background(), &RefreshToken{
					ID:        uuid.New().String(),
					UserID:    user.ID,
					TokenHash: hash,
					ExpiresAt: time.Now().Add(-1 * time.Hour),
				})
				return plain
			},
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ah, store := newTestAuthHandlers(t)
			refreshToken := tt.setup(t, ah, store)

			body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
			w := httptest.NewRecorder()
			_, r := gin.CreateTestContext(w)
			r.POST("/auth/refresh", ah.HandleRefresh)

			req, _ := http.NewRequest("POST", "/auth/refresh", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d; body: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestAuthHandler_ChangePassword(t *testing.T) {
	tests := []struct {
		name     string
		body     map[string]string
		wantCode int
	}{
		{
			name:     "valid change",
			body:     map[string]string{"current_password": "password123", "new_password": "newpass456"},
			wantCode: http.StatusOK,
		},
		{
			name:     "wrong current password",
			body:     map[string]string{"current_password": "wrongpass", "new_password": "newpass456"},
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ah, store := newTestAuthHandlers(t)
			user := seedTestUser(t, store)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			_, r := gin.CreateTestContext(w)

			// Simulate authenticated user
			r.Use(func(c *gin.Context) {
				c.Set("user", &auth.UserClaims{
					UserID: user.ID,
					Email:  user.Email,
				})
				c.Next()
			})
			r.POST("/auth/change-password", ah.HandleChangePassword)

			req, _ := http.NewRequest("POST", "/auth/change-password", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("expected %d, got %d; body: %s", tt.wantCode, w.Code, w.Body.String())
			}

			// Verify the new password works after successful change
			if tt.wantCode == http.StatusOK {
				updated, _ := store.GetUserByID(context.Background(), user.ID)
				if !auth.CheckPassword("newpass456", updated.PasswordHash) {
					t.Error("new password should work after change")
				}
			}
		})
	}
}

func TestAuthHandler_Logout(t *testing.T) {
	ah, store := newTestAuthHandlers(t)
	user := seedTestUser(t, store)

	// Create a refresh token
	plain, hash, _ := ah.jwtManager.GenerateRefreshToken()
	store.CreateRefreshToken(context.Background(), &RefreshToken{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		TokenHash: hash,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	})

	body, _ := json.Marshal(map[string]string{"refresh_token": plain})
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)
	r.POST("/auth/logout", ah.HandleLogout)

	req, _ := http.NewRequest("POST", "/auth/logout", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
}

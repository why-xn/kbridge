package central

import (
	"context"
	"sync"
	"testing"
	"time"
)

// testPepper is the agent-token HMAC pepper used across tests. Token creation
// and verification must use the same pepper, so all test fixtures share this.
const testPepper = "test-agent-token-pepper"

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestSQLiteStore_Migrate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"users table", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		}},
		{"clusters table", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM clusters").Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		}},
		{"agent_tokens table", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_tokens").Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		}},
		{"audit_logs table", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_logs").Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		}},
		{"refresh_tokens table", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM refresh_tokens").Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		}},
		{"is_admin column", func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE is_admin = 0").Scan(&count); err != nil {
				t.Fatalf("is_admin column should exist: %v", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}

	// Verify idempotency
	t.Run("migrate is idempotent", func(t *testing.T) {
		if err := store.Migrate(ctx); err != nil {
			t.Fatalf("second migration should succeed: %v", err)
		}
	})
}

func TestSQLiteStore_Users(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and get by ID", func(t *testing.T) {
			user := &User{Email: "alice@test.com", PasswordHash: "hash1", Name: "Alice", IsActive: true}
			if err := store.CreateUser(ctx, user); err != nil {
				t.Fatalf("create user: %v", err)
			}
			if user.ID == "" {
				t.Fatal("ID should be set")
			}
			got, err := store.GetUserByID(ctx, user.ID)
			if err != nil {
				t.Fatalf("get user: %v", err)
			}
			if got.Email != "alice@test.com" {
				t.Errorf("expected email alice@test.com, got %q", got.Email)
			}
			if got.Name != "Alice" {
				t.Errorf("expected name Alice, got %q", got.Name)
			}
		}},
		{"get by email", func(t *testing.T) {
			user := &User{Email: "bob@test.com", PasswordHash: "hash2", Name: "Bob", IsActive: true}
			store.CreateUser(ctx, user)
			got, err := store.GetUserByEmail(ctx, "bob@test.com")
			if err != nil {
				t.Fatalf("get user by email: %v", err)
			}
			if got == nil {
				t.Fatal("user not found")
			}
			if got.Name != "Bob" {
				t.Errorf("expected name Bob, got %q", got.Name)
			}
		}},
		{"get non-existent returns nil", func(t *testing.T) {
			got, err := store.GetUserByID(ctx, "nonexistent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Error("expected nil for non-existent user")
			}
		}},
		{"list users", func(t *testing.T) {
			users, err := store.ListUsers(ctx)
			if err != nil {
				t.Fatalf("list users: %v", err)
			}
			if len(users) < 2 {
				t.Errorf("expected at least 2 users, got %d", len(users))
			}
		}},
		{"update user", func(t *testing.T) {
			user := &User{Email: "update@test.com", PasswordHash: "hash3", Name: "Update", IsActive: true}
			store.CreateUser(ctx, user)
			user.Name = "Updated"
			user.IsActive = false
			if err := store.UpdateUser(ctx, user); err != nil {
				t.Fatalf("update user: %v", err)
			}
			got, _ := store.GetUserByID(ctx, user.ID)
			if got.Name != "Updated" {
				t.Errorf("expected name Updated, got %q", got.Name)
			}
			if got.IsActive {
				t.Error("expected is_active false")
			}
		}},
		{"delete user", func(t *testing.T) {
			user := &User{Email: "delete@test.com", PasswordHash: "hash4", Name: "Delete", IsActive: true}
			store.CreateUser(ctx, user)
			if err := store.DeleteUser(ctx, user.ID); err != nil {
				t.Fatalf("delete user: %v", err)
			}
			got, _ := store.GetUserByID(ctx, user.ID)
			if got != nil {
				t.Error("user should be deleted")
			}
		}},
		{"duplicate email error", func(t *testing.T) {
			user1 := &User{Email: "dupe@test.com", PasswordHash: "hash5", Name: "Dupe1", IsActive: true}
			store.CreateUser(ctx, user1)
			user2 := &User{Email: "dupe@test.com", PasswordHash: "hash6", Name: "Dupe2", IsActive: true}
			err := store.CreateUser(ctx, user2)
			if err == nil {
				t.Error("expected error for duplicate email")
			}
		}},
		{"is_admin persisted", func(t *testing.T) {
			user := &User{Email: "admin@test.com", PasswordHash: "h", Name: "Admin", IsActive: true, IsAdmin: true}
			if err := store.CreateUser(ctx, user); err != nil {
				t.Fatalf("create admin user: %v", err)
			}
			got, _ := store.GetUserByID(ctx, user.ID)
			if !got.IsAdmin {
				t.Error("expected is_admin true")
			}
			got.IsAdmin = false
			store.UpdateUser(ctx, got)
			updated, _ := store.GetUserByID(ctx, got.ID)
			if updated.IsAdmin {
				t.Error("expected is_admin false after update")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteStore_Clusters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and get by ID", func(t *testing.T) {
			c := &Cluster{Name: "prod", Status: "disconnected"}
			if err := store.CreateCluster(ctx, c); err != nil {
				t.Fatalf("create cluster: %v", err)
			}
			if c.ID == "" {
				t.Fatal("ID should be set")
			}
			got, err := store.GetClusterByID(ctx, c.ID)
			if err != nil {
				t.Fatalf("get cluster: %v", err)
			}
			if got.Name != "prod" {
				t.Errorf("expected name prod, got %q", got.Name)
			}
		}},
		{"get by name", func(t *testing.T) {
			c := &Cluster{Name: "staging", Status: "connected"}
			store.CreateCluster(ctx, c)
			got, err := store.GetClusterByName(ctx, "staging")
			if err != nil {
				t.Fatalf("get cluster by name: %v", err)
			}
			if got == nil {
				t.Fatal("cluster not found")
			}
			if got.Status != "connected" {
				t.Errorf("expected status connected, got %q", got.Status)
			}
		}},
		{"get non-existent returns nil", func(t *testing.T) {
			got, err := store.GetClusterByID(ctx, "nonexistent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Error("expected nil for non-existent cluster")
			}
		}},
		{"list clusters", func(t *testing.T) {
			clusters, err := store.ListClusters(ctx)
			if err != nil {
				t.Fatalf("list clusters: %v", err)
			}
			if len(clusters) < 2 {
				t.Errorf("expected at least 2 clusters, got %d", len(clusters))
			}
		}},
		{"update cluster", func(t *testing.T) {
			c := &Cluster{Name: "update-cluster", Status: "disconnected"}
			store.CreateCluster(ctx, c)
			c.Status = "connected"
			now := time.Now()
			c.LastSeenAt = &now
			if err := store.UpdateCluster(ctx, c); err != nil {
				t.Fatalf("update cluster: %v", err)
			}
			got, _ := store.GetClusterByID(ctx, c.ID)
			if got.Status != "connected" {
				t.Errorf("expected status connected, got %q", got.Status)
			}
			if got.LastSeenAt == nil {
				t.Error("expected last_seen_at to be set")
			}
		}},
		{"delete cluster", func(t *testing.T) {
			c := &Cluster{Name: "delete-cluster", Status: "disconnected"}
			store.CreateCluster(ctx, c)
			if err := store.DeleteCluster(ctx, c.ID); err != nil {
				t.Fatalf("delete cluster: %v", err)
			}
			got, _ := store.GetClusterByID(ctx, c.ID)
			if got != nil {
				t.Error("cluster should be deleted")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteStore_AgentTokens(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a cluster first
	cluster := &Cluster{Name: "token-cluster", Status: "disconnected"}
	store.CreateCluster(ctx, cluster)

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and get by hash", func(t *testing.T) {
			tok := &AgentToken{
				ClusterID:   cluster.ID,
				TokenHash:   "hash123",
				TokenPrefix: "kb_",
				Description: "test token",
			}
			if err := store.CreateAgentToken(ctx, tok); err != nil {
				t.Fatalf("create agent token: %v", err)
			}
			got, err := store.GetAgentTokenByHash(ctx, "hash123")
			if err != nil {
				t.Fatalf("get agent token: %v", err)
			}
			if got == nil {
				t.Fatal("token not found")
			}
			if got.Description != "test token" {
				t.Errorf("expected description 'test token', got %q", got.Description)
			}
		}},
		{"get non-existent returns nil", func(t *testing.T) {
			got, err := store.GetAgentTokenByHash(ctx, "nonexistent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Error("expected nil for non-existent token")
			}
		}},
		{"list by cluster", func(t *testing.T) {
			tok2 := &AgentToken{
				ClusterID:   cluster.ID,
				TokenHash:   "hash456",
				TokenPrefix: "kb_",
			}
			store.CreateAgentToken(ctx, tok2)
			tokens, err := store.ListAgentTokensByCluster(ctx, cluster.ID)
			if err != nil {
				t.Fatalf("list agent tokens: %v", err)
			}
			if len(tokens) < 2 {
				t.Errorf("expected at least 2 tokens, got %d", len(tokens))
			}
		}},
		{"revoke token", func(t *testing.T) {
			tok := &AgentToken{
				ClusterID:   cluster.ID,
				TokenHash:   "revokeme",
				TokenPrefix: "kb_",
			}
			store.CreateAgentToken(ctx, tok)
			if err := store.RevokeAgentToken(ctx, tok.ID); err != nil {
				t.Fatalf("revoke token: %v", err)
			}
			got, _ := store.GetAgentTokenByHash(ctx, "revokeme")
			if !got.IsRevoked {
				t.Error("token should be revoked")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteStore_RefreshTokens(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user := &User{Email: "rt@test.com", PasswordHash: "h", Name: "RT", IsActive: true}
	store.CreateUser(ctx, user)

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and get by hash", func(t *testing.T) {
			rt := &RefreshToken{
				UserID:    user.ID,
				TokenHash: "rthash1",
				ExpiresAt: time.Now().Add(24 * time.Hour),
			}
			if err := store.CreateRefreshToken(ctx, rt); err != nil {
				t.Fatalf("create refresh token: %v", err)
			}
			got, err := store.GetRefreshTokenByHash(ctx, "rthash1")
			if err != nil {
				t.Fatalf("get refresh token: %v", err)
			}
			if got == nil {
				t.Fatal("refresh token not found")
			}
			if got.UserID != user.ID {
				t.Errorf("expected user_id %q, got %q", user.ID, got.UserID)
			}
		}},
		{"get non-existent returns nil", func(t *testing.T) {
			got, err := store.GetRefreshTokenByHash(ctx, "nonexistent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Error("expected nil for non-existent token")
			}
		}},
		{"delete by ID", func(t *testing.T) {
			rt := &RefreshToken{
				UserID:    user.ID,
				TokenHash: "rthash-del",
				ExpiresAt: time.Now().Add(24 * time.Hour),
			}
			store.CreateRefreshToken(ctx, rt)
			if err := store.DeleteRefreshToken(ctx, rt.ID); err != nil {
				t.Fatalf("delete refresh token: %v", err)
			}
			got, _ := store.GetRefreshTokenByHash(ctx, "rthash-del")
			if got != nil {
				t.Error("token should be deleted")
			}
		}},
		{"delete by user", func(t *testing.T) {
			user2 := &User{Email: "rt2@test.com", PasswordHash: "h", Name: "RT2", IsActive: true}
			store.CreateUser(ctx, user2)
			store.CreateRefreshToken(ctx, &RefreshToken{
				UserID: user2.ID, TokenHash: "u2h1", ExpiresAt: time.Now().Add(time.Hour),
			})
			store.CreateRefreshToken(ctx, &RefreshToken{
				UserID: user2.ID, TokenHash: "u2h2", ExpiresAt: time.Now().Add(time.Hour),
			})
			if err := store.DeleteRefreshTokensByUser(ctx, user2.ID); err != nil {
				t.Fatalf("delete by user: %v", err)
			}
			got1, _ := store.GetRefreshTokenByHash(ctx, "u2h1")
			got2, _ := store.GetRefreshTokenByHash(ctx, "u2h2")
			if got1 != nil || got2 != nil {
				t.Error("all tokens should be deleted")
			}
		}},
		{"cleanup expired", func(t *testing.T) {
			// Create an expired token
			store.CreateRefreshToken(ctx, &RefreshToken{
				UserID: user.ID, TokenHash: "expired1",
				ExpiresAt: time.Now().Add(-1 * time.Hour),
			})
			// Create a valid token
			store.CreateRefreshToken(ctx, &RefreshToken{
				UserID: user.ID, TokenHash: "valid1",
				ExpiresAt: time.Now().Add(24 * time.Hour),
			})
			if err := store.CleanupExpiredRefreshTokens(ctx); err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			got, _ := store.GetRefreshTokenByHash(ctx, "expired1")
			if got != nil {
				t.Error("expired token should be cleaned up")
			}
			got2, _ := store.GetRefreshTokenByHash(ctx, "valid1")
			if got2 == nil {
				t.Error("valid token should still exist")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteConcurrentWritesNoLock(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			log := &AuditLog{
				UserEmail: "u@x", ClusterName: "c", Command: "get pods",
				Status: AuditStatusSuccess,
			}
			if err := store.CreateAuditLog(ctx, log); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed (database locked?): %v", err)
	}
}

func TestSQLitePragmas(t *testing.T) {
	store := newTestStore(t)
	var busy int
	if err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy < 1000 {
		t.Fatalf("busy_timeout=%d want >=1000", busy)
	}
	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open conns=%d want 1", got)
	}
}

func TestSQLiteStore_AuditLogs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and list", func(t *testing.T) {
			exitCode := int32(0)
			dur := int64(100)
			log := &AuditLog{
				UserEmail:   "audit@test.com",
				ClusterName: "prod",
				Command:     "get pods",
				Status:      "success",
				ExitCode:    &exitCode,
				DurationMs:  &dur,
				ClientIP:    "127.0.0.1",
			}
			if err := store.CreateAuditLog(ctx, log); err != nil {
				t.Fatalf("create audit log: %v", err)
			}
			logs, total, err := store.ListAuditLogs(ctx, AuditLogFilter{})
			if err != nil {
				t.Fatalf("list audit logs: %v", err)
			}
			if total < 1 {
				t.Fatalf("expected at least 1 log, got %d", total)
			}
			if len(logs) < 1 {
				t.Fatal("expected at least 1 log entry")
			}
		}},
		{"filter by email", func(t *testing.T) {
			store.CreateAuditLog(ctx, &AuditLog{
				UserEmail: "filter@test.com", ClusterName: "dev",
				Command: "get ns", Status: "success",
			})
			logs, total, err := store.ListAuditLogs(ctx, AuditLogFilter{UserEmail: "filter@test.com"})
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if total != 1 {
				t.Errorf("expected 1 log, got %d", total)
			}
			if len(logs) != 1 {
				t.Errorf("expected 1 entry, got %d", len(logs))
			}
		}},
		{"filter by cluster", func(t *testing.T) {
			store.CreateAuditLog(ctx, &AuditLog{
				UserEmail: "x@test.com", ClusterName: "unique-cluster",
				Command: "get pods", Status: "success",
			})
			logs, total, _ := store.ListAuditLogs(ctx, AuditLogFilter{ClusterName: "unique-cluster"})
			if total != 1 {
				t.Errorf("expected 1 log, got %d", total)
			}
			if len(logs) != 1 {
				t.Errorf("expected 1 entry, got %d", len(logs))
			}
		}},
		{"filter by status", func(t *testing.T) {
			store.CreateAuditLog(ctx, &AuditLog{
				UserEmail: "err@test.com", ClusterName: "prod",
				Command: "delete pod", Status: "error",
			})
			logs, _, _ := store.ListAuditLogs(ctx, AuditLogFilter{Status: "error"})
			if len(logs) < 1 {
				t.Error("expected at least 1 error log")
			}
		}},
		{"pagination", func(t *testing.T) {
			for i := 0; i < 5; i++ {
				store.CreateAuditLog(ctx, &AuditLog{
					UserEmail: "page@test.com", ClusterName: "page-cluster",
					Command: "get pods", Status: "success",
				})
			}
			logs, total, _ := store.ListAuditLogs(ctx, AuditLogFilter{
				ClusterName: "page-cluster", Page: 1, PerPage: 2,
			})
			if total != 5 {
				t.Errorf("expected total 5, got %d", total)
			}
			if len(logs) != 2 {
				t.Errorf("expected 2 entries on page, got %d", len(logs))
			}
		}},
		{"cleanup old", func(t *testing.T) {
			// Insert a log with old timestamp by directly using DB
			store.db.ExecContext(ctx,
				`INSERT INTO audit_logs (id, user_email, cluster_name, command, status, created_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				"old-log-id", "old@test.com", "old-cluster", "get pods", "success",
				time.Now().Add(-48*time.Hour).UTC().Format(timeFormat),
			)
			n, err := store.CleanupOldAuditLogs(ctx, time.Now().Add(-24*time.Hour))
			if err != nil {
				t.Fatalf("cleanup: %v", err)
			}
			if n < 1 {
				t.Errorf("expected at least 1 deleted, got %d", n)
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

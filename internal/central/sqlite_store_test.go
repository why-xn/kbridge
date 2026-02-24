package central

import (
	"context"
	"testing"
	"time"
)

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
		name  string
		query string
	}{
		{"users table", "SELECT COUNT(*) FROM users"},
		{"clusters table", "SELECT COUNT(*) FROM clusters"},
		{"agent_tokens table", "SELECT COUNT(*) FROM agent_tokens"},
		{"roles table", "SELECT COUNT(*) FROM roles"},
		{"permissions table", "SELECT COUNT(*) FROM permissions"},
		{"user_roles table", "SELECT COUNT(*) FROM user_roles"},
		{"audit_logs table", "SELECT COUNT(*) FROM audit_logs"},
		{"refresh_tokens table", "SELECT COUNT(*) FROM refresh_tokens"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var count int
			if err := store.db.QueryRowContext(ctx, tc.query).Scan(&count); err != nil {
				t.Fatalf("table should exist: %v", err)
			}
		})
	}

	// Verify system roles seeded
	t.Run("admin role seeded", func(t *testing.T) {
		role, err := store.GetRoleByName(ctx, "admin")
		if err != nil {
			t.Fatalf("get admin role: %v", err)
		}
		if role == nil {
			t.Fatal("admin role not found")
		}
		if !role.IsSystem {
			t.Error("admin role should be system role")
		}
		if len(role.Permissions) != 1 {
			t.Fatalf("expected 1 permission, got %d", len(role.Permissions))
		}
		if role.Permissions[0].Verbs != "*" {
			t.Errorf("expected verbs '*', got %q", role.Permissions[0].Verbs)
		}
	})

	t.Run("viewer role seeded", func(t *testing.T) {
		role, err := store.GetRoleByName(ctx, "viewer")
		if err != nil {
			t.Fatalf("get viewer role: %v", err)
		}
		if role == nil {
			t.Fatal("viewer role not found")
		}
		if !role.IsSystem {
			t.Error("viewer role should be system role")
		}
		if len(role.Permissions) != 1 {
			t.Fatalf("expected 1 permission, got %d", len(role.Permissions))
		}
		if role.Permissions[0].Verbs != "get,list,describe,logs" {
			t.Errorf("expected verbs 'get,list,describe,logs', got %q", role.Permissions[0].Verbs)
		}
	})

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
			c.KubernetesVersion = "1.28.0"
			c.NodeCount = 5
			now := time.Now()
			c.LastSeenAt = &now
			if err := store.UpdateCluster(ctx, c); err != nil {
				t.Fatalf("update cluster: %v", err)
			}
			got, _ := store.GetClusterByID(ctx, c.ID)
			if got.Status != "connected" {
				t.Errorf("expected status connected, got %q", got.Status)
			}
			if got.KubernetesVersion != "1.28.0" {
				t.Errorf("expected k8s 1.28.0, got %q", got.KubernetesVersion)
			}
			if got.NodeCount != 5 {
				t.Errorf("expected 5 nodes, got %d", got.NodeCount)
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

func TestSQLiteStore_Roles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and get by ID", func(t *testing.T) {
			role := &Role{Name: "editor", Description: "Can edit"}
			if err := store.CreateRole(ctx, role); err != nil {
				t.Fatalf("create role: %v", err)
			}
			got, err := store.GetRoleByID(ctx, role.ID)
			if err != nil {
				t.Fatalf("get role: %v", err)
			}
			if got.Name != "editor" {
				t.Errorf("expected name editor, got %q", got.Name)
			}
		}},
		{"get by name", func(t *testing.T) {
			got, err := store.GetRoleByName(ctx, "admin")
			if err != nil {
				t.Fatalf("get role by name: %v", err)
			}
			if got == nil {
				t.Fatal("admin role not found")
			}
			if !got.IsSystem {
				t.Error("admin should be system role")
			}
		}},
		{"get non-existent returns nil", func(t *testing.T) {
			got, err := store.GetRoleByID(ctx, "nonexistent")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != nil {
				t.Error("expected nil for non-existent role")
			}
		}},
		{"list roles with permissions", func(t *testing.T) {
			roles, err := store.ListRoles(ctx)
			if err != nil {
				t.Fatalf("list roles: %v", err)
			}
			if len(roles) < 2 {
				t.Fatalf("expected at least 2 roles, got %d", len(roles))
			}
			// Check system roles have permissions loaded
			for _, r := range roles {
				if r.IsSystem && len(r.Permissions) == 0 {
					t.Errorf("system role %q should have permissions", r.Name)
				}
			}
		}},
		{"update role", func(t *testing.T) {
			role := &Role{Name: "updatable", Description: "Before"}
			store.CreateRole(ctx, role)
			role.Description = "After"
			if err := store.UpdateRole(ctx, role); err != nil {
				t.Fatalf("update role: %v", err)
			}
			got, _ := store.GetRoleByID(ctx, role.ID)
			if got.Description != "After" {
				t.Errorf("expected description After, got %q", got.Description)
			}
		}},
		{"delete non-system role", func(t *testing.T) {
			role := &Role{Name: "deletable", Description: "To delete"}
			store.CreateRole(ctx, role)
			if err := store.DeleteRole(ctx, role.ID); err != nil {
				t.Fatalf("delete role: %v", err)
			}
			got, _ := store.GetRoleByID(ctx, role.ID)
			if got != nil {
				t.Error("role should be deleted")
			}
		}},
		{"cannot delete system role", func(t *testing.T) {
			// Attempt to delete admin role
			store.DeleteRole(ctx, adminRoleID)
			got, _ := store.GetRoleByID(ctx, adminRoleID)
			if got == nil {
				t.Error("system role should not be deleted")
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteStore_Permissions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	role := &Role{Name: "perm-test-role"}
	store.CreateRole(ctx, role)

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"create and list by role", func(t *testing.T) {
			perm := &Permission{
				RoleID:           role.ID,
				ClusterPattern:   "prod-*",
				NamespacePattern: "default",
				ResourcePattern:  "pods",
				Verbs:            "get,list",
			}
			if err := store.CreatePermission(ctx, perm); err != nil {
				t.Fatalf("create permission: %v", err)
			}
			perms, err := store.ListPermissionsByRole(ctx, role.ID)
			if err != nil {
				t.Fatalf("list permissions: %v", err)
			}
			if len(perms) != 1 {
				t.Fatalf("expected 1 permission, got %d", len(perms))
			}
			if perms[0].ClusterPattern != "prod-*" {
				t.Errorf("expected cluster_pattern 'prod-*', got %q", perms[0].ClusterPattern)
			}
		}},
		{"delete permission", func(t *testing.T) {
			perm := &Permission{
				RoleID: role.ID, ClusterPattern: "*",
				NamespacePattern: "*", ResourcePattern: "*", Verbs: "*",
			}
			store.CreatePermission(ctx, perm)
			if err := store.DeletePermission(ctx, perm.ID); err != nil {
				t.Fatalf("delete permission: %v", err)
			}
			perms, _ := store.ListPermissionsByRole(ctx, role.ID)
			for _, p := range perms {
				if p.ID == perm.ID {
					t.Error("permission should be deleted")
				}
			}
		}},
		{"delete by role", func(t *testing.T) {
			r2 := &Role{Name: "perm-del-role"}
			store.CreateRole(ctx, r2)
			store.CreatePermission(ctx, &Permission{
				RoleID: r2.ID, ClusterPattern: "*",
				NamespacePattern: "*", ResourcePattern: "*", Verbs: "*",
			})
			store.CreatePermission(ctx, &Permission{
				RoleID: r2.ID, ClusterPattern: "dev",
				NamespacePattern: "*", ResourcePattern: "*", Verbs: "get",
			})
			if err := store.DeletePermissionsByRole(ctx, r2.ID); err != nil {
				t.Fatalf("delete permissions by role: %v", err)
			}
			perms, _ := store.ListPermissionsByRole(ctx, r2.ID)
			if len(perms) != 0 {
				t.Errorf("expected 0 permissions, got %d", len(perms))
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

func TestSQLiteStore_UserRoles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user := &User{Email: "ur@test.com", PasswordHash: "h", Name: "UR", IsActive: true}
	store.CreateUser(ctx, user)
	role := &Role{Name: "custom-role"}
	store.CreateRole(ctx, role)

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"assign and list roles by user", func(t *testing.T) {
			if err := store.AssignRole(ctx, user.ID, adminRoleID, ""); err != nil {
				t.Fatalf("assign role: %v", err)
			}
			roles, err := store.ListRolesByUser(ctx, user.ID)
			if err != nil {
				t.Fatalf("list roles by user: %v", err)
			}
			if len(roles) != 1 {
				t.Fatalf("expected 1 role, got %d", len(roles))
			}
			if roles[0].Name != "admin" {
				t.Errorf("expected role admin, got %q", roles[0].Name)
			}
		}},
		{"list users by role", func(t *testing.T) {
			users, err := store.ListUsersByRole(ctx, adminRoleID)
			if err != nil {
				t.Fatalf("list users by role: %v", err)
			}
			if len(users) != 1 {
				t.Fatalf("expected 1 user, got %d", len(users))
			}
			if users[0].Email != "ur@test.com" {
				t.Errorf("expected email ur@test.com, got %q", users[0].Email)
			}
		}},
		{"assign multiple roles", func(t *testing.T) {
			store.AssignRole(ctx, user.ID, role.ID, "")
			roles, _ := store.ListRolesByUser(ctx, user.ID)
			if len(roles) != 2 {
				t.Errorf("expected 2 roles, got %d", len(roles))
			}
		}},
		{"unassign role", func(t *testing.T) {
			if err := store.UnassignRole(ctx, user.ID, role.ID); err != nil {
				t.Fatalf("unassign role: %v", err)
			}
			roles, _ := store.ListRolesByUser(ctx, user.ID)
			if len(roles) != 1 {
				t.Errorf("expected 1 role after unassign, got %d", len(roles))
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

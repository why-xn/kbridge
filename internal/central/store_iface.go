package central

import (
	"context"
	"time"
)

// Store defines the persistence interface for kbridge.
type Store interface {
	// Users
	CreateUser(ctx context.Context, user *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	ListUsers(ctx context.Context) ([]*User, error)
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, id string) error

	// Clusters
	CreateCluster(ctx context.Context, cluster *Cluster) error
	GetClusterByID(ctx context.Context, id string) (*Cluster, error)
	GetClusterByName(ctx context.Context, name string) (*Cluster, error)
	ListClusters(ctx context.Context) ([]*Cluster, error)
	UpdateCluster(ctx context.Context, cluster *Cluster) error
	DeleteCluster(ctx context.Context, id string) error

	// Agent Tokens
	CreateAgentToken(ctx context.Context, token *AgentToken) error
	GetAgentTokenByHash(ctx context.Context, tokenHash string) (*AgentToken, error)
	ListAgentTokensByCluster(ctx context.Context, clusterID string) ([]*AgentToken, error)
	RevokeAgentToken(ctx context.Context, id string) error

	// Roles
	CreateRole(ctx context.Context, role *Role) error
	GetRoleByID(ctx context.Context, id string) (*Role, error)
	GetRoleByName(ctx context.Context, name string) (*Role, error)
	ListRoles(ctx context.Context) ([]*Role, error)
	UpdateRole(ctx context.Context, role *Role) error
	DeleteRole(ctx context.Context, id string) error

	// Permissions
	CreatePermission(ctx context.Context, perm *Permission) error
	ListPermissionsByRole(ctx context.Context, roleID string) ([]*Permission, error)
	DeletePermission(ctx context.Context, id string) error
	DeletePermissionsByRole(ctx context.Context, roleID string) error

	// User-Role Assignments
	AssignRole(ctx context.Context, userID, roleID, assignedBy string) error
	UnassignRole(ctx context.Context, userID, roleID string) error
	ListRolesByUser(ctx context.Context, userID string) ([]*Role, error)
	ListUsersByRole(ctx context.Context, roleID string) ([]*User, error)

	// Refresh Tokens
	CreateRefreshToken(ctx context.Context, rt *RefreshToken) error
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	DeleteRefreshToken(ctx context.Context, id string) error
	DeleteRefreshTokensByUser(ctx context.Context, userID string) error
	CleanupExpiredRefreshTokens(ctx context.Context) error

	// Audit Logs
	CreateAuditLog(ctx context.Context, log *AuditLog) error
	ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]*AuditLog, int, error)
	CleanupOldAuditLogs(ctx context.Context, before time.Time) (int, error)

	// Lifecycle
	Migrate(ctx context.Context) error
	Close() error
}

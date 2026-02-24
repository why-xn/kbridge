package central

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const timeFormat = "2006-01-02T15:04:05Z"

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a SQLite database and returns a store.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("set journal mode: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Migrate creates the schema and seeds system roles.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	if err := createSchema(s.db); err != nil {
		return err
	}
	return seedSystemRoles(s.db)
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func parseNullableTime(str *string) *time.Time {
	if str == nil || *str == "" {
		return nil
	}
	t, err := time.Parse(timeFormat, *str)
	if err != nil {
		return nil
	}
	return &t
}

func formatNullableTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(timeFormat)
	return &s
}

// --- Users ---

func (s *SQLiteStore) CreateUser(ctx context.Context, user *User) error {
	if user.ID == "" {
		user.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, name, is_active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Email, user.PasswordHash, user.Name, user.IsActive, now, now,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	user.CreatedAt, _ = time.Parse(timeFormat, now)
	user.UpdatedAt = user.CreatedAt
	return nil
}

func (s *SQLiteStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, is_active, created_at, updated_at
		 FROM users WHERE id = ?`, id))
}

func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, is_active, created_at, updated_at
		 FROM users WHERE email = ?`, email))
}

func (s *SQLiteStore) scanUser(row *sql.Row) (*User, error) {
	var u User
	var isActive int
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &isActive, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.IsActive = isActive != 0
	u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &u, nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, password_hash, name, is_active, created_at, updated_at FROM users`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	return s.scanUsers(rows)
}

func (s *SQLiteStore) scanUsers(rows *sql.Rows) ([]*User, error) {
	var users []*User
	for rows.Next() {
		var u User
		var isActive int
		var createdAt, updatedAt string
		err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &isActive, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		u.IsActive = isActive != 0
		u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		users = append(users, &u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateUser(ctx context.Context, user *User) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = ?, is_active = ?, updated_at = ? WHERE id = ?`,
		user.Email, user.Name, user.PasswordHash, user.IsActive, now, user.ID,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	user.UpdatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// --- Clusters ---

func (s *SQLiteStore) CreateCluster(ctx context.Context, cluster *Cluster) error {
	if cluster.ID == "" {
		cluster.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO clusters (id, name, status, agent_id, kubernetes_version, node_count, region, provider, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cluster.ID, cluster.Name, cluster.Status, nilIfEmpty(cluster.AgentID),
		nilIfEmpty(cluster.KubernetesVersion), cluster.NodeCount,
		nilIfEmpty(cluster.Region), nilIfEmpty(cluster.Provider),
		formatNullableTime(cluster.LastSeenAt), now, now,
	)
	if err != nil {
		return fmt.Errorf("create cluster: %w", err)
	}
	cluster.CreatedAt, _ = time.Parse(timeFormat, now)
	cluster.UpdatedAt = cluster.CreatedAt
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *SQLiteStore) GetClusterByID(ctx context.Context, id string) (*Cluster, error) {
	return s.scanCluster(s.db.QueryRowContext(ctx,
		`SELECT id, name, status, agent_id, kubernetes_version, node_count, region, provider, last_seen_at, created_at, updated_at
		 FROM clusters WHERE id = ?`, id))
}

func (s *SQLiteStore) GetClusterByName(ctx context.Context, name string) (*Cluster, error) {
	return s.scanCluster(s.db.QueryRowContext(ctx,
		`SELECT id, name, status, agent_id, kubernetes_version, node_count, region, provider, last_seen_at, created_at, updated_at
		 FROM clusters WHERE name = ?`, name))
}

func (s *SQLiteStore) scanCluster(row *sql.Row) (*Cluster, error) {
	var c Cluster
	var agentID, k8sVer, region, provider, lastSeen *string
	var createdAt, updatedAt string
	err := row.Scan(&c.ID, &c.Name, &c.Status, &agentID, &k8sVer,
		&c.NodeCount, &region, &provider, &lastSeen, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan cluster: %w", err)
	}
	c.AgentID = derefStr(agentID)
	c.KubernetesVersion = derefStr(k8sVer)
	c.Region = derefStr(region)
	c.Provider = derefStr(provider)
	c.LastSeenAt = parseNullableTime(lastSeen)
	c.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	c.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &c, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *SQLiteStore) ListClusters(ctx context.Context) ([]*Cluster, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, status, agent_id, kubernetes_version, node_count, region, provider, last_seen_at, created_at, updated_at
		 FROM clusters`)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	defer rows.Close()

	var clusters []*Cluster
	for rows.Next() {
		var c Cluster
		var agentID, k8sVer, region, provider, lastSeen *string
		var createdAt, updatedAt string
		err := rows.Scan(&c.ID, &c.Name, &c.Status, &agentID, &k8sVer,
			&c.NodeCount, &region, &provider, &lastSeen, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan cluster row: %w", err)
		}
		c.AgentID = derefStr(agentID)
		c.KubernetesVersion = derefStr(k8sVer)
		c.Region = derefStr(region)
		c.Provider = derefStr(provider)
		c.LastSeenAt = parseNullableTime(lastSeen)
		c.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		c.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		clusters = append(clusters, &c)
	}
	return clusters, rows.Err()
}

func (s *SQLiteStore) UpdateCluster(ctx context.Context, cluster *Cluster) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`UPDATE clusters SET name = ?, status = ?, agent_id = ?, kubernetes_version = ?,
		 node_count = ?, region = ?, provider = ?, last_seen_at = ?, updated_at = ?
		 WHERE id = ?`,
		cluster.Name, cluster.Status, nilIfEmpty(cluster.AgentID),
		nilIfEmpty(cluster.KubernetesVersion), cluster.NodeCount,
		nilIfEmpty(cluster.Region), nilIfEmpty(cluster.Provider),
		formatNullableTime(cluster.LastSeenAt), now, cluster.ID,
	)
	if err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	cluster.UpdatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) DeleteCluster(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM clusters WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete cluster: %w", err)
	}
	return nil
}

// --- Agent Tokens ---

func (s *SQLiteStore) CreateAgentToken(ctx context.Context, token *AgentToken) error {
	if token.ID == "" {
		token.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_tokens (id, cluster_id, token_hash, token_prefix, description, is_revoked, last_used_at, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		token.ID, token.ClusterID, token.TokenHash, token.TokenPrefix,
		nilIfEmpty(token.Description), token.IsRevoked,
		formatNullableTime(token.LastUsedAt), formatNullableTime(token.ExpiresAt), now,
	)
	if err != nil {
		return fmt.Errorf("create agent token: %w", err)
	}
	token.CreatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) GetAgentTokenByHash(ctx context.Context, tokenHash string) (*AgentToken, error) {
	var t AgentToken
	var isRevoked int
	var desc, lastUsed, expiresAt *string
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, cluster_id, token_hash, token_prefix, description, is_revoked, last_used_at, expires_at, created_at
		 FROM agent_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&t.ID, &t.ClusterID, &t.TokenHash, &t.TokenPrefix, &desc,
		&isRevoked, &lastUsed, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent token: %w", err)
	}
	t.Description = derefStr(desc)
	t.IsRevoked = isRevoked != 0
	t.LastUsedAt = parseNullableTime(lastUsed)
	t.ExpiresAt = parseNullableTime(expiresAt)
	t.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	return &t, nil
}

func (s *SQLiteStore) ListAgentTokensByCluster(ctx context.Context, clusterID string) ([]*AgentToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, cluster_id, token_hash, token_prefix, description, is_revoked, last_used_at, expires_at, created_at
		 FROM agent_tokens WHERE cluster_id = ?`, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list agent tokens: %w", err)
	}
	defer rows.Close()

	var tokens []*AgentToken
	for rows.Next() {
		var t AgentToken
		var isRevoked int
		var desc, lastUsed, expiresAt *string
		var createdAt string
		err := rows.Scan(&t.ID, &t.ClusterID, &t.TokenHash, &t.TokenPrefix, &desc,
			&isRevoked, &lastUsed, &expiresAt, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan agent token row: %w", err)
		}
		t.Description = derefStr(desc)
		t.IsRevoked = isRevoked != 0
		t.LastUsedAt = parseNullableTime(lastUsed)
		t.ExpiresAt = parseNullableTime(expiresAt)
		t.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		tokens = append(tokens, &t)
	}
	return tokens, rows.Err()
}

func (s *SQLiteStore) RevokeAgentToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET is_revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("revoke agent token: %w", err)
	}
	return nil
}

// --- Roles ---

func (s *SQLiteStore) CreateRole(ctx context.Context, role *Role) error {
	if role.ID == "" {
		role.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO roles (id, name, description, is_system, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		role.ID, role.Name, role.Description, role.IsSystem, now, now,
	)
	if err != nil {
		return fmt.Errorf("create role: %w", err)
	}
	role.CreatedAt, _ = time.Parse(timeFormat, now)
	role.UpdatedAt = role.CreatedAt
	return nil
}

func (s *SQLiteStore) GetRoleByID(ctx context.Context, id string) (*Role, error) {
	role, err := s.scanRole(s.db.QueryRowContext(ctx,
		`SELECT id, name, description, is_system, created_at, updated_at
		 FROM roles WHERE id = ?`, id))
	if err != nil || role == nil {
		return role, err
	}
	role.Permissions, err = s.loadPermissions(ctx, role.ID)
	return role, err
}

func (s *SQLiteStore) GetRoleByName(ctx context.Context, name string) (*Role, error) {
	role, err := s.scanRole(s.db.QueryRowContext(ctx,
		`SELECT id, name, description, is_system, created_at, updated_at
		 FROM roles WHERE name = ?`, name))
	if err != nil || role == nil {
		return role, err
	}
	role.Permissions, err = s.loadPermissions(ctx, role.ID)
	return role, err
}

func (s *SQLiteStore) scanRole(row *sql.Row) (*Role, error) {
	var r Role
	var isSystem int
	var createdAt, updatedAt string
	err := row.Scan(&r.ID, &r.Name, &r.Description, &isSystem, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan role: %w", err)
	}
	r.IsSystem = isSystem != 0
	r.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	r.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &r, nil
}

func (s *SQLiteStore) loadPermissions(ctx context.Context, roleID string) ([]Permission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role_id, cluster_pattern, namespace_pattern, resource_pattern, verbs
		 FROM permissions WHERE role_id = ?`, roleID)
	if err != nil {
		return nil, fmt.Errorf("load permissions: %w", err)
	}
	defer rows.Close()

	var perms []Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.RoleID, &p.ClusterPattern, &p.NamespacePattern, &p.ResourcePattern, &p.Verbs); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

func (s *SQLiteStore) ListRoles(ctx context.Context) ([]*Role, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, is_system, created_at, updated_at FROM roles`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	var roles []*Role
	for rows.Next() {
		var r Role
		var isSystem int
		var createdAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &isSystem, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan role row: %w", err)
		}
		r.IsSystem = isSystem != 0
		r.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		r.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		roles = append(roles, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, role := range roles {
		role.Permissions, err = s.loadPermissions(ctx, role.ID)
		if err != nil {
			return nil, err
		}
	}
	return roles, nil
}

func (s *SQLiteStore) UpdateRole(ctx context.Context, role *Role) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`UPDATE roles SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		role.Name, role.Description, now, role.ID,
	)
	if err != nil {
		return fmt.Errorf("update role: %w", err)
	}
	role.UpdatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) DeleteRole(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM roles WHERE id = ? AND is_system = 0`, id)
	if err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	return nil
}

// --- Permissions ---

func (s *SQLiteStore) CreatePermission(ctx context.Context, perm *Permission) error {
	if perm.ID == "" {
		perm.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO permissions (id, role_id, cluster_pattern, namespace_pattern, resource_pattern, verbs)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		perm.ID, perm.RoleID, perm.ClusterPattern, perm.NamespacePattern, perm.ResourcePattern, perm.Verbs,
	)
	if err != nil {
		return fmt.Errorf("create permission: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListPermissionsByRole(ctx context.Context, roleID string) ([]*Permission, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, role_id, cluster_pattern, namespace_pattern, resource_pattern, verbs
		 FROM permissions WHERE role_id = ?`, roleID)
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	defer rows.Close()

	var perms []*Permission
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.ID, &p.RoleID, &p.ClusterPattern, &p.NamespacePattern, &p.ResourcePattern, &p.Verbs); err != nil {
			return nil, fmt.Errorf("scan permission row: %w", err)
		}
		perms = append(perms, &p)
	}
	return perms, rows.Err()
}

func (s *SQLiteStore) DeletePermission(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM permissions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete permission: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeletePermissionsByRole(ctx context.Context, roleID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM permissions WHERE role_id = ?`, roleID)
	if err != nil {
		return fmt.Errorf("delete permissions by role: %w", err)
	}
	return nil
}

// --- User-Role Assignments ---

func (s *SQLiteStore) AssignRole(ctx context.Context, userID, roleID, assignedBy string) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_roles (user_id, role_id, assigned_at, assigned_by)
		 VALUES (?, ?, ?, ?)`,
		userID, roleID, now, nilIfEmpty(assignedBy),
	)
	if err != nil {
		return fmt.Errorf("assign role: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UnassignRole(ctx context.Context, userID, roleID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_roles WHERE user_id = ? AND role_id = ?`, userID, roleID)
	if err != nil {
		return fmt.Errorf("unassign role: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListRolesByUser(ctx context.Context, userID string) ([]*Role, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.id, r.name, r.description, r.is_system, r.created_at, r.updated_at
		 FROM roles r JOIN user_roles ur ON r.id = ur.role_id
		 WHERE ur.user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("list roles by user: %w", err)
	}
	defer rows.Close()

	var roles []*Role
	for rows.Next() {
		var r Role
		var isSystem int
		var createdAt, updatedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &isSystem, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan role row: %w", err)
		}
		r.IsSystem = isSystem != 0
		r.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		r.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		roles = append(roles, &r)
	}
	return roles, rows.Err()
}

func (s *SQLiteStore) ListUsersByRole(ctx context.Context, roleID string) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.email, u.password_hash, u.name, u.is_active, u.created_at, u.updated_at
		 FROM users u JOIN user_roles ur ON u.id = ur.user_id
		 WHERE ur.role_id = ?`, roleID)
	if err != nil {
		return nil, fmt.Errorf("list users by role: %w", err)
	}
	defer rows.Close()
	return s.scanUsers(rows)
}

// --- Refresh Tokens ---

func (s *SQLiteStore) CreateRefreshToken(ctx context.Context, rt *RefreshToken) error {
	if rt.ID == "" {
		rt.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		rt.ID, rt.UserID, rt.TokenHash, rt.ExpiresAt.UTC().Format(timeFormat), now,
	)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	rt.CreatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var rt RefreshToken
	var expiresAt, createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, expires_at, created_at
		 FROM refresh_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get refresh token: %w", err)
	}
	rt.ExpiresAt, _ = time.Parse(timeFormat, expiresAt)
	rt.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	return &rt, nil
}

func (s *SQLiteStore) DeleteRefreshToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete refresh token: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteRefreshTokensByUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete refresh tokens by user: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CleanupExpiredRefreshTokens(ctx context.Context) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE expires_at < ?`, now)
	if err != nil {
		return fmt.Errorf("cleanup expired refresh tokens: %w", err)
	}
	return nil
}

// --- Audit Logs ---

func (s *SQLiteStore) CreateAuditLog(ctx context.Context, log *AuditLog) error {
	if log.ID == "" {
		log.ID = uuid.New().String()
	}
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_logs (id, user_id, user_email, cluster_name, cluster_id, command, namespace, status, exit_code, duration_ms, error_message, client_ip, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		log.ID, nilIfEmpty(log.UserID), log.UserEmail, log.ClusterName,
		nilIfEmpty(log.ClusterID), log.Command, nilIfEmpty(log.Namespace),
		log.Status, log.ExitCode, log.DurationMs,
		nilIfEmpty(log.ErrorMessage), nilIfEmpty(log.ClientIP), now,
	)
	if err != nil {
		return fmt.Errorf("create audit log: %w", err)
	}
	log.CreatedAt, _ = time.Parse(timeFormat, now)
	return nil
}

func (s *SQLiteStore) ListAuditLogs(ctx context.Context, filter AuditLogFilter) ([]*AuditLog, int, error) {
	where, args := buildAuditFilter(filter)

	// Count total
	countQuery := "SELECT COUNT(*) FROM audit_logs" + where
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	// Fetch page
	query := `SELECT id, user_id, user_email, cluster_name, cluster_id, command, namespace, status, exit_code, duration_ms, error_message, client_ip, created_at
		 FROM audit_logs` + where + ` ORDER BY created_at DESC`
	pageArgs := append([]any{}, args...)
	if filter.PerPage > 0 {
		query += " LIMIT ? OFFSET ?"
		offset := 0
		if filter.Page > 1 {
			offset = (filter.Page - 1) * filter.PerPage
		}
		pageArgs = append(pageArgs, filter.PerPage, offset)
	}

	rows, err := s.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*AuditLog
	for rows.Next() {
		log, err := scanAuditRow(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, log)
	}
	return logs, total, rows.Err()
}

func buildAuditFilter(f AuditLogFilter) (string, []any) {
	var clauses []string
	var args []any
	if f.UserEmail != "" {
		clauses = append(clauses, "user_email = ?")
		args = append(args, f.UserEmail)
	}
	if f.ClusterName != "" {
		clauses = append(clauses, "cluster_name = ?")
		args = append(args, f.ClusterName)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	if f.From != nil {
		clauses = append(clauses, "created_at >= ?")
		args = append(args, f.From.UTC().Format(timeFormat))
	}
	if f.To != nil {
		clauses = append(clauses, "created_at <= ?")
		args = append(args, f.To.UTC().Format(timeFormat))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanAuditRow(rows *sql.Rows) (*AuditLog, error) {
	var l AuditLog
	var userID, clusterID, ns, errMsg, clientIP *string
	var createdAt string
	err := rows.Scan(&l.ID, &userID, &l.UserEmail, &l.ClusterName, &clusterID,
		&l.Command, &ns, &l.Status, &l.ExitCode, &l.DurationMs,
		&errMsg, &clientIP, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("scan audit log: %w", err)
	}
	l.UserID = derefStr(userID)
	l.ClusterID = derefStr(clusterID)
	l.Namespace = derefStr(ns)
	l.ErrorMessage = derefStr(errMsg)
	l.ClientIP = derefStr(clientIP)
	l.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	return &l, nil
}

func (s *SQLiteStore) CleanupOldAuditLogs(ctx context.Context, before time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM audit_logs WHERE created_at < ?`, before.UTC().Format(timeFormat))
	if err != nil {
		return 0, fmt.Errorf("cleanup old audit logs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(n), nil
}

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

// Migrate creates the schema.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return createSchema(s.db)
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
		`INSERT INTO users (id, email, password_hash, name, is_active, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Email, user.PasswordHash, user.Name, user.IsActive, user.IsAdmin, now, now,
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
		`SELECT id, email, password_hash, name, is_active, is_admin, created_at, updated_at
		 FROM users WHERE id = ?`, id))
}

func (s *SQLiteStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, name, is_active, is_admin, created_at, updated_at
		 FROM users WHERE email = ?`, email))
}

func (s *SQLiteStore) scanUser(row *sql.Row) (*User, error) {
	var u User
	var isActive, isAdmin int
	var createdAt, updatedAt string
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &isActive, &isAdmin, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.IsActive = isActive != 0
	u.IsAdmin = isAdmin != 0
	u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
	u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
	return &u, nil
}

func (s *SQLiteStore) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, password_hash, name, is_active, is_admin, created_at, updated_at FROM users`)
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
		var isActive, isAdmin int
		var createdAt, updatedAt string
		err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &isActive, &isAdmin, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		u.IsActive = isActive != 0
		u.IsAdmin = isAdmin != 0
		u.CreatedAt, _ = time.Parse(timeFormat, createdAt)
		u.UpdatedAt, _ = time.Parse(timeFormat, updatedAt)
		users = append(users, &u)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateUser(ctx context.Context, user *User) error {
	now := time.Now().UTC().Format(timeFormat)
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ?, password_hash = ?, is_active = ?, is_admin = ?, updated_at = ? WHERE id = ?`,
		user.Email, user.Name, user.PasswordHash, user.IsActive, user.IsAdmin, now, user.ID,
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
		`INSERT INTO clusters (id, name, status, agent_id, last_seen_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cluster.ID, cluster.Name, cluster.Status, nilIfEmpty(cluster.AgentID),
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
		`SELECT id, name, status, agent_id, last_seen_at, created_at, updated_at
		 FROM clusters WHERE id = ?`, id))
}

func (s *SQLiteStore) GetClusterByName(ctx context.Context, name string) (*Cluster, error) {
	return s.scanCluster(s.db.QueryRowContext(ctx,
		`SELECT id, name, status, agent_id, last_seen_at, created_at, updated_at
		 FROM clusters WHERE name = ?`, name))
}

func (s *SQLiteStore) scanCluster(row *sql.Row) (*Cluster, error) {
	var c Cluster
	var agentID, lastSeen *string
	var createdAt, updatedAt string
	err := row.Scan(&c.ID, &c.Name, &c.Status, &agentID, &lastSeen, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan cluster: %w", err)
	}
	c.AgentID = derefStr(agentID)
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
		`SELECT id, name, status, agent_id, last_seen_at, created_at, updated_at
		 FROM clusters`)
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}
	defer rows.Close()

	var clusters []*Cluster
	for rows.Next() {
		var c Cluster
		var agentID, lastSeen *string
		var createdAt, updatedAt string
		err := rows.Scan(&c.ID, &c.Name, &c.Status, &agentID, &lastSeen, &createdAt, &updatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan cluster row: %w", err)
		}
		c.AgentID = derefStr(agentID)
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
		`UPDATE clusters SET name = ?, status = ?, agent_id = ?, last_seen_at = ?, updated_at = ?
		 WHERE id = ?`,
		cluster.Name, cluster.Status, nilIfEmpty(cluster.AgentID),
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

func (s *SQLiteStore) TouchAgentToken(ctx context.Context, id string, usedAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE agent_tokens SET last_used_at = ? WHERE id = ?`,
		usedAt.UTC().Format(timeFormat), id)
	if err != nil {
		return fmt.Errorf("touch agent token: %w", err)
	}
	return nil
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

package central

import (
	"database/sql"
	"fmt"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 1,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS clusters (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL UNIQUE,
    status             TEXT NOT NULL DEFAULT 'disconnected',
    agent_id           TEXT,
    kubernetes_version TEXT,
    node_count         INTEGER,
    region             TEXT,
    provider           TEXT,
    last_seen_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_clusters_name ON clusters(name);
CREATE INDEX IF NOT EXISTS idx_clusters_status ON clusters(status);

CREATE TABLE IF NOT EXISTS agent_tokens (
    id           TEXT PRIMARY KEY,
    cluster_id   TEXT NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    description  TEXT,
    is_revoked   INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT,
    expires_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_agent_tokens_cluster_id ON agent_tokens(cluster_id);
CREATE INDEX IF NOT EXISTS idx_agent_tokens_token_hash ON agent_tokens(token_hash);

CREATE TABLE IF NOT EXISTS roles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    is_system   INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_roles_name ON roles(name);

CREATE TABLE IF NOT EXISTS permissions (
    id                TEXT PRIMARY KEY,
    role_id           TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    cluster_pattern   TEXT NOT NULL DEFAULT '*',
    namespace_pattern TEXT NOT NULL DEFAULT '*',
    resource_pattern  TEXT NOT NULL DEFAULT '*',
    verbs             TEXT NOT NULL DEFAULT '*'
);
CREATE INDEX IF NOT EXISTS idx_permissions_role_id ON permissions(role_id);

CREATE TABLE IF NOT EXISTS user_roles (
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id     TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    assigned_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    assigned_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, role_id)
);
CREATE INDEX IF NOT EXISTS idx_user_roles_user_id ON user_roles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_roles_role_id ON user_roles(role_id);

CREATE TABLE IF NOT EXISTS audit_logs (
    id            TEXT PRIMARY KEY,
    user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
    user_email    TEXT NOT NULL,
    cluster_name  TEXT NOT NULL,
    cluster_id    TEXT REFERENCES clusters(id) ON DELETE SET NULL,
    command       TEXT NOT NULL,
    namespace     TEXT,
    status        TEXT NOT NULL,
    exit_code     INTEGER,
    duration_ms   INTEGER,
    error_message TEXT,
    client_ip     TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_cluster_id ON audit_logs(cluster_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_logs_user_email ON audit_logs(user_email);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash ON refresh_tokens(token_hash);
`

// System role IDs (fixed for idempotency).
const (
	adminRoleID      = "00000000-0000-0000-0000-000000000001"
	viewerRoleID     = "00000000-0000-0000-0000-000000000002"
	adminPermID      = "00000000-0000-0000-0000-000000000003"
	viewerPermID     = "00000000-0000-0000-0000-000000000004"
)

func createSchema(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

func seedSystemRoles(db *sql.DB) error {
	if err := seedRole(db, adminRoleID, "admin", "Full administrative access", adminPermID, "*"); err != nil {
		return err
	}
	return seedRole(db, viewerRoleID, "viewer", "Read-only access", viewerPermID, "get,list,describe,logs")
}

func seedRole(db *sql.DB, roleID, name, desc, permID, verbs string) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO roles (id, name, description, is_system) VALUES (?, ?, ?, 1)`,
		roleID, name, desc,
	)
	if err != nil {
		return fmt.Errorf("seed role %s: %w", name, err)
	}
	_, err = db.Exec(
		`INSERT OR IGNORE INTO permissions (id, role_id, cluster_pattern, namespace_pattern, resource_pattern, verbs)
		 VALUES (?, ?, '*', '*', '*', ?)`,
		permID, roleID, verbs,
	)
	if err != nil {
		return fmt.Errorf("seed permission for %s: %w", name, err)
	}
	return nil
}

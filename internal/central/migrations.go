package central

import (
	"database/sql"
	"fmt"
	"strings"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 1,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email);

CREATE TABLE IF NOT EXISTS clusters (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'disconnected',
    agent_id     TEXT,
    last_seen_at TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
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

func createSchema(db *sql.DB) error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	// Add is_admin to existing DBs; ignore "duplicate column" on fresh ones.
	if err := addIsAdminColumn(db); err != nil {
		return err
	}
	// Drop obsolete tables if they exist (no-op on fresh DBs).
	for _, tbl := range []string{"user_roles", "permissions", "roles"} {
		if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl); err != nil {
			return fmt.Errorf("drop table %s: %w", tbl, err)
		}
	}
	return nil
}

func addIsAdminColumn(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("add is_admin column: %w", err)
	}
	return nil
}

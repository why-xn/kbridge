package central

import "time"

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Name         string    `json:"name"`
	IsActive     bool      `json:"is_active"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Cluster struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	AgentID    string     `json:"agent_id,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type AgentToken struct {
	ID          string     `json:"id"`
	ClusterID   string     `json:"cluster_id"`
	ClusterName string     `json:"cluster_name,omitempty"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"token_prefix"`
	Description string     `json:"description,omitempty"`
	IsRevoked   bool       `json:"is_revoked"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AuditLog struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id,omitempty"`
	UserEmail    string    `json:"user_email"`
	ClusterName  string    `json:"cluster_name"`
	ClusterID    string    `json:"cluster_id,omitempty"`
	Command      string    `json:"command"`
	Namespace    string    `json:"namespace,omitempty"`
	Status       string    `json:"status"`
	ExitCode     *int32    `json:"exit_code,omitempty"`
	DurationMs   *int64    `json:"duration_ms,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ClientIP     string    `json:"client_ip,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type RefreshToken struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	TokenHash string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditLogFilter struct {
	UserEmail   string
	ClusterName string
	Status      string
	From        *time.Time
	To          *time.Time
	Page        int
	PerPage     int
}

package controlplane

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// auditColumns lists the column names of the audit_logs table.
func auditColumns(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info('audit_logs')`)
	if err != nil {
		t.Fatalf("read table info: %v", err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}
	return cols
}

func TestCreateSchema_AddsReasonToFreshDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if !auditColumns(t, db)["reason"] {
		t.Error("audit_logs is missing the reason column")
	}
}

// TestCreateSchema_UpgradesExistingDatabase covers the path that matters on a
// live deployment: a database created before guardrails existed must gain the
// reason column, because CREATE TABLE IF NOT EXISTS alone would leave it behind.
func TestCreateSchema_UpgradesExistingDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// The pre-guardrails audit_logs table.
	_, err = db.Exec(`CREATE TABLE audit_logs (
		id TEXT PRIMARY KEY, user_id TEXT, user_email TEXT NOT NULL,
		cluster_name TEXT NOT NULL, cluster_id TEXT, command TEXT NOT NULL,
		namespace TEXT, status TEXT NOT NULL, exit_code INTEGER,
		duration_ms INTEGER, error_message TEXT, client_ip TEXT,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if auditColumns(t, db)["reason"] {
		t.Fatal("legacy table already has reason; the test is not exercising the upgrade")
	}

	if err := createSchema(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !auditColumns(t, db)["reason"] {
		t.Error("upgrade did not add the reason column")
	}
}

func TestCreateSchema_IsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	for i := range 3 {
		if err := createSchema(db); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	if !auditColumns(t, db)["reason"] {
		t.Error("audit_logs is missing the reason column after repeated migration")
	}
}

func TestAuditLog_ReasonRoundTrips(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	entries := []*AuditLog{
		{UserID: user.ID, UserEmail: user.Email, ClusterName: "prod", Command: "delete pod p", Status: AuditStatusSuccess, Reason: "INC-4521 rollback"},
		{UserID: user.ID, UserEmail: user.Email, ClusterName: "prod", Command: "get pods", Status: AuditStatusSuccess},
	}
	for _, e := range entries {
		if err := store.CreateAuditLog(ctx, e); err != nil {
			t.Fatalf("create audit log: %v", err)
		}
	}

	logs, _, err := store.ListAuditLogs(ctx, AuditLogFilter{})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("entries = %d, want 2", len(logs))
	}

	byCommand := map[string]string{}
	for _, l := range logs {
		byCommand[l.Command] = l.Reason
	}
	if got := byCommand["delete pod p"]; got != "INC-4521 rollback" {
		t.Errorf("reason = %q, want the stored justification", got)
	}
	if got := byCommand["get pods"]; got != "" {
		t.Errorf("reason = %q, want empty for an entry written without one", got)
	}
}

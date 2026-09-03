package controlplane

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// tableColumns lists the column names of a table.
func tableColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
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

func TestCreateSchema_CreatesGrantsTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	cols := tableColumns(t, db, "grants")
	for _, want := range []string{"id", "subject", "cluster_name", "namespace", "status",
		"reason", "duration_ns", "requested_at", "decided_at", "decided_by", "decision_note", "expires_at"} {
		if !cols[want] {
			t.Errorf("grants table is missing column %q", want)
		}
	}
	if !tableColumns(t, db, "audit_logs")["grant_id"] {
		t.Error("audit_logs is missing the grant_id column")
	}
}

// TestCreateSchema_AddsGrantIDToExistingDatabase covers the upgrade a live
// deployment takes: a database predating grants must gain audit_logs.grant_id,
// which CREATE TABLE IF NOT EXISTS alone would not add.
func TestCreateSchema_AddsGrantIDToExistingDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE audit_logs (
		id TEXT PRIMARY KEY, user_id TEXT, user_email TEXT NOT NULL,
		cluster_name TEXT NOT NULL, cluster_id TEXT, command TEXT NOT NULL,
		namespace TEXT, status TEXT NOT NULL, exit_code INTEGER,
		duration_ms INTEGER, error_message TEXT, client_ip TEXT, reason TEXT,
		created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')))`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if tableColumns(t, db, "audit_logs")["grant_id"] {
		t.Fatal("legacy table already has grant_id; the test is not exercising the upgrade")
	}

	if err := createSchema(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !tableColumns(t, db, "audit_logs")["grant_id"] {
		t.Error("upgrade did not add the grant_id column")
	}
	if len(tableColumns(t, db, "grants")) == 0 {
		t.Error("upgrade did not create the grants table")
	}
}

func TestSQLiteStore_GrantRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	requested := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	g := &Grant{
		Subject:     "dev@corp.com",
		ClusterName: "prod-eu",
		Namespace:   "payments",
		Status:      GrantStatusPending,
		Reason:      "INC-4521 rollback",
		Duration:    2 * time.Hour,
		RequestedAt: requested,
	}
	if err := store.CreateGrant(ctx, g); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	if g.ID == "" {
		t.Fatal("create should assign an ID")
	}

	got, err := store.GetGrant(ctx, g.ID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	if got == nil {
		t.Fatal("grant not found after create")
	}
	if got.Subject != g.Subject || got.ClusterName != g.ClusterName || got.Namespace != g.Namespace {
		t.Errorf("scope round-trip mismatch: %+v", got)
	}
	if got.Duration != 2*time.Hour {
		t.Errorf("duration = %v, want 2h", got.Duration)
	}
	if got.DurationStr != "2h0m0s" {
		t.Errorf("duration string = %q, want a rendered form for API clients", got.DurationStr)
	}
	if !got.RequestedAt.Equal(requested) {
		t.Errorf("requested_at = %v, want %v", got.RequestedAt, requested)
	}
	if got.DecidedAt != nil || got.ExpiresAt != nil {
		t.Error("a pending grant should have no decision or expiry timestamps")
	}
}

func TestSQLiteStore_GetGrantMissing(t *testing.T) {
	store := newTestStore(t)
	got, err := store.GetGrant(context.Background(), "no-such-grant")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for an unknown grant, not an error")
	}
}

func TestSQLiteStore_UpdateGrant(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	g := &Grant{Subject: "dev@corp.com", ClusterName: "prod-eu", Status: GrantStatusPending,
		Reason: "INC-4521 rollback", Duration: time.Hour, RequestedAt: time.Now().UTC()}
	if err := store.CreateGrant(ctx, g); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	decided := time.Date(2026, 9, 3, 13, 0, 0, 0, time.UTC)
	expires := decided.Add(time.Hour)
	g.Status, g.DecidedAt, g.DecidedBy = GrantStatusApproved, &decided, "boss@corp.com"
	g.DecisionNote, g.ExpiresAt = "paged, go ahead", &expires
	if err := store.UpdateGrant(ctx, g); err != nil {
		t.Fatalf("update grant: %v", err)
	}

	got, _ := store.GetGrant(ctx, g.ID)
	if got.Status != GrantStatusApproved {
		t.Errorf("status = %q, want approved", got.Status)
	}
	if got.DecidedBy != "boss@corp.com" || got.DecisionNote != "paged, go ahead" {
		t.Errorf("decision not persisted: %+v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("expires_at = %v, want %v", got.ExpiresAt, expires)
	}
	if got.DecidedAt == nil || !got.DecidedAt.Equal(decided) {
		t.Errorf("decided_at = %v, want %v", got.DecidedAt, decided)
	}
}

func TestSQLiteStore_UpdateGrantMissing(t *testing.T) {
	store := newTestStore(t)
	g := &Grant{ID: "no-such-grant", Status: GrantStatusApproved}
	if err := store.UpdateGrant(context.Background(), g); err != ErrGrantNotFound {
		t.Errorf("error = %v, want ErrGrantNotFound", err)
	}
}

func TestSQLiteStore_ListGrants(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

	seed := []struct {
		subject string
		status  GrantStatus
		offset  time.Duration
	}{
		{"dev@corp.com", GrantStatusPending, 0},
		{"dev@corp.com", GrantStatusApproved, time.Minute},
		{"other@corp.com", GrantStatusPending, 2 * time.Minute},
		{"other@corp.com", GrantStatusApproved, 3 * time.Minute},
	}
	for _, s := range seed {
		g := &Grant{Subject: s.subject, ClusterName: "prod-eu", Status: s.status,
			Reason: "INC-4521 rollback", Duration: time.Hour, RequestedAt: base.Add(s.offset)}
		if err := store.CreateGrant(ctx, g); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}

	tests := []struct {
		name   string
		filter GrantFilter
		want   int
	}{
		{"no filter returns all", GrantFilter{}, 4},
		{"by subject", GrantFilter{Subject: "dev@corp.com"}, 2},
		{"by status", GrantFilter{Status: string(GrantStatusApproved)}, 2},
		{"by subject and status", GrantFilter{Subject: "dev@corp.com", Status: string(GrantStatusApproved)}, 1},
		{"limit", GrantFilter{Limit: 2}, 2},
		{"no matches", GrantFilter{Subject: "nobody@corp.com"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.ListGrants(ctx, tt.filter)
			if err != nil {
				t.Fatalf("list grants: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d grants, want %d", len(got), tt.want)
			}
		})
	}

	t.Run("newest request first", func(t *testing.T) {
		got, _ := store.ListGrants(ctx, GrantFilter{})
		for i := 1; i < len(got); i++ {
			if got[i-1].RequestedAt.Before(got[i].RequestedAt) {
				t.Fatalf("listing is not newest-first: %v then %v",
					got[i-1].RequestedAt, got[i].RequestedAt)
			}
		}
	})
}

func TestSQLiteStore_AuditGrantIDRoundTrips(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	user := &User{Email: "dev@x.com", Name: "Dev", PasswordHash: "h", IsActive: true}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	entries := []*AuditLog{
		{UserID: user.ID, UserEmail: user.Email, ClusterName: "prod", Command: "delete pod p",
			Status: AuditStatusSuccess, GrantID: "grant-123"},
		{UserID: user.ID, UserEmail: user.Email, ClusterName: "prod", Command: "get pods",
			Status: AuditStatusSuccess},
	}
	for _, e := range entries {
		if err := store.CreateAuditLog(ctx, e); err != nil {
			t.Fatalf("create audit log: %v", err)
		}
	}

	byGrant, _, err := store.ListAuditLogs(ctx, AuditLogFilter{GrantID: "grant-123"})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(byGrant) != 1 {
		t.Fatalf("grant filter returned %d entries, want 1", len(byGrant))
	}
	if byGrant[0].GrantID != "grant-123" || byGrant[0].Command != "delete pod p" {
		t.Errorf("wrong entry returned: %+v", byGrant[0])
	}

	all, _, _ := store.ListAuditLogs(ctx, AuditLogFilter{})
	for _, l := range all {
		if l.Command == "get pods" && l.GrantID != "" {
			t.Errorf("grant_id = %q, want empty for a command run without a grant", l.GrantID)
		}
	}
}

package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrGrantNotFound is returned when a grant ID matches nothing.
var ErrGrantNotFound = errors.New("grant not found")

// grantColumns is the select list shared by every grant read.
const grantColumns = `id, subject, user_id, cluster_name, namespace, status, reason,
	duration_ns, requested_at, decided_at, decided_by, decision_note, expires_at`

// CreateGrant persists a new access request.
func (s *SQLiteStore) CreateGrant(ctx context.Context, g *Grant) error {
	if g.ID == "" {
		g.ID = uuid.New().String()
	}
	if g.RequestedAt.IsZero() {
		g.RequestedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO grants (id, subject, user_id, cluster_name, namespace, status, reason,
			duration_ns, requested_at, decided_at, decided_by, decision_note, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ID, g.Subject, nilIfEmpty(g.UserID), g.ClusterName, nilIfEmpty(g.Namespace),
		string(g.Status), g.Reason, int64(g.Duration),
		g.RequestedAt.UTC().Format(timeFormat), formatTimePtr(g.DecidedAt),
		nilIfEmpty(g.DecidedBy), nilIfEmpty(g.DecisionNote), formatTimePtr(g.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("create grant: %w", err)
	}
	return nil
}

// GetGrant loads one grant by ID, returning nil when it does not exist.
func (s *SQLiteStore) GetGrant(ctx context.Context, id string) (*Grant, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id)
	g, err := scanGrant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get grant: %w", err)
	}
	return g, nil
}

// UpdateGrant writes back a decision. Only the mutable fields are touched: the
// request itself is immutable once made.
func (s *SQLiteStore) UpdateGrant(ctx context.Context, g *Grant) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE grants SET status = ?, duration_ns = ?, decided_at = ?, decided_by = ?,
			decision_note = ?, expires_at = ? WHERE id = ?`,
		string(g.Status), int64(g.Duration), formatTimePtr(g.DecidedAt),
		nilIfEmpty(g.DecidedBy), nilIfEmpty(g.DecisionNote), formatTimePtr(g.ExpiresAt), g.ID,
	)
	if err != nil {
		return fmt.Errorf("update grant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update grant: %w", err)
	}
	if n == 0 {
		return ErrGrantNotFound
	}
	return nil
}

// ListGrants returns grants matching the filter, newest request first.
func (s *SQLiteStore) ListGrants(ctx context.Context, f GrantFilter) ([]*Grant, error) {
	query := `SELECT ` + grantColumns + ` FROM grants`
	var clauses []string
	var args []any
	if f.Subject != "" {
		clauses = append(clauses, "subject = ?")
		args = append(args, f.Subject)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, f.Status)
	}
	for i, c := range clauses {
		if i == 0 {
			query += " WHERE " + c
		} else {
			query += " AND " + c
		}
	}
	query += " ORDER BY requested_at DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list grants: %w", err)
	}
	defer rows.Close()

	var grants []*Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, fmt.Errorf("list grants: %w", err)
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanGrant reads one grant row.
func scanGrant(row rowScanner) (*Grant, error) {
	var g Grant
	var userID, namespace, decidedBy, note, decidedAt, expiresAt *string
	var status, requestedAt string
	var durationNS int64

	err := row.Scan(&g.ID, &g.Subject, &userID, &g.ClusterName, &namespace, &status,
		&g.Reason, &durationNS, &requestedAt, &decidedAt, &decidedBy, &note, &expiresAt)
	if err != nil {
		return nil, err
	}

	g.UserID = derefStr(userID)
	g.Namespace = derefStr(namespace)
	g.Status = GrantStatus(status)
	g.Duration = time.Duration(durationNS)
	g.DurationStr = g.Duration.String()
	g.DecidedBy = derefStr(decidedBy)
	g.DecisionNote = derefStr(note)
	g.RequestedAt, _ = time.Parse(timeFormat, requestedAt)
	g.DecidedAt = parseTimePtr(decidedAt)
	g.ExpiresAt = parseTimePtr(expiresAt)
	return &g, nil
}

// formatTimePtr renders an optional timestamp for storage.
func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeFormat)
}

// parseTimePtr reads an optional timestamp back.
func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t, err := time.Parse(timeFormat, *s)
	if err != nil {
		return nil
	}
	return &t
}

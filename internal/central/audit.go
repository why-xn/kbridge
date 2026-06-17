package central

import (
	"context"
	"log"
	"time"
)

// Audit status values for a recorded command.
const (
	AuditStatusSuccess = "success"
	AuditStatusFailed  = "failed"
	AuditStatusDenied  = "denied"
	AuditStatusTimeout = "timeout"
)

// auditWriteTimeout bounds how long an audit insert may take.
const auditWriteTimeout = 5 * time.Second

// AuditRecorder persists audit log entries. Recording never blocks on the
// caller's request context and never fails the caller: write errors are logged.
type AuditRecorder struct {
	store Store
}

// NewAuditRecorder creates an AuditRecorder backed by the given store.
func NewAuditRecorder(store Store) *AuditRecorder {
	return &AuditRecorder{store: store}
}

// Record persists a single audit entry. It uses a background context so a
// cancelled request still produces an audit trail.
func (r *AuditRecorder) Record(entry *AuditLog) {
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	if err := r.store.CreateAuditLog(ctx, entry); err != nil {
		log.Printf("audit: failed to record %q on %s by %s: %v",
			entry.Command, entry.ClusterName, entry.UserEmail, err)
	}
}

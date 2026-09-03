package controlplane

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/why-xn/kbridge/internal/policy"
)

// GrantStatus is where an access request sits in its lifecycle. Expiry is not a
// status: it is derived from ExpiresAt, so a grant lapses on its own without a
// background job flipping rows.
type GrantStatus string

const (
	GrantStatusPending  GrantStatus = "pending"
	GrantStatusApproved GrantStatus = "approved"
	GrantStatusDenied   GrantStatus = "denied"
	GrantStatusRevoked  GrantStatus = "revoked"
)

// Grant bounds of a requested window. A grant that never expires would defeat
// the point of just-in-time access, so a duration is always required.
const (
	MinGrantDuration = time.Minute
	MaxGrantReason   = policy.MaxReasonLength
)

// Grant is a time-boxed permission to run commands a guardrail would otherwise
// refuse. It is requested by one person and approved by another, which is what
// distinguishes it from a self-asserted reason.
type Grant struct {
	ID           string        `json:"id"`
	Subject      string        `json:"subject"`
	UserID       string        `json:"user_id,omitempty"`
	ClusterName  string        `json:"cluster_name"`
	Namespace    string        `json:"namespace,omitempty"`
	Status       GrantStatus   `json:"status"`
	Reason       string        `json:"reason"`
	Duration     time.Duration `json:"-"`
	DurationStr  string        `json:"duration"`
	RequestedAt  time.Time     `json:"requested_at"`
	DecidedAt    *time.Time    `json:"decided_at,omitempty"`
	DecidedBy    string        `json:"decided_by,omitempty"`
	DecisionNote string        `json:"decision_note,omitempty"`
	ExpiresAt    *time.Time    `json:"expires_at,omitempty"`
}

// GrantFilter narrows a grant listing.
type GrantFilter struct {
	Subject string
	Status  string
	Limit   int
}

// Active reports whether the grant currently authorizes anything. A grant is
// live only while it is approved and unexpired.
func (g *Grant) Active(now time.Time) bool {
	return g.Status == GrantStatusApproved &&
		g.ExpiresAt != nil && now.Before(*g.ExpiresAt)
}

// Covers reports whether an active grant authorizes this specific request. The
// cluster and namespace recorded on the grant are glob patterns, so a grant for
// "prod-*" covers every production cluster; an empty namespace means any.
func (g *Grant) Covers(req AccessRequest, now time.Time) bool {
	if !g.Active(now) {
		return false
	}
	if !policy.MatchPattern(g.ClusterName, req.Cluster) {
		return false
	}
	return g.Namespace == "" || policy.MatchPattern(g.Namespace, req.Namespace)
}

// Expired reports whether an approved grant has run out. Used for display, so a
// listing can distinguish "approved and live" from "approved but lapsed".
func (g *Grant) Expired(now time.Time) bool {
	return g.Status == GrantStatusApproved &&
		g.ExpiresAt != nil && !now.Before(*g.ExpiresAt)
}

// DisplayStatus renders the status a human should see, folding a lapsed
// approval into "expired" rather than showing it as still approved.
func (g *Grant) DisplayStatus(now time.Time) string {
	if g.Expired(now) {
		return "expired"
	}
	return string(g.Status)
}

// GrantService is the domain service for just-in-time access: it creates
// requests, records decisions, and answers whether a command is covered.
type GrantService struct {
	store  Store
	audit  *AuditRecorder
	limits GrantsConfig
	now    func() time.Time
}

// NewGrantService creates a GrantService bounded by the configured limits.
func NewGrantService(store Store, audit *AuditRecorder, limits GrantsConfig) *GrantService {
	return &GrantService{store: store, audit: audit, limits: limits, now: time.Now}
}

// Now is the service's clock. Handlers render expiry against it rather than
// the wall clock so every view of a grant agrees with the decision the service
// would make about it.
func (s *GrantService) Now() time.Time {
	return s.now()
}

// Request records a pending access request. The duration is the window the
// grant will run for once approved; the clock does not start until then.
func (s *GrantService) Request(ctx context.Context, subject, userID, cluster, namespace string, d time.Duration, reason string) (*Grant, error) {
	if err := s.validateRequest(cluster, d, reason); err != nil {
		return nil, err
	}
	g := &Grant{
		Subject:     subject,
		UserID:      userID,
		ClusterName: cluster,
		Namespace:   namespace,
		Status:      GrantStatusPending,
		Reason:      policy.NormalizeReason(reason),
		Duration:    d,
		RequestedAt: s.now().UTC(),
	}
	if err := s.store.CreateGrant(ctx, g); err != nil {
		return nil, fmt.Errorf("creating grant: %w", err)
	}
	s.record(g, AuditStatusGrantRequested, "")
	return g, nil
}

// validateRequest checks a request against the configured bounds.
func (s *GrantService) validateRequest(cluster string, d time.Duration, reason string) error {
	if strings.TrimSpace(cluster) == "" {
		return fmt.Errorf("cluster is required")
	}
	if len(policy.NormalizeReason(reason)) < policy.MinReasonLength {
		return fmt.Errorf("a reason of at least %d characters is required", policy.MinReasonLength)
	}
	if d < MinGrantDuration {
		return fmt.Errorf("duration must be at least %s", MinGrantDuration)
	}
	if d > s.limits.EffectiveMaxDuration() {
		return fmt.Errorf("duration exceeds the maximum of %s", s.limits.EffectiveMaxDuration())
	}
	return nil
}

// Approve activates a pending grant, starting its window now. An approver may
// shorten the window by passing a non-zero duration.
func (s *GrantService) Approve(ctx context.Context, id, approver, note string, override time.Duration) (*Grant, error) {
	g, err := s.pending(ctx, id)
	if err != nil {
		return nil, err
	}
	if approver != "" && approver == g.Subject && !s.limits.AllowSelfApproval {
		return nil, fmt.Errorf("you cannot approve your own request")
	}
	d := g.Duration
	if override > 0 {
		if override > s.limits.EffectiveMaxDuration() {
			return nil, fmt.Errorf("duration exceeds the maximum of %s", s.limits.EffectiveMaxDuration())
		}
		d = override
	}
	now := s.now().UTC()
	expires := now.Add(d)
	g.Status, g.DecidedAt, g.DecidedBy = GrantStatusApproved, &now, approver
	g.DecisionNote, g.ExpiresAt, g.Duration = note, &expires, d
	if err := s.store.UpdateGrant(ctx, g); err != nil {
		return nil, fmt.Errorf("approving grant: %w", err)
	}
	s.record(g, AuditStatusGrantApproved, note)
	return g, nil
}

// Deny rejects a pending grant.
func (s *GrantService) Deny(ctx context.Context, id, approver, note string) (*Grant, error) {
	g, err := s.pending(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	g.Status, g.DecidedAt, g.DecidedBy, g.DecisionNote = GrantStatusDenied, &now, approver, note
	if err := s.store.UpdateGrant(ctx, g); err != nil {
		return nil, fmt.Errorf("denying grant: %w", err)
	}
	s.record(g, AuditStatusGrantDenied, note)
	return g, nil
}

// Revoke ends an approved grant early. Revoking is always allowed, including
// after expiry, so an operator never has to reason about the clock to shut
// access off.
func (s *GrantService) Revoke(ctx context.Context, id, approver, note string) (*Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrGrantNotFound
	}
	if g.Status == GrantStatusDenied || g.Status == GrantStatusRevoked {
		return nil, fmt.Errorf("grant is already %s", g.Status)
	}
	now := s.now().UTC()
	g.Status, g.DecidedAt, g.DecidedBy, g.DecisionNote = GrantStatusRevoked, &now, approver, note
	if err := s.store.UpdateGrant(ctx, g); err != nil {
		return nil, fmt.Errorf("revoking grant: %w", err)
	}
	s.record(g, AuditStatusGrantRevoked, note)
	return g, nil
}

// pending loads a grant and rejects it if it is not awaiting a decision.
func (s *GrantService) pending(ctx context.Context, id string) (*Grant, error) {
	g, err := s.store.GetGrant(ctx, id)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrGrantNotFound
	}
	if g.Status != GrantStatusPending {
		return nil, fmt.Errorf("grant is already %s", g.Status)
	}
	return g, nil
}

// Covering returns the active grant that authorizes req for subject, or nil.
// A store failure is logged and treated as "no grant": a JIT check that cannot
// read its own state must not hand out access.
func (s *GrantService) Covering(ctx context.Context, subject string, req AccessRequest) *Grant {
	grants, err := s.store.ListGrants(ctx, GrantFilter{
		Subject: subject,
		Status:  string(GrantStatusApproved),
	})
	if err != nil {
		log.Printf("grants: lookup failed for %s, treating as no grant: %v", subject, err)
		return nil
	}
	now := s.now()
	for _, g := range grants {
		if g.Covers(req, now) {
			return g
		}
	}
	return nil
}

// List returns grants matching the filter.
func (s *GrantService) List(ctx context.Context, f GrantFilter) ([]*Grant, error) {
	return s.store.ListGrants(ctx, f)
}

// record writes a grant lifecycle event to the audit log, so "who asked for
// production, and who let them in" is answerable from one place.
func (s *GrantService) record(g *Grant, status, note string) {
	if s.audit == nil {
		return
	}
	entry := &AuditLog{
		UserID:       g.UserID,
		UserEmail:    g.Subject,
		ClusterName:  g.ClusterName,
		Command:      grantSummary(g),
		Namespace:    g.Namespace,
		Status:       status,
		Reason:       g.Reason,
		GrantID:      g.ID,
		ErrorMessage: note,
	}
	s.audit.Record(entry)
}

// grantSummary renders a grant as the audit log's command column.
func grantSummary(g *Grant) string {
	scope := g.ClusterName
	if g.Namespace != "" {
		scope += "/" + g.Namespace
	}
	return fmt.Sprintf("grant %s for %s", scope, g.Duration)
}

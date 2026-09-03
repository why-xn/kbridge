package controlplane

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// testGrantLimits are the bounds most tests run under.
var testGrantLimits = GrantsConfig{
	MaxDuration:     8 * time.Hour,
	DefaultDuration: time.Hour,
}

// newGrantService builds a service over a fresh store with a frozen clock, so
// expiry can be reasoned about exactly rather than raced.
func newGrantService(t *testing.T, now time.Time, limits GrantsConfig) (*GrantService, *SQLiteStore) {
	t.Helper()
	store := newTestStore(t)
	svc := NewGrantService(store, NewAuditRecorder(store), limits)
	svc.now = func() time.Time { return now }
	return svc, store
}

func TestGrant_Active(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Hour)

	tests := []struct {
		name    string
		grant   Grant
		want    bool
		expired bool
	}{
		{
			name:  "approved and unexpired",
			grant: Grant{Status: GrantStatusApproved, ExpiresAt: &future}, want: true,
		},
		{
			name:  "approved but lapsed",
			grant: Grant{Status: GrantStatusApproved, ExpiresAt: &past}, want: false, expired: true,
		},
		{
			name:  "pending grants nothing",
			grant: Grant{Status: GrantStatusPending, ExpiresAt: &future}, want: false,
		},
		{
			name:  "denied grants nothing",
			grant: Grant{Status: GrantStatusDenied, ExpiresAt: &future}, want: false,
		},
		{
			name:  "revoked grants nothing even before expiry",
			grant: Grant{Status: GrantStatusRevoked, ExpiresAt: &future}, want: false,
		},
		{
			name:  "approved with no expiry is not active",
			grant: Grant{Status: GrantStatusApproved}, want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grant.Active(now); got != tt.want {
				t.Errorf("Active() = %v, want %v", got, tt.want)
			}
			if got := tt.grant.Expired(now); got != tt.expired {
				t.Errorf("Expired() = %v, want %v", got, tt.expired)
			}
		})
	}
}

func TestGrant_DisplayStatus(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	future, past := now.Add(time.Hour), now.Add(-time.Hour)

	tests := []struct {
		name  string
		grant Grant
		want  string
	}{
		{"live approval", Grant{Status: GrantStatusApproved, ExpiresAt: &future}, "approved"},
		{"lapsed approval reads as expired", Grant{Status: GrantStatusApproved, ExpiresAt: &past}, "expired"},
		{"pending", Grant{Status: GrantStatusPending}, "pending"},
		{"revoked", Grant{Status: GrantStatusRevoked, ExpiresAt: &past}, "revoked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grant.DisplayStatus(now); got != tt.want {
				t.Errorf("DisplayStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGrant_Covers(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	active := func(cluster, ns string) Grant {
		return Grant{Status: GrantStatusApproved, ExpiresAt: &future, ClusterName: cluster, Namespace: ns}
	}

	tests := []struct {
		name  string
		grant Grant
		req   AccessRequest
		want  bool
	}{
		{
			name:  "exact cluster, any namespace",
			grant: active("prod-eu", ""),
			req:   AccessRequest{Cluster: "prod-eu", Namespace: "payments"}, want: true,
		},
		{
			name:  "wrong cluster",
			grant: active("prod-eu", ""),
			req:   AccessRequest{Cluster: "prod-us", Namespace: "payments"}, want: false,
		},
		{
			name:  "cluster glob",
			grant: active("prod-*", ""),
			req:   AccessRequest{Cluster: "prod-us", Namespace: "payments"}, want: true,
		},
		{
			name:  "namespace scoped grant matches",
			grant: active("prod-eu", "payments"),
			req:   AccessRequest{Cluster: "prod-eu", Namespace: "payments"}, want: true,
		},
		{
			name:  "namespace scoped grant rejects another namespace",
			grant: active("prod-eu", "payments"),
			req:   AccessRequest{Cluster: "prod-eu", Namespace: "billing"}, want: false,
		},
		{
			name:  "namespace glob",
			grant: active("prod-eu", "team-*"),
			req:   AccessRequest{Cluster: "prod-eu", Namespace: "team-a"}, want: true,
		},
		{
			name: "an inactive grant covers nothing",
			grant: Grant{Status: GrantStatusPending, ExpiresAt: &future,
				ClusterName: "prod-eu"},
			req: AccessRequest{Cluster: "prod-eu"}, want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grant.Covers(tt.req, now); got != tt.want {
				t.Errorf("Covers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGrantService_Request_Validation(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)

	tests := []struct {
		name     string
		cluster  string
		duration time.Duration
		reason   string
		wantErr  string
	}{
		{"valid", "prod-eu", time.Hour, "INC-4521 rollback", ""},
		{"missing cluster", "", time.Hour, "INC-4521 rollback", "cluster is required"},
		{"blank cluster", "   ", time.Hour, "INC-4521 rollback", "cluster is required"},
		{"short reason", "prod-eu", time.Hour, "oops", "at least 8 characters"},
		{"empty reason", "prod-eu", time.Hour, "", "at least 8 characters"},
		{"duration too short", "prod-eu", time.Second, "INC-4521 rollback", "at least"},
		{"duration over the ceiling", "prod-eu", 100 * time.Hour, "INC-4521 rollback", "exceeds the maximum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := svc.Request(context.Background(), "dev@corp.com", "", tt.cluster, "", tt.duration, tt.reason)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if g.Status != GrantStatusPending {
					t.Errorf("status = %q, want pending: a request grants nothing until approved", g.Status)
				}
				if g.ExpiresAt != nil {
					t.Error("a pending grant must not have an expiry: the clock starts at approval")
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestGrantService_ApproveStartsTheClock(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	ctx := context.Background()

	g, err := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", 2*time.Hour, "INC-4521 rollback")
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	approved, err := svc.Approve(ctx, g.ID, "boss@corp.com", "paged, go ahead", 0)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != GrantStatusApproved {
		t.Errorf("status = %q, want approved", approved.Status)
	}
	if approved.DecidedBy != "boss@corp.com" {
		t.Errorf("decided_by = %q, want the approver", approved.DecidedBy)
	}
	want := now.Add(2 * time.Hour)
	if approved.ExpiresAt == nil || !approved.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want %v (approval time plus the window)", approved.ExpiresAt, want)
	}
	if !approved.Active(now) {
		t.Error("a freshly approved grant should be active")
	}
	if approved.Active(want.Add(time.Second)) {
		t.Error("the grant should lapse once the window ends")
	}
}

func TestGrantService_ApproveHonoursShorterWindow(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", 4*time.Hour, "INC-4521 rollback")
	approved, err := svc.Approve(ctx, g.ID, "boss@corp.com", "", 30*time.Minute)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	want := now.Add(30 * time.Minute)
	if approved.ExpiresAt == nil || !approved.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the approver's shorter window %v", approved.ExpiresAt, want)
	}
}

func TestGrantService_ApproveRejectsOverlongOverride(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
	if _, err := svc.Approve(ctx, g.ID, "boss@corp.com", "", 100*time.Hour); err == nil {
		t.Fatal("an approver must not be able to exceed the configured ceiling")
	}
}

func TestGrantService_SelfApproval(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("refused by default", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		_, err := svc.Approve(ctx, g.ID, "dev@corp.com", "", 0)
		if err == nil {
			t.Fatal("self-approval defeats the point and must be refused")
		}
		if !strings.Contains(err.Error(), "your own") {
			t.Errorf("error = %q, want it to explain self-approval", err)
		}
	})

	t.Run("allowed when configured", func(t *testing.T) {
		limits := testGrantLimits
		limits.AllowSelfApproval = true
		svc, _ := newGrantService(t, now, limits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		if _, err := svc.Approve(ctx, g.ID, "dev@corp.com", "", 0); err != nil {
			t.Fatalf("self-approval should be permitted when enabled: %v", err)
		}
	})
}

func TestGrantService_DecisionsAreOneShot(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	tests := []struct {
		name  string
		first func(*GrantService, string) error
	}{
		{"approve twice", func(s *GrantService, id string) error {
			_, err := s.Approve(ctx, id, "boss@corp.com", "", 0)
			return err
		}},
		{"deny then approve", func(s *GrantService, id string) error {
			_, err := s.Deny(ctx, id, "boss@corp.com", "no")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newGrantService(t, now, testGrantLimits)
			g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
			if err := tt.first(svc, g.ID); err != nil {
				t.Fatalf("first decision: %v", err)
			}
			if _, err := svc.Approve(ctx, g.ID, "boss@corp.com", "", 0); err == nil {
				t.Error("a decided grant must not accept a second decision")
			}
		})
	}
}

func TestGrantService_Revoke(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	t.Run("ends an approved grant early", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", 4*time.Hour, "INC-4521 rollback")
		if _, err := svc.Approve(ctx, g.ID, "boss@corp.com", "", 0); err != nil {
			t.Fatalf("approve: %v", err)
		}
		revoked, err := svc.Revoke(ctx, g.ID, "boss@corp.com", "incident over")
		if err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if revoked.Active(now) {
			t.Error("a revoked grant must stop authorizing immediately")
		}
	})

	t.Run("works on a pending grant", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		if _, err := svc.Revoke(ctx, g.ID, "boss@corp.com", ""); err != nil {
			t.Fatalf("revoke: %v", err)
		}
	})

	t.Run("is refused twice", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		svc.Revoke(ctx, g.ID, "boss@corp.com", "")
		if _, err := svc.Revoke(ctx, g.ID, "boss@corp.com", ""); err == nil {
			t.Error("an already revoked grant should report that it is revoked")
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		if _, err := svc.Revoke(ctx, "no-such-grant", "boss@corp.com", ""); err != ErrGrantNotFound {
			t.Errorf("error = %v, want ErrGrantNotFound", err)
		}
	})
}

func TestGrantService_Covering(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	req := AccessRequest{Cluster: "prod-eu", Namespace: "payments", Verb: "delete", Resource: "pods"}

	t.Run("an approved grant covers the request", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)
		if got := svc.Covering(ctx, "dev@corp.com", req); got == nil {
			t.Fatal("expected the approved grant to cover the request")
		}
	})

	t.Run("a pending grant covers nothing", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		if got := svc.Covering(ctx, "dev@corp.com", req); got != nil {
			t.Error("an unapproved request must not authorize anything")
		}
	})

	t.Run("another user's grant does not carry over", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "other@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)
		if got := svc.Covering(ctx, "dev@corp.com", req); got != nil {
			t.Error("a grant belongs to its subject only")
		}
	})

	t.Run("an expired grant covers nothing", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)
		svc.now = func() time.Time { return now.Add(2 * time.Hour) }
		if got := svc.Covering(ctx, "dev@corp.com", req); got != nil {
			t.Error("the grant should have lapsed without anything flipping its row")
		}
	})

	t.Run("a revoked grant covers nothing", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
		svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)
		svc.Revoke(ctx, g.ID, "boss@corp.com", "")
		if got := svc.Covering(ctx, "dev@corp.com", req); got != nil {
			t.Error("revocation must take effect immediately")
		}
	})

	t.Run("a grant for another cluster does not cover", func(t *testing.T) {
		svc, _ := newGrantService(t, now, testGrantLimits)
		g, _ := svc.Request(ctx, "dev@corp.com", "", "dev-1", "", time.Hour, "INC-4521 rollback")
		svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)
		if got := svc.Covering(ctx, "dev@corp.com", req); got != nil {
			t.Error("scope must be honoured")
		}
	})
}

func TestGrantService_AuditsLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, store := newGrantService(t, now, testGrantLimits)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
	svc.Approve(ctx, g.ID, "boss@corp.com", "paged", 0)
	svc.Revoke(ctx, g.ID, "boss@corp.com", "done")

	logs, _, err := store.ListAuditLogs(ctx, AuditLogFilter{})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	seen := map[string]bool{}
	for _, l := range logs {
		seen[l.Status] = true
		if l.GrantID != g.ID {
			t.Errorf("audit entry %q has grant_id %q, want %q", l.Status, l.GrantID, g.ID)
		}
	}
	for _, want := range []string{AuditStatusGrantRequested, AuditStatusGrantApproved, AuditStatusGrantRevoked} {
		if !seen[want] {
			t.Errorf("no audit entry with status %q", want)
		}
	}
}

// recordingNotifier captures events the service emits.
type recordingNotifier struct {
	mu     sync.Mutex
	events []GrantEvent
}

func (r *recordingNotifier) Notify(ev GrantEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingNotifier) got() []GrantEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]GrantEvent(nil), r.events...)
}

func TestGrantService_NotifiesLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	rec := &recordingNotifier{}
	svc.SetNotifier(rec)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
	svc.Approve(ctx, g.ID, "boss@corp.com", "paged", 0)
	svc.Revoke(ctx, g.ID, "boss@corp.com", "done")

	events := rec.got()
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	want := []struct{ event, actor, note string }{
		{AuditStatusGrantRequested, "", ""},
		{AuditStatusGrantApproved, "boss@corp.com", "paged"},
		{AuditStatusGrantRevoked, "boss@corp.com", "done"},
	}
	for i, w := range want {
		ev := events[i]
		if ev.Event != w.event || ev.Actor != w.actor || ev.Note != w.note {
			t.Errorf("event %d = %s/%q/%q, want %s/%q/%q", i, ev.Event, ev.Actor, ev.Note, w.event, w.actor, w.note)
		}
		if ev.Grant == nil || ev.Grant.ID != g.ID {
			t.Errorf("event %d carries the wrong grant", i)
		}
		if !ev.At.Equal(now) {
			t.Errorf("event %d At = %v, want the service clock %v", i, ev.At, now)
		}
	}
}

func TestGrantService_NotifiesDeny(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	rec := &recordingNotifier{}
	svc.SetNotifier(rec)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
	svc.Deny(ctx, g.ID, "boss@corp.com", "use the runbook")

	events := rec.got()
	if len(events) != 2 || events[1].Event != AuditStatusGrantDenied {
		t.Fatalf("events = %v, want request then deny", events)
	}
	if events[1].Grant.Status != GrantStatusDenied {
		t.Errorf("the denied event should carry the decided grant, got status %q", events[1].Grant.Status)
	}
}

// TestGrantService_NotifierGetsASnapshot pins that a later change to the grant
// does not reach into an event already handed off, since delivery is async.
func TestGrantService_NotifierGetsASnapshot(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	rec := &recordingNotifier{}
	svc.SetNotifier(rec)
	ctx := context.Background()

	g, _ := svc.Request(ctx, "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback")
	svc.Approve(ctx, g.ID, "boss@corp.com", "", 0)

	requested := rec.got()[0]
	if requested.Grant.Status != GrantStatusPending {
		t.Errorf("the request event now reads %q; it must keep the state it was sent with", requested.Grant.Status)
	}
}

func TestGrantService_NoNotifierIsFine(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	svc, _ := newGrantService(t, now, testGrantLimits)
	if _, err := svc.Request(context.Background(), "dev@corp.com", "", "prod-eu", "", time.Hour, "INC-4521 rollback"); err != nil {
		t.Fatalf("a service with no notifier must still work: %v", err)
	}
}

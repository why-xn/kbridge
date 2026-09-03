package controlplane

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sampleEvent is a request event for rendering tests.
func sampleEvent(event string) GrantEvent {
	return GrantEvent{
		Event: event,
		Grant: &Grant{
			ID: "g-123", Subject: "dev@corp.com", ClusterName: "prod-eu", Namespace: "payments",
			Duration: 2 * time.Hour, Reason: "INC-4521 rollback",
		},
		Actor: "boss@corp.com",
		Note:  "paged, go ahead",
		At:    time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
	}
}

// capture is a webhook receiver that records what it was sent.
type capture struct {
	mu       sync.Mutex
	requests []capturedRequest
	failures int // remaining responses that fail with 500
	srv      *httptest.Server
}

type capturedRequest struct {
	body      []byte
	signature string
	content   string
}

func newCapture(t *testing.T, failures int) *capture {
	t.Helper()
	c := &capture{failures: failures}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.requests = append(c.requests, capturedRequest{
			body: body, signature: r.Header.Get(SignatureHeader), content: r.Header.Get("Content-Type"),
		})
		if c.failures > 0 {
			c.failures--
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capture) got() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

// quietNotifier builds a notifier whose retry delay is instant.
func quietNotifier(hooks ...NotifyConfig) *WebhookNotifier {
	n := NewWebhookNotifier(hooks)
	n.sleep = func(time.Duration) {}
	return n
}

func TestRenderNotification_Formats(t *testing.T) {
	ev := sampleEvent(AuditStatusGrantRequested)

	tests := []struct {
		format string
		check  func(t *testing.T, body map[string]any)
	}{
		{NotifyFormatSlack, func(t *testing.T, body map[string]any) {
			text, _ := body["text"].(string)
			if !strings.Contains(text, "dev@corp.com") || !strings.Contains(text, "prod-eu/payments") {
				t.Errorf("slack text missing subject or scope: %q", text)
			}
			if !strings.Contains(text, "kb admin grants approve g-123") {
				t.Errorf("a request should tell the approver exactly what to run: %q", text)
			}
			if _, ok := body["blocks"]; !ok {
				t.Error("slack payload should carry blocks for rich rendering")
			}
		}},
		{NotifyFormatGoogleChat, func(t *testing.T, body map[string]any) {
			text, _ := body["text"].(string)
			if !strings.Contains(text, "INC-4521 rollback") {
				t.Errorf("google chat text missing the reason: %q", text)
			}
			if len(body) != 1 {
				t.Errorf("google chat webhooks accept only text, got keys %v", body)
			}
		}},
		{NotifyFormatJSON, func(t *testing.T, body map[string]any) {
			if body["event"] != AuditStatusGrantRequested {
				t.Errorf("event = %v", body["event"])
			}
			grant, _ := body["grant"].(map[string]any)
			if grant["id"] != "g-123" || grant["subject"] != "dev@corp.com" {
				t.Errorf("json payload should carry the structured grant: %v", grant)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			raw, err := renderNotification(tt.format, ev)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("payload is not JSON: %v", err)
			}
			tt.check(t, body)
		})
	}

	t.Run("unknown format is an error", func(t *testing.T) {
		if _, err := renderNotification("carrier-pigeon", ev); err == nil {
			t.Error("expected an error for an unknown format")
		}
	})
}

func TestEventText_PerEvent(t *testing.T) {
	tests := []struct {
		event    string
		contains []string
		absent   []string
	}{
		{AuditStatusGrantRequested, []string{"Access requested", "kb admin grants approve", "kb admin grants deny"}, []string{"by boss@corp.com"}},
		{AuditStatusGrantApproved, []string{"Access approved", "by boss@corp.com", "paged, go ahead"}, []string{"kb admin grants approve"}},
		{AuditStatusGrantDenied, []string{"Access denied", "by boss@corp.com"}, []string{"kb admin grants approve"}},
		{AuditStatusGrantRevoked, []string{"Access revoked", "by boss@corp.com"}, []string{"kb admin grants approve"}},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			ev := sampleEvent(tt.event)
			if tt.event == AuditStatusGrantRequested {
				ev.Actor, ev.Note = "", "" // a request has no decider yet
			}
			text := eventText(ev)
			for _, want := range tt.contains {
				if !strings.Contains(text, want) {
					t.Errorf("text missing %q: %s", want, text)
				}
			}
			for _, bad := range tt.absent {
				if strings.Contains(text, bad) {
					t.Errorf("text should not contain %q: %s", bad, text)
				}
			}
		})
	}
}

func TestSignPayload(t *testing.T) {
	body := []byte(`{"event":"grant-requested"}`)
	sig := SignPayload("s3cret", body)
	if len(sig) != 64 {
		t.Errorf("signature should be hex sha256 (64 chars), got %d", len(sig))
	}
	if SignPayload("s3cret", body) != sig {
		t.Error("signing must be deterministic")
	}
	if SignPayload("other", body) == sig {
		t.Error("a different secret must give a different signature")
	}
	if SignPayload("s3cret", []byte(`{"event":"grant-denied"}`)) == sig {
		t.Error("a different body must give a different signature")
	}
}

func TestWebhookNotifier_DeliversAndSigns(t *testing.T) {
	c := newCapture(t, 0)
	n := quietNotifier(NotifyConfig{URL: c.srv.URL, Format: NotifyFormatJSON, Secret: "s3cret"})

	n.Notify(sampleEvent(AuditStatusGrantRequested))
	n.Flush()

	got := c.got()
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	if got[0].content != "application/json" {
		t.Errorf("content-type = %q", got[0].content)
	}
	if want := SignPayload("s3cret", got[0].body); got[0].signature != want {
		t.Errorf("signature = %q, want the HMAC of the delivered body", got[0].signature)
	}
}

func TestWebhookNotifier_ChatFormatsAreUnsigned(t *testing.T) {
	c := newCapture(t, 0)
	n := quietNotifier(NotifyConfig{URL: c.srv.URL, Format: NotifyFormatSlack, Secret: "ignored"})
	n.Notify(sampleEvent(AuditStatusGrantRequested))
	n.Flush()

	got := c.got()
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	if got[0].signature != "" {
		t.Error("a chat webhook cannot verify a signature; sending one would only leak that a secret exists")
	}
}

func TestWebhookNotifier_RetriesOnce(t *testing.T) {
	tests := []struct {
		name       string
		failures   int
		wantHits   int
		deliveries string
	}{
		{"succeeds first time", 0, 1, "no retry"},
		{"fails once then succeeds", 1, 2, "one retry"},
		{"fails twice, gives up", 2, 2, "retry once, not forever"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newCapture(t, tt.failures)
			n := quietNotifier(NotifyConfig{URL: c.srv.URL, Format: NotifyFormatJSON})
			n.Notify(sampleEvent(AuditStatusGrantRequested))
			n.Flush()
			if got := len(c.got()); got != tt.wantHits {
				t.Errorf("hits = %d, want %d (%s)", got, tt.wantHits, tt.deliveries)
			}
		})
	}
}

func TestWebhookNotifier_EventFilter(t *testing.T) {
	c := newCapture(t, 0)
	n := quietNotifier(NotifyConfig{
		URL: c.srv.URL, Format: NotifyFormatJSON,
		Events: []string{AuditStatusGrantRequested},
	})
	n.Notify(sampleEvent(AuditStatusGrantRequested))
	n.Notify(sampleEvent(AuditStatusGrantApproved))
	n.Notify(sampleEvent(AuditStatusGrantRevoked))
	n.Flush()

	got := c.got()
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1: only the subscribed event", len(got))
	}
	var body GrantEvent
	_ = json.Unmarshal(got[0].body, &body)
	if body.Event != AuditStatusGrantRequested {
		t.Errorf("delivered %q, want the subscribed event", body.Event)
	}
}

func TestWebhookNotifier_FansOutToEveryHook(t *testing.T) {
	a, b := newCapture(t, 0), newCapture(t, 0)
	n := quietNotifier(
		NotifyConfig{URL: a.srv.URL, Format: NotifyFormatSlack},
		NotifyConfig{URL: b.srv.URL, Format: NotifyFormatJSON},
	)
	n.Notify(sampleEvent(AuditStatusGrantApproved))
	n.Flush()
	if len(a.got()) != 1 || len(b.got()) != 1 {
		t.Errorf("hits = %d and %d, want one each", len(a.got()), len(b.got()))
	}
}

func TestWebhookNotifier_UnreachableHostDoesNotBlock(t *testing.T) {
	// A dead endpoint must be logged and forgotten. The caller's Notify returns
	// immediately; only Flush waits, and even that is bounded by the timeout.
	n := quietNotifier(NotifyConfig{URL: "http://127.0.0.1:1", Format: NotifyFormatJSON})
	start := time.Now()
	n.Notify(sampleEvent(AuditStatusGrantRequested))
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("Notify blocked for %v; delivery must be asynchronous", elapsed)
	}
	n.Flush()
}

func TestWebhookNotifier_NoHooksIsNoop(t *testing.T) {
	n := NewWebhookNotifier(nil)
	n.Notify(sampleEvent(AuditStatusGrantRequested))
	n.Flush() // must not hang
}

func TestNotifyConfig_Wants(t *testing.T) {
	tests := []struct {
		name   string
		events []string
		event  string
		want   bool
	}{
		{"empty subscribes to all", nil, AuditStatusGrantRevoked, true},
		{"listed event", []string{AuditStatusGrantRequested}, AuditStatusGrantRequested, true},
		{"unlisted event", []string{AuditStatusGrantRequested}, AuditStatusGrantApproved, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (NotifyConfig{Events: tt.events}).wants(tt.event); got != tt.want {
				t.Errorf("wants() = %v, want %v", got, tt.want)
			}
		})
	}
}

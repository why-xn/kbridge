package controlplane

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Notification formats. Each renders the same event for a different receiver.
const (
	NotifyFormatSlack      = "slack"       // Slack incoming webhook: text + blocks
	NotifyFormatGoogleChat = "google-chat" // Google Chat incoming webhook: text
	NotifyFormatJSON       = "json"        // structured payload, optionally HMAC-signed
)

// Delivery bounds. A webhook must never hold up the request that triggered it,
// so delivery is asynchronous, short, and retried once.
const (
	notifyTimeout    = 5 * time.Second
	notifyRetryDelay = 2 * time.Second
	// SignatureHeader carries the HMAC-SHA256 of the JSON body, hex-encoded,
	// when a json hook has a secret. Receivers use it to verify the sender.
	SignatureHeader = "X-Kbridge-Signature"
)

// GrantEvent is one step in a grant's life, as sent to a webhook.
type GrantEvent struct {
	Event string    `json:"event"` // grant-requested, grant-approved, grant-denied, grant-revoked
	Grant *Grant    `json:"grant"`
	Actor string    `json:"actor,omitempty"` // who took the action; empty for a request
	Note  string    `json:"note,omitempty"`
	At    time.Time `json:"at"`
}

// Notifier receives grant lifecycle events.
type Notifier interface {
	Notify(ev GrantEvent)
}

// WebhookNotifier delivers grant events to configured HTTP endpoints. Delivery
// runs in the background and failures are logged, never returned: an approver
// not being pinged must not fail the request that created the grant.
type WebhookNotifier struct {
	hooks  []NotifyConfig
	client *http.Client
	wg     sync.WaitGroup
	// sleep is replaced in tests so a retry does not take two real seconds.
	sleep func(time.Duration)
}

// NewWebhookNotifier creates a notifier for the given hooks. With none it is a
// no-op, which is what a deployment that has not configured any gets.
func NewWebhookNotifier(hooks []NotifyConfig) *WebhookNotifier {
	return &WebhookNotifier{
		hooks:  hooks,
		client: &http.Client{Timeout: notifyTimeout},
		sleep:  time.Sleep,
	}
}

// Notify fans the event out to every hook that subscribes to it.
func (n *WebhookNotifier) Notify(ev GrantEvent) {
	for _, hook := range n.hooks {
		if !hook.wants(ev.Event) {
			continue
		}
		n.wg.Add(1)
		go n.deliver(hook, ev)
	}
}

// Flush waits for in-flight deliveries, so a shutdown does not drop the
// notification for the request that arrived a moment ago.
func (n *WebhookNotifier) Flush() {
	n.wg.Wait()
}

// deliver posts one event to one hook, retrying once on failure.
func (n *WebhookNotifier) deliver(hook NotifyConfig, ev GrantEvent) {
	defer n.wg.Done()
	body, err := renderNotification(hook.Format, ev)
	if err != nil {
		log.Printf("notify: rendering %s for %s: %v", ev.Event, hook.URL, err)
		return
	}
	if err := n.post(hook, body); err == nil {
		return
	} else {
		log.Printf("notify: %s to %s failed, retrying: %v", ev.Event, hook.URL, err)
	}
	n.sleep(notifyRetryDelay)
	if err := n.post(hook, body); err != nil {
		log.Printf("notify: %s to %s failed after retry: %v", ev.Event, hook.URL, err)
	}
}

// post sends one rendered payload.
func (n *WebhookNotifier) post(hook NotifyConfig, body []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if hook.Format == NotifyFormatJSON && hook.Secret != "" {
		req.Header.Set(SignatureHeader, SignPayload(hook.Secret, body))
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// SignPayload returns the hex HMAC-SHA256 of body under secret. Exported so a
// receiver written in Go can verify with the same function.
func SignPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// renderNotification encodes an event for a given receiver format.
func renderNotification(format string, ev GrantEvent) ([]byte, error) {
	switch format {
	case NotifyFormatSlack:
		return json.Marshal(slackPayload(ev))
	case NotifyFormatGoogleChat:
		return json.Marshal(map[string]string{"text": eventText(ev)})
	case NotifyFormatJSON:
		return json.Marshal(ev)
	default:
		return nil, fmt.Errorf("unknown notify format %q", format)
	}
}

// slackPayload renders an event as a Slack incoming-webhook message: a plain
// text fallback for notifications, plus a block with the action to take.
func slackPayload(ev GrantEvent) map[string]any {
	text := eventText(ev)
	return map[string]any{
		"text": text,
		"blocks": []map[string]any{
			{"type": "section", "text": map[string]string{"type": "mrkdwn", "text": text}},
		},
	}
}

// eventText is the human-readable line every chat format shares. For a new
// request it ends with the exact command an approver runs, so acting on the
// message is a paste, not a lookup.
func eventText(ev GrantEvent) string {
	g := ev.Grant
	scope := g.ClusterName
	if g.Namespace != "" {
		scope += "/" + g.Namespace
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*%s* — %s wants %s on `%s` for %s\n> %s",
		eventTitle(ev.Event), g.Subject, verbFor(ev.Event), scope, g.Duration, g.Reason)
	if ev.Actor != "" {
		fmt.Fprintf(&b, "\nby %s", ev.Actor)
	}
	if ev.Note != "" {
		fmt.Fprintf(&b, ": %s", ev.Note)
	}
	if ev.Event == AuditStatusGrantRequested {
		fmt.Fprintf(&b, "\n`kb admin grants approve %s`  ·  `kb admin grants deny %s`", g.ID, g.ID)
	}
	return b.String()
}

// eventTitle is the headline for each lifecycle event.
func eventTitle(event string) string {
	switch event {
	case AuditStatusGrantRequested:
		return "Access requested"
	case AuditStatusGrantApproved:
		return "Access approved"
	case AuditStatusGrantDenied:
		return "Access denied"
	case AuditStatusGrantRevoked:
		return "Access revoked"
	default:
		return event
	}
}

// verbFor phrases the request in the tense that fits the event.
func verbFor(event string) string {
	if event == AuditStatusGrantRequested {
		return "access"
	}
	return "access (grant " + strings.TrimPrefix(event, "grant-") + ")"
}

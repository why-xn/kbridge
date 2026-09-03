//go:build e2e
// +build e2e

package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

var notifyPort = flag.Int("notify-port", 18999, "port the harness configured as the grant webhook target")

// The harness configures a json hook to this port with this secret, so the
// test can prove the control plane signs what it sends.
const notifySecret = "e2e-notify-secret"

// hookReceiver is a webhook endpoint the test owns.
type hookReceiver struct {
	mu     sync.Mutex
	events []receivedHook
	srv    *http.Server
}

type receivedHook struct {
	body      []byte
	signature string
}

func startHookReceiver(t *testing.T) *hookReceiver {
	t.Helper()
	r := &hookReceiver{}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.events = append(r.events, receivedHook{body: body, signature: req.Header.Get("X-Kbridge-Signature")})
		r.mu.Unlock()
	})
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *notifyPort))
	if err != nil {
		t.Fatalf("listen on notify port %d: %v", *notifyPort, err)
	}
	r.srv = &http.Server{Handler: mux}
	go r.srv.Serve(ln)
	t.Cleanup(func() { r.srv.Close() })
	return r
}

// waitFor polls until an event matching pred arrives, since delivery is async.
func (r *hookReceiver) waitFor(t *testing.T, what string, pred func(receivedHook) bool) receivedHook {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, e := range r.events {
			if pred(e) {
				r.mu.Unlock()
				return e
			}
		}
		r.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no webhook delivery for %s within 10s", what)
	return receivedHook{}
}

// eventNamed matches a delivered json payload by event and grant ID.
func eventNamed(event, grantID string) func(receivedHook) bool {
	return func(h receivedHook) bool {
		var ev struct {
			Event string `json:"event"`
			Grant struct {
				ID string `json:"id"`
			} `json:"grant"`
		}
		return json.Unmarshal(h.body, &ev) == nil && ev.Event == event && ev.Grant.ID == grantID
	}
}

// TestGrantNotifications proves a real control plane delivers each lifecycle
// event to a configured webhook, signed, with the grant inside.
func TestGrantNotifications(t *testing.T) {
	recv := startHookReceiver(t)
	email, userTok := jitRequester(t, "edge-jit-notify@e2e.test")
	installApprovalPolicy(t, email)

	g := requestGrantAPI(t, userTok, *clusterName, "payments", "1h", "INC-4521 notify flow")

	t.Run("a request is delivered and signed", func(t *testing.T) {
		got := recv.waitFor(t, "grant-requested", eventNamed("grant-requested", g.ID))

		mac := hmac.New(sha256.New, []byte(notifySecret))
		mac.Write(got.body)
		if want := hex.EncodeToString(mac.Sum(nil)); got.signature != want {
			t.Errorf("signature = %q, want the HMAC of the body under the configured secret", got.signature)
		}

		var ev struct {
			Grant struct {
				Subject   string `json:"subject"`
				Namespace string `json:"namespace"`
				Reason    string `json:"reason"`
				Status    string `json:"status"`
			} `json:"grant"`
			Actor string `json:"actor"`
		}
		if err := json.Unmarshal(got.body, &ev); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ev.Grant.Subject != email || ev.Grant.Namespace != "payments" || ev.Grant.Reason != "INC-4521 notify flow" {
			t.Errorf("payload does not carry the request: %+v", ev.Grant)
		}
		if ev.Grant.Status != "pending" || ev.Actor != "" {
			t.Errorf("a request event should be pending with no actor, got %q/%q", ev.Grant.Status, ev.Actor)
		}
	})

	t.Run("a decision is delivered with the actor", func(t *testing.T) {
		if _, code := decideGrantAPI(t, g.ID, "approve", map[string]any{"note": "e2e notify"}); code != http.StatusOK {
			t.Fatalf("approve: status %d", code)
		}
		got := recv.waitFor(t, "grant-approved", eventNamed("grant-approved", g.ID))
		var ev struct {
			Actor string `json:"actor"`
			Note  string `json:"note"`
			Grant struct {
				ExpiresAt *time.Time `json:"expires_at"`
			} `json:"grant"`
		}
		_ = json.Unmarshal(got.body, &ev)
		if ev.Actor != "admin@e2e.test" || ev.Note != "e2e notify" {
			t.Errorf("actor/note = %q/%q, want the approver and their note", ev.Actor, ev.Note)
		}
		if ev.Grant.ExpiresAt == nil {
			t.Error("the approved event should carry the grant's expiry")
		}
	})

	t.Run("a revocation is delivered", func(t *testing.T) {
		if _, code := decideGrantAPI(t, g.ID, "revoke", nil); code != http.StatusOK {
			t.Fatalf("revoke: status %d", code)
		}
		recv.waitFor(t, "grant-revoked", eventNamed("grant-revoked", g.ID))
	})
}

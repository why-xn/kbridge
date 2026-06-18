package central

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"github.com/why-xn/kbridge/internal/auth"
)

func TestHandleStreamCommand_StreamsOutput(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	sm := NewSessionManager(10)
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})
	snd := &fakeSender{}
	sm.RegisterAgentStream("a1", snd)

	srv := NewHTTPServer(agents, NewCommandQueue(), NewAuthHandlers(store, jm, time.Hour),
		NewAdminHandlers(store, testPepper), nil, NewAuditRecorder(store), sm, jm)

	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "dev@x.com"})
	body, _ := json.Marshal(ExecRequest{Command: []string{"logs", "-f", "web"}})
	req, _ := http.NewRequest("POST", "/api/v1/clusters/prod/stream", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Drive output via Route once the handler has sent StartStream to the agent.
	// Poll the fakeSender under its lock rather than sleeping a fixed interval.
	go func() {
		var sid string
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if start := snd.lastStart(); start != nil {
				sid = start.GetSessionId()
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if sid == "" {
			return // handler never sent StartStream; let the HTTP request time out naturally
		}
		sm.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Output{
			Output: &agentpb.StreamOutput{
				SessionId: sid,
				Type:      agentpb.OutputType_OUTPUT_TYPE_STDOUT,
				Data:      []byte("line-1\n"),
			},
		}})
		sm.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
			Exit: &agentpb.StreamExit{SessionId: sid, ExitCode: 0},
		}})
	}()

	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("line-1")) {
		t.Errorf("expected streamed output, got %q", w.Body.String())
	}
}

func TestHandleStreamCommand_NoAgentStream(t *testing.T) {
	store := newTestStore(t)
	jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
	agents := NewAgentStore()
	agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"}) // connected but no OpenStream
	srv := NewHTTPServer(agents, NewCommandQueue(), NewAuthHandlers(store, jm, time.Hour),
		NewAdminHandlers(store, testPepper), nil, NewAuditRecorder(store), NewSessionManager(10), jm)

	token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "dev@x.com"})
	body, _ := json.Marshal(ExecRequest{Command: []string{"logs", "-f", "web"}})
	req, _ := http.NewRequest("POST", "/api/v1/clusters/prod/stream", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

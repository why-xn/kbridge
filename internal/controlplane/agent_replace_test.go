package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"github.com/why-xn/kbridge/internal/auth"
	"github.com/why-xn/kbridge/internal/execframe"
)

// A restarted agent registers under a new ID; the old registration must not
// linger and receive requests for the cluster.
func TestAgentStore_RegisterReplacesSameCluster(t *testing.T) {
	tests := []struct {
		name         string
		existing     []*AgentInfo
		register     *AgentInfo
		wantReplaced []string
		wantIDs      []string
	}{
		{
			name:     "first agent for a cluster",
			register: &AgentInfo{ID: "new", ClusterName: "prod"},
			wantIDs:  []string{"new"},
		},
		{
			name:         "restart replaces the previous agent",
			existing:     []*AgentInfo{{ID: "old", ClusterName: "prod"}},
			register:     &AgentInfo{ID: "new", ClusterName: "prod"},
			wantReplaced: []string{"old"},
			wantIDs:      []string{"new"},
		},
		{
			name:     "other clusters are untouched",
			existing: []*AgentInfo{{ID: "dev-agent", ClusterName: "dev"}},
			register: &AgentInfo{ID: "new", ClusterName: "prod"},
			wantIDs:  []string{"dev-agent", "new"},
		},
		{
			name:     "re-registering the same ID replaces nothing",
			existing: []*AgentInfo{{ID: "same", ClusterName: "prod"}},
			register: &AgentInfo{ID: "same", ClusterName: "prod"},
			wantIDs:  []string{"same"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewAgentStore()
			for _, a := range tt.existing {
				store.Register(a)
			}
			replaced := store.Register(tt.register)
			if !slices.Equal(replaced, tt.wantReplaced) {
				t.Fatalf("replaced = %v, want %v", replaced, tt.wantReplaced)
			}
			var ids []string
			for _, a := range store.List() {
				ids = append(ids, a.ID)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, tt.wantIDs) {
				t.Fatalf("agents = %v, want %v", ids, tt.wantIDs)
			}
			got, ok := store.GetByClusterName(tt.register.ClusterName)
			if !ok || got.ID != tt.register.ID {
				t.Fatalf("cluster routes to %+v, want %s", got, tt.register.ID)
			}
		})
	}
}

// When the CLI half-closes the request (its stdin hit EOF), the agent must be
// told so the remote process sees EOF instead of waiting forever.
func TestRunExecBridge_HalfCloseSendsStdinEOF(t *testing.T) {
	m := NewSessionManager(10)
	rs := &recordingSender{}
	m.RegisterAgentStream("a1", rs)
	sess, _ := m.StartWithStdin("a1", []string{"exec", "-i", "p", "--", "sh"}, "ns")

	upR, upW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runExecBridge(ctx, upR, io.Discard, sess, m, func() {})

	_ = execframe.Encode(upW, execframe.Stdin, []byte("echo hi\n"))
	_ = upW.Close() // clean half-close: io.EOF upstream
	waitFor(t, func() bool { return rs.last().GetStdin().GetEof() })
	if got := rs.last().GetStdin().GetSessionId(); got != sess.ID {
		t.Fatalf("EOF sent for session %q, want %q", got, sess.ID)
	}
}

// A streamed command's exit code must reach the CLI (as a trailer), so a
// failing `kb logs -f` does not look like success.
func TestHandleStreamCommand_ExitCodeTrailer(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int32
		want     string
	}{
		{"success", 0, "0"},
		{"failure", 1, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			jm := auth.NewJWTManager("test-secret-at-least-32-chars!!", time.Hour)
			sm := NewSessionManager(10)
			agents := NewAgentStore()
			agents.Register(&AgentInfo{ID: "a1", ClusterName: "prod"})
			snd := &fakeSender{}
			sm.RegisterAgentStream("a1", snd)
			srv := NewHTTPServer(agents, NewCommandQueue(), NewAuthHandlers(store, jm, time.Hour),
				NewAdminHandlers(store, testPepper), nil, NewAuditRecorder(store), sm, jm)

			go func() {
				for i := 0; i < 400 && snd.lastStart() == nil; i++ {
					time.Sleep(5 * time.Millisecond)
				}
				if start := snd.lastStart(); start != nil {
					sm.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
						Exit: &agentpb.StreamExit{SessionId: start.GetSessionId(), ExitCode: tt.exitCode},
					}})
				}
			}()

			token, _ := jm.GenerateAccessToken(&auth.UserClaims{UserID: "u1", Email: "dev@x.com"})
			body, _ := json.Marshal(ExecRequest{Command: []string{"logs", "-f", "web"}})
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			req, _ := http.NewRequest("POST", ts.URL+"/api/v1/clusters/prod/stream", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			_, _ = io.ReadAll(resp.Body) // trailers are available only after the body
			if got := resp.Trailer.Get(exitCodeTrailer); got != tt.want {
				t.Fatalf("exit code trailer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommandQueue_Reassign(t *testing.T) {
	tests := []struct {
		name      string
		agentID   string
		status    CommandStatus
		wantMoved int
		wantAgent string
	}{
		{"pending command follows the new agent", "old", CommandStatusPending, 1, "new"},
		{"picked-up command stays with the old agent", "old", CommandStatusRunning, 0, "old"},
		{"other agents' commands are untouched", "other", CommandStatusPending, 0, "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewCommandQueue()
			id, _ := q.Enqueue(tt.agentID, "prod", []string{"get", "pods"}, "", 30, nil)
			if tt.status == CommandStatusRunning {
				q.MarkRunning(id)
			}
			if moved := q.Reassign("old", "new"); moved != tt.wantMoved {
				t.Fatalf("moved = %d, want %d", moved, tt.wantMoved)
			}
			cmd, _ := q.Get(id)
			if cmd.AgentID != tt.wantAgent {
				t.Fatalf("command agent = %q, want %q", cmd.AgentID, tt.wantAgent)
			}
		})
	}
}

// End to end through the gRPC handler: a restarted agent takes over both the
// cluster's routing and the commands queued for its predecessor.
func TestGRPCServer_Register_RestartTakesOverCluster(t *testing.T) {
	srv, agents, queue := newTestGRPCServer(t)
	ctx := context.Background()
	req := &agentpb.RegisterRequest{AgentToken: testAgentToken, ClusterName: testClusterName}

	first, err := srv.Register(ctx, req)
	if err != nil || !first.Success {
		t.Fatalf("first register: %v %+v", err, first)
	}
	queued, _ := queue.Enqueue(first.AgentId, testClusterName, []string{"get", "pods"}, "", 30, nil)

	second, err := srv.Register(ctx, req)
	if err != nil || !second.Success {
		t.Fatalf("second register: %v %+v", err, second)
	}
	if _, ok := agents.Get(first.AgentId); ok {
		t.Fatal("replaced agent is still registered")
	}
	if got, _ := agents.GetByClusterName(testClusterName); got.ID != second.AgentId {
		t.Fatalf("cluster routes to %s, want %s", got.ID, second.AgentId)
	}
	if pending := queue.GetPendingForAgent(second.AgentId); len(pending) != 1 || pending[0].RequestID != queued {
		t.Fatalf("queued command not moved to the new agent: %+v", pending)
	}
}

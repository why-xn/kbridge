package central

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"github.com/why-xn/kbridge/internal/pfframe"
)

func TestRunPortForwardBridge_RelaysBothDirections(t *testing.T) {
	m := NewSessionManager(10)
	rs := &recordingSender{}
	m.RegisterAgentStream("a1", rs)
	sess, _ := m.StartPortForward("a1", "pod", "ns", []uint32{5432})

	upR, upW := io.Pipe()
	var down bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { runPortForwardBridge(ctx, upR, &down, sess, m, func() {}); close(done) }()

	// upstream OPEN -> PfOpen to agent
	_ = pfframe.Encode(upW, pfframe.Open, pfframe.EncodeOpen(1, 5432))
	waitFor(t, func() bool { return rs.last().GetPfOpen() != nil })

	// downstream: agent PfReady + PfData -> READY + DATA frames
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfReady{PfReady: &agentpb.PfReady{SessionId: sess.ID}}})
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfData{PfData: &agentpb.PfData{SessionId: sess.ID, ConnId: 1, Data: []byte("hi")}}})
	// session error ends the bridge
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfSessionError{PfSessionError: &agentpb.PfSessionError{SessionId: sess.ID, Error: "boom"}}})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not finish")
	}

	r := bytes.NewReader(down.Bytes())
	t1, _, _ := pfframe.Decode(r)
	t2, p2, _ := pfframe.Decode(r)
	t3, _, _ := pfframe.Decode(r)
	if t1 != pfframe.Ready {
		t.Fatalf("frame1 = %v want READY", t1)
	}
	if t2 != pfframe.Data {
		t.Fatalf("frame2 = %v want DATA", t2)
	}
	if cid, _, _ := pfframe.DecodeData(p2); cid != 1 {
		t.Fatalf("data conn_id = %d want 1", cid)
	}
	if t3 != pfframe.SessionError {
		t.Fatalf("frame3 = %v want SESSION_ERROR", t3)
	}
}

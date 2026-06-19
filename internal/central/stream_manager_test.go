package central

import (
	"sync"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []*agentpb.CentralStreamMessage
}

func (f *fakeSender) Send(m *agentpb.CentralStreamMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) sentMessages() []*agentpb.CentralStreamMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*agentpb.CentralStreamMessage, len(f.sent))
	copy(out, f.sent)
	return out
}

func (f *fakeSender) lastStart() *agentpb.StartStream {
	msgs := f.sentMessages()
	for _, m := range msgs {
		if s := m.GetStart(); s != nil {
			return s
		}
	}
	return nil
}

func TestSessionManager_StartRoutesOutputAndExit(t *testing.T) {
	m := NewSessionManager(10)
	snd := &fakeSender{}
	m.RegisterAgentStream("agent-1", snd)

	sess, err := m.Start("agent-1", []string{"logs", "-f", "p"}, "app")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	msgs := snd.sentMessages()
	if len(msgs) != 1 || msgs[0].GetStart() == nil {
		t.Fatalf("expected one StartStream sent, got %+v", msgs)
	}
	sid := msgs[0].GetStart().GetSessionId()

	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Output{
		Output: &agentpb.StreamOutput{SessionId: sid, Type: agentpb.OutputType_OUTPUT_TYPE_STDOUT, Data: []byte("hello\n")},
	}})
	select {
	case chunk := <-sess.Output:
		if string(chunk.Data) != "hello\n" {
			t.Errorf("got %q", chunk.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("no output chunk")
	}

	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
		Exit: &agentpb.StreamExit{SessionId: sid, ExitCode: 0},
	}})
	code, _ := sess.Wait()
	if code != 0 {
		t.Errorf("want exit 0, got %d", code)
	}
}

func TestSessionManager_StartWithoutAgentStream(t *testing.T) {
	m := NewSessionManager(10)
	if _, err := m.Start("missing", []string{"logs"}, ""); err == nil {
		t.Fatal("expected error when agent has no open stream")
	}
}

func TestSessionManager_MaxConcurrent(t *testing.T) {
	m := NewSessionManager(1)
	m.RegisterAgentStream("a", &fakeSender{})
	if _, err := m.Start("a", []string{"logs"}, ""); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := m.Start("a", []string{"logs"}, ""); err == nil {
		t.Fatal("expected ErrTooManyStreams on second start")
	}
}

func TestSessionManager_CancelSendsCancel(t *testing.T) {
	m := NewSessionManager(10)
	snd := &fakeSender{}
	m.RegisterAgentStream("a", snd)
	sess, _ := m.Start("a", []string{"logs"}, "")
	m.Cancel(sess.ID)
	found := false
	for _, msg := range snd.sentMessages() {
		if msg.GetCancel() != nil && msg.GetCancel().GetSessionId() == sess.ID {
			found = true
		}
	}
	if !found {
		t.Error("expected CancelStream to be sent")
	}
}

func TestSessionManager_AgentDisconnectClosesSessions(t *testing.T) {
	m := NewSessionManager(10)
	m.RegisterAgentStream("a", &fakeSender{})
	sess, _ := m.Start("a", []string{"logs"}, "")
	m.UnregisterAgentStream("a")
	if _, ok := <-sess.Output; ok {
		t.Error("expected output channel closed after disconnect")
	}
}

// recordingSender captures messages sent to an agent.
type recordingSender struct {
	mu   sync.Mutex
	msgs []*agentpb.CentralStreamMessage
}

func (r *recordingSender) Send(m *agentpb.CentralStreamMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
	return nil
}

func (r *recordingSender) last() *agentpb.CentralStreamMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.msgs) == 0 {
		return nil
	}
	return r.msgs[len(r.msgs)-1]
}

func TestSessionManager_PortForward(t *testing.T) {
	m := NewSessionManager(10)
	rs := &recordingSender{}
	m.RegisterAgentStream("a1", rs)

	sess, err := m.StartPortForward("a1", "pod", "ns", []uint32{5432})
	if err != nil {
		t.Fatalf("start pf: %v", err)
	}
	if st := rs.last().GetPfStart(); st == nil || st.GetPod() != "pod" || len(st.GetPorts()) != 1 {
		t.Fatalf("PortForwardStart wrong: %+v", rs.last())
	}

	if err := m.SendPfOpen(sess.ID, 1, 5432); err != nil {
		t.Fatal(err)
	}
	if o := rs.last().GetPfOpen(); o == nil || o.GetConnId() != 1 || o.GetRemotePort() != 5432 {
		t.Fatalf("PfOpen wrong: %+v", rs.last())
	}
	if err := m.SendPfData(sess.ID, 1, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if d := rs.last().GetPfData(); d == nil || d.GetConnId() != 1 || string(d.GetData()) != "x" {
		t.Fatalf("PfData wrong: %+v", rs.last())
	}

	// Agent->central routing lands on PfOutput.
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfReady{PfReady: &agentpb.PfReady{SessionId: sess.ID}}})
	if c := <-sess.PfOutput; c.Kind != PfKindReady {
		t.Fatalf("want PfKindReady, got %+v", c)
	}
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfData{PfData: &agentpb.PfData{SessionId: sess.ID, ConnId: 1, Data: []byte("hi")}}})
	if c := <-sess.PfOutput; c.Kind != PfKindData || c.ConnID != 1 || string(c.Data) != "hi" {
		t.Fatalf("want PfData chunk, got %+v", c)
	}

	if err := m.SendPfOpen("nope", 1, 1); err != ErrNoAgentStream {
		t.Fatalf("unknown session: want ErrNoAgentStream, got %v", err)
	}
}

func TestSessionManager_StartInteractiveAndControl(t *testing.T) {
	m := NewSessionManager(10)
	rs := &recordingSender{}
	m.RegisterAgentStream("agent-1", rs)

	sess, err := m.StartInteractive("agent-1", []string{"exec", "-i", "-t", "p", "--", "sh"}, "ns", 40, 120)
	if err != nil {
		t.Fatalf("start interactive: %v", err)
	}
	start := rs.last().GetStart()
	if start == nil || !start.GetTty() || start.GetRows() != 40 || start.GetCols() != 120 {
		t.Fatalf("StartStream tty/size wrong: %+v", start)
	}

	if err := m.SendStdin(sess.ID, []byte("ls\n")); err != nil {
		t.Fatalf("send stdin: %v", err)
	}
	if sd := rs.last().GetStdin(); sd == nil || sd.GetSessionId() != sess.ID || string(sd.GetData()) != "ls\n" {
		t.Fatalf("StdinData wrong: %+v", rs.last())
	}

	if err := m.SendResize(sess.ID, 50, 200); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	if rz := rs.last().GetResize(); rz == nil || rz.GetRows() != 50 || rz.GetCols() != 200 {
		t.Fatalf("Resize wrong: %+v", rs.last())
	}

	if err := m.SendStdin("nope", []byte("x")); err != ErrNoAgentStream {
		t.Fatalf("unknown session: want ErrNoAgentStream, got %v", err)
	}
}

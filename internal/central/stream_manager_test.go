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

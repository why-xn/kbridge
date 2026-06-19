package central

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
	"github.com/why-xn/kbridge/internal/execframe"
)

func TestRunExecBridge_RelaysBothDirections(t *testing.T) {
	m := NewSessionManager(10)
	rs := &recordingSender{}
	m.RegisterAgentStream("a1", rs)
	sess, _ := m.StartInteractive("a1", []string{"exec", "-i", "-t", "p", "--", "sh"}, "ns", 24, 80)

	upR, upW := io.Pipe()       // CLI -> central (stdin frames)
	var down bytes.Buffer       // central -> CLI (output frames)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runExecBridge(ctx, upR, &down, sess, m, func() {})
		close(done)
	}()

	// upstream: a STDIN frame must become a StdinData to the agent
	_ = execframe.Encode(upW, execframe.Stdin, []byte("ls\n"))
	waitFor(t, func() bool { return rs.last().GetStdin() != nil })
	if string(rs.last().GetStdin().GetData()) != "ls\n" {
		t.Fatalf("stdin not relayed: %+v", rs.last())
	}

	// downstream: agent output then exit must become STDOUT then EXIT frames
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Output{
		Output: &agentpb.StreamOutput{SessionId: sess.ID, Type: agentpb.OutputType_OUTPUT_TYPE_STDOUT, Data: []byte("hi")},
	}})
	m.Route(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
		Exit: &agentpb.StreamExit{SessionId: sess.ID, ExitCode: 0},
	}})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge did not finish after EXIT")
	}

	r := bytes.NewReader(down.Bytes())
	t1, d1, _ := execframe.Decode(r)
	t2, _, _ := execframe.Decode(r)
	if t1 != execframe.Stdout || string(d1) != "hi" || t2 != execframe.Exit {
		t.Fatalf("downstream frames wrong: %v %q %v", t1, d1, t2)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal("condition not met in time")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

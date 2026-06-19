package agent

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

const streamReconnectDelay = 3 * time.Second

func outputTypeFor(stdout bool) agentpb.OutputType {
	if stdout {
		return agentpb.OutputType_OUTPUT_TYPE_STDOUT
	}
	return agentpb.OutputType_OUTPUT_TYPE_STDERR
}

// runStream maintains the persistent OpenStream connection, reconnecting until
// ctx is cancelled.
func (a *Agent) runStream(ctx context.Context) {
	for ctx.Err() == nil {
		if err := a.openAndServeStream(ctx); err != nil && ctx.Err() == nil {
			log.Printf("stream closed: %v; reconnecting in %s", err, streamReconnectDelay)
			select {
			case <-time.After(streamReconnectDelay):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (a *Agent) openAndServeStream(ctx context.Context) error {
	stream, err := a.client.OpenStream(ctx)
	if err != nil {
		return err
	}
	a.mu.RLock()
	agentID := a.agentID
	a.mu.RUnlock()
	if err := stream.Send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Register{
		Register: &agentpb.StreamRegister{AgentId: agentID},
	}}); err != nil {
		return err
	}
	log.Printf("Opened command stream to central")

	var mu sync.Mutex // guards stream.Send across session goroutines
	sessions := newSessionCancels()
	// When the stream tears down (drop, error, or ctx cancel), cancel every
	// in-flight session so its kubectl process is killed rather than orphaned
	// until agent shutdown.
	defer sessions.cancelAll()

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch v := msg.GetMsg().(type) {
		case *agentpb.CentralStreamMessage_Start:
			sctx, cancel := context.WithCancel(ctx)
			sid := v.Start.GetSessionId()
			if v.Start.GetTty() {
				stdin := make(chan []byte, 16)
				resize := make(chan [2]uint16, 4)
				sessions.add(sid, cancel, stdin, resize)
				go func(start *agentpb.StartStream) {
					defer sessions.cancel(sid)
					a.runInteractiveSession(sctx, &mu, stream, start, stdin, resize)
				}(v.Start)
			} else {
				sessions.add(sid, cancel, nil, nil)
				go func(start *agentpb.StartStream) {
					defer sessions.cancel(sid) // cancel + forget on completion
					a.runStreamSession(sctx, &mu, stream, start)
				}(v.Start)
			}
		case *agentpb.CentralStreamMessage_Stdin:
			sessions.stdinTo(v.Stdin.GetSessionId(), v.Stdin.GetData())
		case *agentpb.CentralStreamMessage_Resize:
			sessions.resizeTo(v.Resize.GetSessionId(), uint16(v.Resize.GetRows()), uint16(v.Resize.GetCols()))
		case *agentpb.CentralStreamMessage_Cancel:
			sessions.cancel(v.Cancel.GetSessionId())
		}
	}
}

type streamSession struct {
	cancel context.CancelFunc
	stdin  chan []byte
	resize chan [2]uint16
}

// sessionCancels tracks in-flight stream sessions, guaranteeing cancellation
// on stream teardown and routing stdin/resize to the right session.
type sessionCancels struct {
	mu       sync.Mutex
	sessions map[string]*streamSession
}

func newSessionCancels() *sessionCancels {
	return &sessionCancels{sessions: make(map[string]*streamSession)}
}

func (s *sessionCancels) add(id string, cancel context.CancelFunc, stdin chan []byte, resize chan [2]uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = &streamSession{cancel: cancel, stdin: stdin, resize: resize}
}

// cancel cancels and forgets a single session; it is a no-op for an unknown id
// and safe to call more than once (e.g. session completion after an explicit
// cancel).
func (s *sessionCancels) cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[id]; sess != nil {
		sess.cancel()
		delete(s.sessions, id)
	}
}

// cancelAll cancels and forgets every tracked session.
func (s *sessionCancels) cancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		sess.cancel()
		delete(s.sessions, id)
	}
}

func (s *sessionCancels) stdinTo(id string, data []byte) {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess != nil && sess.stdin != nil {
		select {
		case sess.stdin <- data:
		case <-time.After(time.Second): // drop if the session is wedged; never block recv loop
		}
	}
}

func (s *sessionCancels) resizeTo(id string, rows, cols uint16) {
	s.mu.Lock()
	sess := s.sessions[id]
	s.mu.Unlock()
	if sess != nil && sess.resize != nil {
		select {
		case sess.resize <- [2]uint16{rows, cols}:
		default: // resize is best-effort; coalesce by dropping
		}
	}
}

func (s *sessionCancels) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (a *Agent) runStreamSession(ctx context.Context, mu *sync.Mutex, stream agentpb.AgentService_OpenStreamClient, start *agentpb.StartStream) {
	sid := start.GetSessionId()
	send := func(m *agentpb.AgentStreamMessage) {
		mu.Lock()
		defer mu.Unlock()
		_ = stream.Send(m)
	}
	code, err := a.executor.ExecuteStream(ctx, start.GetCommand(), start.GetNamespace(),
		func(stdout bool, data []byte) {
			send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Output{
				Output: &agentpb.StreamOutput{SessionId: sid, Type: outputTypeFor(stdout), Data: data},
			}})
		})
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		code = -1
	}
	send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
		Exit: &agentpb.StreamExit{SessionId: sid, ExitCode: int32(code), ErrorMessage: errMsg},
	}})
}

func (a *Agent) runInteractiveSession(ctx context.Context, mu *sync.Mutex, stream agentpb.AgentService_OpenStreamClient, start *agentpb.StartStream, stdin <-chan []byte, resize <-chan [2]uint16) {
	sid := start.GetSessionId()
	send := func(m *agentpb.AgentStreamMessage) {
		mu.Lock()
		defer mu.Unlock()
		_ = stream.Send(m)
	}
	code, err := a.executor.ExecuteInteractive(ctx, start.GetCommand(), start.GetNamespace(),
		uint16(start.GetRows()), uint16(start.GetCols()), stdin, resize,
		func(data []byte) {
			send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Output{
				Output: &agentpb.StreamOutput{SessionId: sid, Type: agentpb.OutputType_OUTPUT_TYPE_STDOUT, Data: data},
			}})
		})
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		code = -1
	}
	send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_Exit{
		Exit: &agentpb.StreamExit{SessionId: sid, ExitCode: int32(code), ErrorMessage: errMsg},
	}})
}

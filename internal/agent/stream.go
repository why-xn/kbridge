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
			sessions.add(sid, cancel)
			go func(start *agentpb.StartStream) {
				defer sessions.cancel(sid) // cancel + forget on completion
				a.runStreamSession(sctx, &mu, stream, start)
			}(v.Start)
		case *agentpb.CentralStreamMessage_Cancel:
			sessions.cancel(v.Cancel.GetSessionId())
		}
	}
}

// sessionCancels tracks the cancel func of each in-flight stream session and
// guarantees they are all invoked when the stream tears down.
type sessionCancels struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newSessionCancels() *sessionCancels {
	return &sessionCancels{cancels: make(map[string]context.CancelFunc)}
}

func (s *sessionCancels) add(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[id] = cancel
}

// cancel cancels and forgets a single session; it is a no-op for an unknown id
// and safe to call more than once (e.g. session completion after an explicit
// cancel).
func (s *sessionCancels) cancel(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.cancels[id]; cancel != nil {
		cancel()
		delete(s.cancels, id)
	}
}

// cancelAll cancels and forgets every tracked session.
func (s *sessionCancels) cancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.cancels {
		cancel()
		delete(s.cancels, id)
	}
}

func (s *sessionCancels) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cancels)
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

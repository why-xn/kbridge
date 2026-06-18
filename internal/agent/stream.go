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
	cancels := make(map[string]context.CancelFunc)
	var cmu sync.Mutex

	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch v := msg.GetMsg().(type) {
		case *agentpb.CentralStreamMessage_Start:
			sctx, cancel := context.WithCancel(ctx)
			cmu.Lock()
			cancels[v.Start.GetSessionId()] = cancel
			cmu.Unlock()
			go a.runStreamSession(sctx, &mu, stream, v.Start)
		case *agentpb.CentralStreamMessage_Cancel:
			cmu.Lock()
			if cancel := cancels[v.Cancel.GetSessionId()]; cancel != nil {
				cancel()
				delete(cancels, v.Cancel.GetSessionId())
			}
			cmu.Unlock()
		}
	}
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

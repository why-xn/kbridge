package central

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/why-xn/kbridge/api/proto/agentpb"
)

// Errors returned by Start.
var (
	ErrNoAgentStream  = errors.New("agent has no open stream")
	ErrTooManyStreams = errors.New("too many concurrent streams")
)

// sessionOutputBuffer bounds how many chunks may queue for a slow client.
const sessionOutputBuffer = 64

// streamSender is the subset of the gRPC agent stream the manager needs to send on.
type streamSender interface {
	Send(*agentpb.CentralStreamMessage) error
}

// StreamChunk is one piece of command output.
type StreamChunk struct {
	Type agentpb.OutputType
	Data []byte
}

// PfKind tags a port-forward output chunk.
type PfKind int

const (
	PfKindData PfKind = iota
	PfKindClose
	PfKindConnError
	PfKindReady
	PfKindSessionError
)

// PfChunk is one port-forward message routed from the agent to the handler.
type PfChunk struct {
	Kind   PfKind
	ConnID uint32
	Data   []byte
	Err    string
}

// Session is a single streaming command in flight.
type Session struct {
	ID       string
	AgentID  string
	Output   chan StreamChunk
	PfOutput chan PfChunk // non-nil only for port-forward sessions
	done     chan struct{}
	once     sync.Once
	// exitCode and errMsg are written inside once.Do before closing done,
	// so reads after <-done are race-free without additional locking.
	exitCode int32
	errMsg   string
}

func (s *Session) close(exitCode int32, errMsg string) {
	s.once.Do(func() {
		s.exitCode = exitCode
		s.errMsg = errMsg
		if s.Output != nil {
			close(s.Output)
		}
		if s.PfOutput != nil {
			close(s.PfOutput)
		}
		close(s.done)
	})
}

// Wait blocks until the session ends and returns its exit code and error message.
func (s *Session) Wait() (int32, string) {
	<-s.done
	return s.exitCode, s.errMsg
}

type agentConn struct {
	sender   streamSender
	mu       sync.Mutex // gRPC streams are not safe for concurrent Send
	sessions map[string]*Session
}

// SessionManager multiplexes streaming sessions over per-agent bidi streams.
type SessionManager struct {
	mu       sync.Mutex
	agents   map[string]*agentConn
	sessions map[string]*Session
	max      int
}

// NewSessionManager creates a manager allowing maxConcurrent sessions (<=0 means default 50).
func NewSessionManager(maxConcurrent int) *SessionManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 50
	}
	return &SessionManager{
		agents:   make(map[string]*agentConn),
		sessions: make(map[string]*Session),
		max:      maxConcurrent,
	}
}

// RegisterAgentStream records an agent's open stream.
func (m *SessionManager) RegisterAgentStream(agentID string, s streamSender) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[agentID] = &agentConn{sender: s, sessions: make(map[string]*Session)}
}

// UnregisterAgentStream drops an agent and closes all of its sessions.
func (m *SessionManager) UnregisterAgentStream(agentID string) {
	m.mu.Lock()
	conn := m.agents[agentID]
	delete(m.agents, agentID)
	var dead []*Session
	if conn != nil {
		for id, sess := range conn.sessions {
			dead = append(dead, sess)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, sess := range dead {
		sess.close(-1, "agent disconnected")
	}
}

// Start opens a non-interactive session (logs -f / get -w).
func (m *SessionManager) Start(agentID string, command []string, namespace string) (*Session, error) {
	return m.startSession(agentID, &agentpb.StartStream{Command: command, Namespace: namespace})
}

// StartInteractive opens an interactive (tty) session and includes the initial size.
func (m *SessionManager) StartInteractive(agentID string, command []string, namespace string, rows, cols uint16) (*Session, error) {
	return m.startSession(agentID, &agentpb.StartStream{
		Command: command, Namespace: namespace, Tty: true, Rows: uint32(rows), Cols: uint32(cols),
	})
}

// StartWithStdin opens a stdin-streaming session without a TTY.
func (m *SessionManager) StartWithStdin(agentID string, command []string, namespace string) (*Session, error) {
	return m.startSession(agentID, &agentpb.StartStream{
		Command: command, Namespace: namespace, Tty: false,
	})
}

// startSession is the shared open path: send StartStream before inserting into
// the maps to close the phantom-session window (see prior comment).
func (m *SessionManager) startSession(agentID string, start *agentpb.StartStream) (*Session, error) {
	m.mu.Lock()
	conn := m.agents[agentID]
	if conn == nil {
		m.mu.Unlock()
		return nil, ErrNoAgentStream
	}
	if len(m.sessions) >= m.max {
		m.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	sess := &Session{
		ID:      uuid.New().String(),
		AgentID: agentID,
		Output:  make(chan StreamChunk, sessionOutputBuffer),
		done:    make(chan struct{}),
	}
	m.mu.Unlock()

	// Send StartStream BEFORE inserting into the maps to close the phantom-session
	// window: a concurrent Cancel/Route must not observe a session the agent has
	// never received a StartStream for.  Inserting after a successful send is still
	// safe because the agent cannot produce StreamOutput until it receives
	// StartStream, spawns the kubectl process, and reads its first output — all of
	// which take far longer than the map insert that follows immediately after.
	start.SessionId = sess.ID
	if err := sendLocked(conn, &agentpb.CentralStreamMessage{
		Msg: &agentpb.CentralStreamMessage_Start{Start: start},
	}); err != nil {
		// Send failed: nothing was inserted, so there is nothing to clean up.
		return nil, err
	}

	m.mu.Lock()
	conn.sessions[sess.ID] = sess
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// SendStdin forwards stdin bytes to a session's agent.
func (m *SessionManager) SendStdin(sessionID string, data []byte) error {
	conn := m.connFor(sessionID)
	if conn == nil {
		return ErrNoAgentStream
	}
	return sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_Stdin{
		Stdin: &agentpb.StdinData{SessionId: sessionID, Data: data},
	}})
}

// SendResize forwards a window-size change to a session's agent.
func (m *SessionManager) SendResize(sessionID string, rows, cols uint32) error {
	conn := m.connFor(sessionID)
	if conn == nil {
		return ErrNoAgentStream
	}
	return sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_Resize{
		Resize: &agentpb.Resize{SessionId: sessionID, Rows: rows, Cols: cols},
	}})
}

// StartPortForward opens a port-forward session and pushes PortForwardStart.
func (m *SessionManager) StartPortForward(agentID, pod, namespace string, ports []uint32) (*Session, error) {
	m.mu.Lock()
	conn := m.agents[agentID]
	if conn == nil {
		m.mu.Unlock()
		return nil, ErrNoAgentStream
	}
	if len(m.sessions) >= m.max {
		m.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	sess := &Session{
		ID:       uuid.New().String(),
		AgentID:  agentID,
		PfOutput: make(chan PfChunk, sessionOutputBuffer),
		done:     make(chan struct{}),
	}
	m.mu.Unlock()

	err := sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_PfStart{
		PfStart: &agentpb.PortForwardStart{SessionId: sess.ID, Pod: pod, Namespace: namespace, Ports: ports},
	}})
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	conn.sessions[sess.ID] = sess
	m.sessions[sess.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// SendPfOpen forwards a new-connection request to the session's agent.
func (m *SessionManager) SendPfOpen(sessionID string, connID, remotePort uint32) error {
	conn := m.connFor(sessionID)
	if conn == nil {
		return ErrNoAgentStream
	}
	return sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_PfOpen{
		PfOpen: &agentpb.PfOpen{SessionId: sessionID, ConnId: connID, RemotePort: remotePort},
	}})
}

// SendPfData forwards client->pod bytes for a connection.
func (m *SessionManager) SendPfData(sessionID string, connID uint32, data []byte) error {
	conn := m.connFor(sessionID)
	if conn == nil {
		return ErrNoAgentStream
	}
	return sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_PfData{
		PfData: &agentpb.PfData{SessionId: sessionID, ConnId: connID, Data: data},
	}})
}

// SendPfClose forwards a connection close to the agent.
func (m *SessionManager) SendPfClose(sessionID string, connID uint32) error {
	conn := m.connFor(sessionID)
	if conn == nil {
		return ErrNoAgentStream
	}
	return sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_PfClose{
		PfClose: &agentpb.PfClose{SessionId: sessionID, ConnId: connID},
	}})
}

func (m *SessionManager) connFor(sessionID string) *agentConn {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := m.sessions[sessionID]
	if sess == nil {
		return nil
	}
	return m.agents[sess.AgentID]
}

// Route delivers an agent message to its session.
func (m *SessionManager) Route(msg *agentpb.AgentStreamMessage) {
	switch v := msg.GetMsg().(type) {
	case *agentpb.AgentStreamMessage_Output:
		if sess := m.lookup(v.Output.GetSessionId()); sess != nil {
			select {
			case sess.Output <- StreamChunk{Type: v.Output.GetType(), Data: v.Output.GetData()}:
			default:
				// Slow client: cancel rather than block the shared recv loop.
				m.Cancel(sess.ID)
			}
		}
	case *agentpb.AgentStreamMessage_Exit:
		if sess := m.lookup(v.Exit.GetSessionId()); sess != nil {
			m.dropSession(sess.ID)
			sess.close(v.Exit.GetExitCode(), v.Exit.GetErrorMessage())
		}
	case *agentpb.AgentStreamMessage_PfReady:
		m.routePf(v.PfReady.GetSessionId(), PfChunk{Kind: PfKindReady})
	case *agentpb.AgentStreamMessage_PfData:
		m.routePf(v.PfData.GetSessionId(), PfChunk{Kind: PfKindData, ConnID: v.PfData.GetConnId(), Data: v.PfData.GetData()})
	case *agentpb.AgentStreamMessage_PfClose:
		m.routePf(v.PfClose.GetSessionId(), PfChunk{Kind: PfKindClose, ConnID: v.PfClose.GetConnId()})
	case *agentpb.AgentStreamMessage_PfConnError:
		m.routePf(v.PfConnError.GetSessionId(), PfChunk{Kind: PfKindConnError, ConnID: v.PfConnError.GetConnId(), Err: v.PfConnError.GetError()})
	case *agentpb.AgentStreamMessage_PfSessionError:
		m.routePf(v.PfSessionError.GetSessionId(), PfChunk{Kind: PfKindSessionError, Err: v.PfSessionError.GetError()})
	}
}

// routePf delivers a port-forward chunk to its session, cancelling the session
// if the client is too slow to drain (bounded blast radius — never blocks the
// shared recv loop).
func (m *SessionManager) routePf(sessionID string, chunk PfChunk) {
	sess := m.lookup(sessionID)
	if sess == nil || sess.PfOutput == nil {
		return
	}
	select {
	case sess.PfOutput <- chunk:
	default:
		m.Cancel(sess.ID)
	}
}

// Cancel sends CancelStream to the agent and ends the session.
func (m *SessionManager) Cancel(sessionID string) {
	m.mu.Lock()
	sess := m.sessions[sessionID]
	var conn *agentConn
	if sess != nil {
		conn = m.agents[sess.AgentID]
	}
	m.mu.Unlock()
	if sess == nil {
		return
	}
	if conn != nil {
		// Best-effort: if the stream is already broken the agent will detect the
		// disconnect independently, and the session is closed below regardless.
		sendLocked(conn, &agentpb.CentralStreamMessage{Msg: &agentpb.CentralStreamMessage_Cancel{ //nolint:errcheck
			Cancel: &agentpb.CancelStream{SessionId: sessionID},
		}})
	}
	m.dropSession(sessionID)
	sess.close(-1, "canceled")
}

func (m *SessionManager) lookup(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *SessionManager) dropSession(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess := m.sessions[id]; sess != nil {
		if conn := m.agents[sess.AgentID]; conn != nil {
			delete(conn.sessions, id)
		}
	}
	delete(m.sessions, id)
}

func sendLocked(conn *agentConn, msg *agentpb.CentralStreamMessage) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return conn.sender.Send(msg)
}

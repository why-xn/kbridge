package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

var forwardLineRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) -> (\d+)`)

// parseForwardLine extracts (remote, local) from a kubectl "Forwarding from
// 127.0.0.1:LOCAL -> REMOTE" line. ok=false for non-matching / non-IPv4 lines.
func parseForwardLine(line string) (remote, local uint16, ok bool) {
	mtch := forwardLineRe.FindStringSubmatch(line)
	if mtch == nil {
		return 0, 0, false
	}
	l, _ := strconv.Atoi(mtch[1])
	r, _ := strconv.Atoi(mtch[2])
	return uint16(r), uint16(l), true
}

// pfConn holds a single forwarded TCP connection and its bounded inbound write channel.
type pfConn struct {
	conn net.Conn
	in   chan []byte
}

// pfSession fans connections out to per-remote-port local kubectl listeners.
type pfSession struct {
	sessionID     string
	remoteToLocal map[uint16]uint16
	send          func(*agentpb.AgentStreamMessage)

	mu    sync.Mutex
	conns map[uint32]*pfConn
}

func newPfSession(sessionID string, remoteToLocal map[uint16]uint16, send func(*agentpb.AgentStreamMessage)) *pfSession {
	return &pfSession{
		sessionID:     sessionID,
		remoteToLocal: remoteToLocal,
		send:          send,
		conns:         make(map[uint32]*pfConn),
	}
}

func (s *pfSession) connError(connID uint32, msg string) {
	s.send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfConnError{
		PfConnError: &agentpb.PfConnError{SessionId: s.sessionID, ConnId: connID, Error: msg},
	}})
}

// open dials the local kubectl listener for remotePort and starts pumping bytes.
func (s *pfSession) open(connID uint32, remotePort uint16) {
	local, ok := s.remoteToLocal[remotePort]
	if !ok {
		s.connError(connID, fmt.Sprintf("no forward for remote port %d", remotePort))
		return
	}
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", local))
	if err != nil {
		s.connError(connID, fmt.Sprintf("dial: %v", err))
		return
	}
	pc := &pfConn{conn: conn, in: make(chan []byte, 32)}
	s.mu.Lock()
	s.conns[connID] = pc
	s.mu.Unlock()

	// writer goroutine: drains pc.in so data() never blocks the recv loop.
	go func() {
		for d := range pc.in {
			if _, err := pc.conn.Write(d); err != nil {
				break
			}
		}
	}()

	// reader goroutine: pumps bytes from the pod socket back upstream.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := conn.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfData{
					PfData: &agentpb.PfData{SessionId: s.sessionID, ConnId: connID, Data: chunk},
				}})
			}
			if rerr != nil {
				break
			}
		}
		s.closeConn(connID)
		s.send(&agentpb.AgentStreamMessage{Msg: &agentpb.AgentStreamMessage_PfClose{
			PfClose: &agentpb.PfClose{SessionId: s.sessionID, ConnId: connID},
		}})
	}()
}

func (s *pfSession) data(connID uint32, data []byte) {
	s.mu.Lock()
	pc := s.conns[connID]
	s.mu.Unlock()
	if pc == nil {
		return
	}
	select {
	case pc.in <- data:
	default:
		// Write channel full: slow/stuck connection — close it and report.
		s.closeConn(connID)
		s.connError(connID, "write buffer full: connection dropped")
	}
}

func (s *pfSession) closeConn(connID uint32) {
	s.mu.Lock()
	pc := s.conns[connID]
	delete(s.conns, connID)
	s.mu.Unlock()
	if pc != nil {
		close(pc.in)
		_ = pc.conn.Close()
	}
}

func (s *pfSession) shutdown() {
	s.mu.Lock()
	conns := s.conns
	s.conns = make(map[uint32]*pfConn)
	s.mu.Unlock()
	for _, pc := range conns {
		close(pc.in)
		_ = pc.conn.Close()
	}
}

// startKubectlPortForward spawns kubectl, parses the Forwarding lines into a
// remote->local map, and returns it once all ports are mapped. The caller emits
// PfReady. On early kubectl failure it returns an error (-> PfSessionError).
func (e *KubectlExecutor) startKubectlPortForward(ctx context.Context, pod, namespace string, ports []uint32) (map[uint16]uint16, *exec.Cmd, error) {
	args := []string{"port-forward"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, pod)
	for _, p := range ports {
		args = append(args, fmt.Sprintf(":%d", p))
	}
	cmd := exec.CommandContext(ctx, e.kubectlPath, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting kubectl port-forward: %w", err)
	}

	type scanResult struct {
		m   map[uint16]uint16
		err error
	}
	scanDone := make(chan scanResult, 1)
	go func() {
		m := make(map[uint16]uint16)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() && len(m) < len(ports) {
			if remote, local, ok := parseForwardLine(scanner.Text()); ok {
				m[remote] = local
			}
		}
		if len(m) < len(ports) {
			scanDone <- scanResult{err: fmt.Errorf("kubectl port-forward did not establish all ports")}
			return
		}
		scanDone <- scanResult{m: m}
	}()

	select {
	case res := <-scanDone:
		if res.err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, nil, res.err
		}
		return res.m, cmd, nil
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("kubectl port-forward establishment timed out")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("kubectl port-forward canceled during establishment")
	}
}

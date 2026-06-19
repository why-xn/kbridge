package agent

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/why-xn/kbridge/api/proto/agentpb"
)

func TestParseForwardLine(t *testing.T) {
	cases := []struct {
		line          string
		remote, local uint16
		ok            bool
	}{
		{"Forwarding from 127.0.0.1:34567 -> 5432", 5432, 34567, true},
		{"Forwarding from [::1]:34567 -> 5432", 0, 0, false}, // v1 parses IPv4 line only
		{"random log line", 0, 0, false},
	}
	for _, c := range cases {
		r, l, ok := parseForwardLine(c.line)
		if ok != c.ok || (ok && (r != c.remote || l != c.local)) {
			t.Errorf("parseForwardLine(%q)=(%d,%d,%v) want (%d,%d,%v)", c.line, r, l, ok, c.remote, c.local, c.ok)
		}
	}
}

// pfRecorder captures messages emitted by a pfSession's send callback.
type pfRecorder struct {
	mu   sync.Mutex
	msgs []*agentpb.AgentStreamMessage
}

func newPfRecorder() *pfRecorder {
	return &pfRecorder{}
}

func (r *pfRecorder) send(m *agentpb.AgentStreamMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, m)
}

// waitForData polls until a PfData message for connID containing want is found,
// or the 2s deadline is exceeded.
func (r *pfRecorder) waitForData(t *testing.T, connID uint32, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.msgs {
			if d := m.GetPfData(); d != nil && d.GetConnId() == connID && string(d.GetData()) == want {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("timed out waiting for PfData connID=%d data=%q", connID, want)
}

// waitForConnError polls until a PfConnError message for connID is found,
// or the 2s deadline is exceeded.
func (r *pfRecorder) waitForConnError(t *testing.T, connID uint32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.msgs {
			if e := m.GetPfConnError(); e != nil && e.GetConnId() == connID {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("timed out waiting for PfConnError connID=%d", connID)
}

// Fan-out: an in-test echo listener stands in for the kubectl-local port.
func TestPfSession_FanOutEchoAndConnError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				for {
					n, e := c.Read(buf)
					if n > 0 {
						c.Write(buf[:n])
					}
					if e != nil {
						return
					}
				}
			}(c)
		}
	}()
	localPort := uint16(ln.Addr().(*net.TCPAddr).Port)

	rec := newPfRecorder()
	s := newPfSession("sess", map[uint16]uint16{5432: localPort}, rec.send)

	s.open(1, 5432)
	s.data(1, []byte("ping"))
	rec.waitForData(t, 1, "ping") // echoed back
	s.closeConn(1)

	// dial to a port not in map -> PfConnError
	s.open(2, 9) // remote 9 not in map -> conn error
	rec.waitForConnError(t, 2)

	s.shutdown()
}

package cli

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/spf13/viper"
	"github.com/why-xn/kbridge/internal/pfframe"
)

type portMapping struct{ local, remote uint16 }

type pfTarget struct {
	namespace string
	pod       string
	mappings  []portMapping
}

// parsePortForwardArgs recognizes `port-forward <pod> [LOCAL:REMOTE|REMOTE|:REMOTE ...]`.
func parsePortForwardArgs(args []string) (pfTarget, bool) {
	if len(args) == 0 || args[0] != "port-forward" {
		return pfTarget{}, false
	}
	var tgt pfTarget
	var positional []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-n" || a == "--namespace":
			if i+1 < len(rest) {
				tgt.namespace = rest[i+1]
				i++
			}
		case strings.HasPrefix(a, "--namespace="):
			tgt.namespace = strings.TrimPrefix(a, "--namespace=")
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return pfTarget{}, false
	}
	tgt.pod = positional[0]
	for _, spec := range positional[1:] {
		m, ok := parsePortSpec(spec)
		if !ok {
			return pfTarget{}, false
		}
		tgt.mappings = append(tgt.mappings, m)
	}
	return tgt, true
}

func parsePortSpec(spec string) (portMapping, bool) {
	if strings.HasPrefix(spec, ":") {
		r, err := strconv.Atoi(spec[1:])
		if err != nil || r <= 0 || r > 65535 {
			return portMapping{}, false
		}
		return portMapping{local: 0, remote: uint16(r)}, true
	}
	if l, r, found := strings.Cut(spec, ":"); found {
		li, e1 := strconv.Atoi(l)
		ri, e2 := strconv.Atoi(r)
		if e1 != nil || e2 != nil || li <= 0 || li > 65535 || ri <= 0 || ri > 65535 {
			return portMapping{}, false
		}
		return portMapping{local: uint16(li), remote: uint16(ri)}, true
	}
	p, err := strconv.Atoi(spec)
	if err != nil || p <= 0 || p > 65535 {
		return portMapping{}, false
	}
	return portMapping{local: uint16(p), remote: uint16(p)}, true
}

// runPortForward opens the bidi stream, waits for READY, binds local listeners,
// and pumps connections until Ctrl-C.
func runPortForward(centralURL, cluster, token string, tgt pfTarget, insecure bool) error {
	q := url.Values{}
	q.Set("pod", tgt.pod)
	if tgt.namespace != "" {
		q.Set("namespace", tgt.namespace)
	}
	for _, m := range tgt.mappings {
		q.Add("port", strconv.Itoa(int(m.remote)))
	}
	reqURL := fmt.Sprintf("%s/api/v1/clusters/%s/port-forward?%s", centralURL, url.PathEscape(cluster), q.Encode())

	client, err := http2Client(centralURL, insecure)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, reqURL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		refreshToken := viper.GetString(ConfigKeyRefreshToken)
		if refreshToken != "" {
			newAccess, newRefresh, rerr := refreshTokenViaHTTP(centralURL, refreshToken, insecure)
			if rerr == nil {
				viper.Set(ConfigKeyToken, newAccess)
				viper.Set(ConfigKeyRefreshToken, newRefresh)
				_ = saveConfig()
				token = newAccess

				pr, pw = io.Pipe()
				req2, err2 := http.NewRequest(http.MethodPost, reqURL, pr)
				if err2 != nil {
					return err2
				}
				req2.Header.Set("Authorization", "Bearer "+token)
				resp, err = client.Do(req2)
				if err != nil {
					return fmt.Errorf("connecting: %w", err)
				}
			}
		}
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpStatusError(resp)
	}

	// Note 1: initialize done in the literal, before readLoop starts.
	reg := &pfConnRegistry{
		conns: make(map[uint32]net.Conn),
		pw:    pw,
		done:  make(chan struct{}),
	}
	defer reg.shutdown()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; reg.shutdown(); os.Exit(0) }()

	readyCh := make(chan struct{})
	go reg.readLoop(resp.Body, readyCh)

	// Note 2: readiness-OR-done: if stream dies before READY, don't hang.
	select {
	case <-readyCh:
	case <-reg.done:
		if reg.sessErr != "" {
			return fmt.Errorf("port-forward failed: %s", reg.sessErr)
		}
		return nil
	}

	for _, m := range tgt.mappings {
		bound, err := reg.listen(m)
		if err != nil {
			return err
		}
		fmt.Printf("Forwarding from 127.0.0.1:%d -> %d\n", bound, m.remote)
	}

	// Block until the stream ends.
	<-reg.done
	if reg.sessErr != "" {
		return fmt.Errorf("port-forward failed: %s", reg.sessErr)
	}
	return nil
}

// pfConnRegistry owns local listeners + a conn_id->net.Conn map and the request pipe.
type pfConnRegistry struct {
	mu      sync.Mutex
	conns   map[uint32]net.Conn
	nextID  uint32
	pw      *io.PipeWriter
	wmu     sync.Mutex // serializes frame writes to pw
	done    chan struct{}
	doneOne sync.Once
	sessErr string // set by readLoop before closing done; race-free: read only after <-done
}

func (r *pfConnRegistry) writeFrame(t pfframe.Type, payload []byte) {
	r.wmu.Lock()
	defer r.wmu.Unlock()
	_ = pfframe.Encode(r.pw, t, payload)
}

func (r *pfConnRegistry) listen(m portMapping) (uint16, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", m.local))
	if err != nil {
		return 0, fmt.Errorf("listen on %d: %w", m.local, err)
	}
	bound := uint16(ln.Addr().(*net.TCPAddr).Port)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			r.handleConn(c, m.remote)
		}
	}()
	return bound, nil
}

func (r *pfConnRegistry) handleConn(c net.Conn, remote uint16) {
	r.mu.Lock()
	r.nextID++
	id := r.nextID
	r.conns[id] = c
	r.mu.Unlock()

	r.writeFrame(pfframe.Open, pfframe.EncodeOpen(id, remote))
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := c.Read(buf)
			if n > 0 {
				r.writeFrame(pfframe.Data, pfframe.EncodeData(id, buf[:n]))
			}
			if rerr != nil {
				break
			}
		}
		r.writeFrame(pfframe.Close, pfframe.EncodeConnID(id))
		r.closeConn(id)
	}()
}

func (r *pfConnRegistry) closeConn(id uint32) {
	r.mu.Lock()
	c := r.conns[id]
	delete(r.conns, id)
	r.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (r *pfConnRegistry) readLoop(body io.Reader, readyCh chan struct{}) {
	defer r.finish()
	readyClosed := false
	for {
		t, payload, err := pfframe.Decode(body)
		if err != nil {
			return
		}
		switch t {
		case pfframe.Ready:
			if !readyClosed {
				close(readyCh)
				readyClosed = true
			}
		case pfframe.Data:
			if id, data, e := pfframe.DecodeData(payload); e == nil {
				r.mu.Lock()
				c := r.conns[id]
				r.mu.Unlock()
				if c != nil {
					_, _ = c.Write(data)
				}
			}
		case pfframe.Close:
			if id, e := pfframe.DecodeConnID(payload); e == nil {
				r.closeConn(id)
			}
		case pfframe.ConnError:
			if id, _, e := pfframe.DecodeConnError(payload); e == nil {
				r.closeConn(id)
			}
		case pfframe.SessionError:
			r.sessErr = string(payload)
			fmt.Fprintln(os.Stderr, r.sessErr)
			return
		}
	}
}

func (r *pfConnRegistry) finish() { r.doneOne.Do(func() { close(r.done) }) }

func (r *pfConnRegistry) shutdown() {
	r.mu.Lock()
	conns := r.conns
	r.conns = make(map[uint32]net.Conn)
	r.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	if r.pw != nil {
		_ = r.pw.Close()
	}
}

// portForwardFromConfig reads viper config and delegates to runPortForward.
func portForwardFromConfig(tgt pfTarget) error {
	centralURL := viper.GetString(ConfigKeyCentralURL)
	if centralURL == "" {
		return fmt.Errorf("central URL not configured, run 'kb login' first")
	}
	cluster := viper.GetString(ConfigKeyCurrentCluster)
	if cluster == "" {
		return fmt.Errorf("no cluster selected, run 'kb clusters use <name>' first")
	}
	return runPortForward(centralURL, cluster, viper.GetString(ConfigKeyToken), tgt, viper.GetBool(ConfigKeyInsecure))
}

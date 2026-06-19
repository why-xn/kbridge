package central

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/pfframe"
)

// runPortForwardBridge relays a port-forward session between the CLI (frames in,
// frames out) and the agent (via the SessionManager). Transport-agnostic for
// pipe testing. Returns the final session error message ("" on clean end).
func runPortForwardBridge(ctx context.Context, upstream io.Reader, downstream io.Writer, sess *Session, sm *SessionManager, flush func()) string {
	go func() {
		for {
			t, payload, err := pfframe.Decode(upstream)
			if err != nil {
				return // EOF or broken client; ctx.Done drives real teardown
			}
			switch t {
			case pfframe.Open:
				if id, port, e := pfframe.DecodeOpen(payload); e == nil {
					_ = sm.SendPfOpen(sess.ID, id, uint32(port))
				}
			case pfframe.Data:
				if id, data, e := pfframe.DecodeData(payload); e == nil {
					_ = sm.SendPfData(sess.ID, id, data)
				}
			case pfframe.Close:
				if id, e := pfframe.DecodeConnID(payload); e == nil {
					_ = sm.SendPfClose(sess.ID, id)
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			sm.Cancel(sess.ID)
			_, errMsg := sess.Wait()
			return errMsg
		case chunk, ok := <-sess.PfOutput:
			if !ok {
				_, errMsg := sess.Wait()
				if errMsg != "" {
					_ = pfframe.Encode(downstream, pfframe.SessionError, []byte(errMsg))
					flush()
				}
				return errMsg
			}
			switch chunk.Kind {
			case PfKindReady:
				_ = pfframe.Encode(downstream, pfframe.Ready, nil)
			case PfKindData:
				_ = pfframe.Encode(downstream, pfframe.Data, pfframe.EncodeData(chunk.ConnID, chunk.Data))
			case PfKindClose:
				_ = pfframe.Encode(downstream, pfframe.Close, pfframe.EncodeConnID(chunk.ConnID))
			case PfKindConnError:
				_ = pfframe.Encode(downstream, pfframe.ConnError, pfframe.EncodeConnError(chunk.ConnID, chunk.Err))
			case PfKindSessionError:
				_ = pfframe.Encode(downstream, pfframe.SessionError, []byte(chunk.Err))
				flush()
				return chunk.Err
			}
			flush()
		}
	}
}

// handlePortForward runs `kubectl port-forward` over an HTTP/2 bidi stream.
func (s *HTTPServer) handlePortForward(c *gin.Context) {
	clusterName := c.Param("name")
	agent, exists := s.agentStore.GetByClusterName(clusterName)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "cluster not found"})
		return
	}
	if agent.Status != AgentStatusConnected {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "cluster agent is disconnected"})
		return
	}

	pod := c.Query("pod")
	if pod == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pod is required"})
		return
	}
	namespace := c.Query("namespace")
	var ports []uint32
	for _, p := range c.QueryArray("port") {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid port"})
			return
		}
		ports = append(ports, uint32(n))
	}
	if len(ports) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one port is required"})
		return
	}

	req := ExecRequest{Command: []string{"port-forward", pod}, Namespace: namespace}
	if !s.authorizeExec(c, clusterName, req) {
		return
	}

	sess, err := s.sessions.StartPortForward(agent.ID, pod, namespace, ports)
	if err != nil {
		if err == ErrTooManyStreams {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent streams"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "port-forward unavailable for this cluster"})
		return
	}

	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	start := time.Now()
	defer c.Request.Body.Close() // unblocks the upstream Decode goroutine on return

	errMsg := runPortForwardBridge(c.Request.Context(), c.Request.Body, c.Writer, sess, s.sessions, flush)

	status := AuditStatusSuccess
	switch {
	case errMsg == "canceled":
		status = AuditStatusCanceled
	case errMsg != "":
		status = AuditStatusFailed
	}
	dur := time.Since(start).Milliseconds()
	s.recordExecAudit(c, clusterName, req, status, nil, &dur, errMsg)
}

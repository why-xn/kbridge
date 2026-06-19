package central

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/api/proto/agentpb"
	"github.com/why-xn/kbridge/internal/execframe"
)

// runExecBridge relays an interactive session between the CLI (upstream frames
// in, downstream frames out) and the agent (via the SessionManager). It is
// transport-agnostic so it can be unit-tested over pipes.
func runExecBridge(ctx context.Context, upstream io.Reader, downstream io.Writer, sess *Session, sm *SessionManager, flush func()) {
	go func() {
		for {
			t, payload, err := execframe.Decode(upstream)
			if err == io.EOF {
				return // clean half-close (Ctrl-D): stop stdin, keep session alive
			}
			if err != nil {
				sm.Cancel(sess.ID) // broken client
				return
			}
			switch t {
			case execframe.Stdin:
				_ = sm.SendStdin(sess.ID, payload)
			case execframe.Resize:
				if rows, cols, e := execframe.DecodeResize(payload); e == nil {
					_ = sm.SendResize(sess.ID, uint32(rows), uint32(cols))
				}
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			sm.Cancel(sess.ID)
			return
		case chunk, ok := <-sess.Output:
			if !ok {
				code, errMsg := sess.Wait()
				_ = execframe.Encode(downstream, execframe.Exit, execframe.EncodeExit(code, errMsg))
				flush()
				return
			}
			ft := execframe.Stdout
			if chunk.Type == agentpb.OutputType_OUTPUT_TYPE_STDERR {
				ft = execframe.Stderr
			}
			_ = execframe.Encode(downstream, ft, chunk.Data)
			flush()
		}
	}
}

// buildExecArgs assembles kubectl args so that command[0] == "exec" (RBAC reads
// the verb from there).
func buildExecArgs(pod, container string, command []string, tty bool) []string {
	args := []string{"exec", "-i"}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, pod)
	if container != "" {
		args = append(args, "-c", container)
	}
	args = append(args, "--")
	return append(args, command...)
}

// handleExecAttach runs an interactive `kubectl exec -it` over an HTTP/2
// bidirectional stream.
func (s *HTTPServer) handleExecAttach(c *gin.Context) {
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
	container := c.Query("container")
	command := c.QueryArray("command")
	if len(command) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "command is required"})
		return
	}
	tty := c.Query("tty") == "true"
	rows := atoiDefault(c.Query("rows"), 24)
	cols := atoiDefault(c.Query("cols"), 80)
	namespace := c.Query("namespace")

	args := buildExecArgs(pod, container, command, tty)
	req := ExecRequest{Command: args, Namespace: namespace}
	if !s.authorizeExec(c, clusterName, req) {
		return // 403 + denied audit already written
	}

	sess, err := s.sessions.StartInteractive(agent.ID, args, namespace, uint16(rows), uint16(cols))
	if err != nil {
		if err == ErrTooManyStreams {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent streams"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "exec unavailable for this cluster"})
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

	runExecBridge(c.Request.Context(), c.Request.Body, c.Writer, sess, s.sessions, flush)

	exitCode, errMsg := sess.Wait()
	status := AuditStatusSuccess
	if c.Request.Context().Err() != nil {
		status = AuditStatusCanceled
	} else if exitCode != 0 || errMsg != "" {
		status = AuditStatusFailed
	}
	dur := time.Since(start).Milliseconds()
	ec := exitCode
	s.recordExecAudit(c, clusterName, req, status, &ec, &dur, errMsg)
}

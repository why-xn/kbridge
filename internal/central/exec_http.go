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
//
// It returns (exitCode, errMsg) from the session so the caller does not need
// to call sess.Wait() again — every return path closes the session exactly once.
func runExecBridge(ctx context.Context, upstream io.Reader, downstream io.Writer, sess *Session, sm *SessionManager, flush func()) (int32, string) {
	// The upstream goroutine only forwards stdin. On any read error (including
	// io.EOF for a clean half-close and post-EXIT body-close errors) it simply
	// stops forwarding. It must NOT call sm.Cancel: true client disconnect is
	// detected by the downstream select's <-ctx.Done() branch.
	go func() {
		for {
			t, payload, err := execframe.Decode(upstream)
			if err != nil {
				return // stop forwarding on any error; cancel is handled downstream
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
			return sess.Wait()
		case chunk, ok := <-sess.Output:
			if !ok {
				// Output closed: session is already closed by the exit/cancel path.
				code, errMsg := sess.Wait()
				_ = execframe.Encode(downstream, execframe.Exit, execframe.EncodeExit(code, errMsg))
				flush()
				return code, errMsg
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

// clampDim clamps a terminal dimension to [1, 65535] before uint16 conversion,
// preventing wrap-around from absurd values such as rows=70000.
func clampDim(n int) uint16 {
	if n < 1 {
		return 1
	}
	if n > 65535 {
		return 65535
	}
	return uint16(n)
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

	var sess *Session
	var err error
	if tty {
		sess, err = s.sessions.StartInteractive(agent.ID, args, namespace, clampDim(rows), clampDim(cols))
	} else {
		sess, err = s.sessions.StartWithStdin(agent.ID, args, namespace)
	}
	if err != nil {
		if err == ErrTooManyStreams {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent streams"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "exec unavailable for this cluster"})
		return
	}

	// Send 200 headers immediately so the HTTP/2 client's Do() returns and the
	// client-side stdin goroutine can start. Without this flush the response
	// headers are not sent until the first Write, causing a deadlock: the server
	// waits for stdin (from the client) while the client waits for headers.
	c.Writer.WriteHeader(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	flush()
	start := time.Now()

	exitCode, errMsg := runExecBridge(c.Request.Context(), c.Request.Body, c.Writer, sess, s.sessions, flush)

	// I3: distinguish canceled (slow-client via Route→Cancel or ctx cancel) from
	// failed (non-zero exit). Cancel closes the session with errMsg "canceled";
	// ctx.Done indicates a client-side disconnect cancel.
	status := AuditStatusSuccess
	if c.Request.Context().Err() != nil || errMsg == "canceled" {
		status = AuditStatusCanceled
	} else if exitCode != 0 || errMsg != "" {
		status = AuditStatusFailed
	}
	dur := time.Since(start).Milliseconds()
	ec := exitCode

	// I6: audit the logical command (exec <pod> -- <cmd>) rather than the raw
	// kubectl args which include transport flags (-i/-t). Use req for authz
	// (unchanged); build a separate auditReq for the final audit record.
	auditCmd := append([]string{"exec", pod}, append([]string{"--"}, command...)...)
	auditReq := ExecRequest{Command: auditCmd, Namespace: namespace}
	s.recordExecAudit(c, clusterName, auditReq, status, &ec, &dur, errMsg)
}

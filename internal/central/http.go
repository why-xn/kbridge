package central

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
)

// ClusterResponse represents a cluster in API responses.
type ClusterResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ExecRequest represents a command execution request.
type ExecRequest struct {
	Command   []string `json:"command" binding:"required"`
	Namespace string   `json:"namespace,omitempty"`
	Timeout   int      `json:"timeout,omitempty"` // seconds, default 30
	Stdin     string   `json:"stdin,omitempty"`   // optional stdin input for the command
}

// ExecResponse represents a command execution response.
type ExecResponse struct {
	Output   string `json:"output"`
	ExitCode int32  `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// DefaultExecTimeout is the default timeout for command execution.
const DefaultExecTimeout = 30 * time.Second

// MaxExecTimeout is the maximum allowed timeout for command execution.
const MaxExecTimeout = 5 * time.Minute

// HTTPServer handles REST API requests from CLI clients.
type HTTPServer struct {
	router        *gin.Engine
	agentStore    *AgentStore
	commandQueue  *CommandQueue
	authHandlers  *AuthHandlers
	adminHandlers *AdminHandlers
	policy        *PolicyEngine
	audit         *AuditRecorder
	sessions      *SessionManager
	jwtManager    *auth.JWTManager
}

// NewHTTPServer creates a new HTTP server with configured routes.
func NewHTTPServer(agentStore *AgentStore, cmdQueue *CommandQueue, ah *AuthHandlers, adminH *AdminHandlers, policy *PolicyEngine, audit *AuditRecorder, sessions *SessionManager, jm *auth.JWTManager) *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())

	s := &HTTPServer{
		router:        router,
		agentStore:    agentStore,
		commandQueue:  cmdQueue,
		authHandlers:  ah,
		adminHandlers: adminH,
		policy:        policy,
		audit:         audit,
		sessions:      sessions,
		jwtManager:    jm,
	}
	s.setupRoutes()
	return s
}

// Handler returns the HTTP handler for the server.
func (s *HTTPServer) Handler() http.Handler {
	return s.router
}

func (s *HTTPServer) setupRoutes() {
	s.router.GET("/health", s.handleHealth)

	// Auth routes (no auth required for login/refresh)
	if s.authHandlers != nil {
		authGroup := s.router.Group("/auth")
		{
			authGroup.POST("/login", s.authHandlers.HandleLogin)
			authGroup.POST("/refresh", s.authHandlers.HandleRefresh)
		}
	}

	// Protected API routes
	api := s.router.Group("/api/v1")
	if s.jwtManager != nil {
		api.Use(auth.AuthMiddleware(s.jwtManager))
	}
	{
		api.GET("/clusters", s.handleListClusters)
		api.POST("/clusters/:name/exec", s.handleExecCommand)
		if s.sessions != nil {
			api.POST("/clusters/:name/stream", s.handleStreamCommand)
			api.POST("/clusters/:name/exec/attach", s.handleExecAttach)
			api.POST("/clusters/:name/port-forward", s.handlePortForward)
		}

		// Auth routes that require authentication
		if s.authHandlers != nil {
			api.POST("/auth/logout", s.authHandlers.HandleLogout)
			api.POST("/auth/change-password", s.authHandlers.HandleChangePassword)
		}

		// Admin routes require the admin role.
		if s.adminHandlers != nil {
			admin := api.Group("/admin")
			if s.jwtManager != nil {
				admin.Use(auth.AdminRequired())
			}
			{
				admin.POST("/agent-tokens", s.adminHandlers.HandleCreateAgentToken)
				admin.GET("/agent-tokens", s.adminHandlers.HandleListAgentTokens)
				admin.DELETE("/agent-tokens/:id", s.adminHandlers.HandleRevokeAgentToken)

				admin.GET("/users", s.adminHandlers.HandleListUsers)
				admin.POST("/users", s.adminHandlers.HandleCreateUser)
				admin.PUT("/users/:id", s.adminHandlers.HandleUpdateUser)
				admin.DELETE("/users/:id", s.adminHandlers.HandleDeleteUser)

				admin.GET("/audit", s.adminHandlers.HandleListAuditLogs)
			}
		}
	}
}

// handleHealth returns the health status of the central service.
func (s *HTTPServer) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
	})
}

// handleListClusters returns a list of registered clusters.
func (s *HTTPServer) handleListClusters(c *gin.Context) {
	agents := s.agentStore.List()

	clusters := make([]ClusterResponse, 0, len(agents))
	for _, agent := range agents {
		clusters = append(clusters, ClusterResponse{
			Name:   agent.ClusterName,
			Status: agent.Status,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"clusters": clusters,
	})
}

// handleExecCommand executes a kubectl command on a cluster.
func (s *HTTPServer) handleExecCommand(c *gin.Context) {
	clusterName := c.Param("name")

	// Check if agent exists and is connected
	agent, exists := s.agentStore.GetByClusterName(clusterName)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "cluster not found",
		})
		return
	}

	if agent.Status != AgentStatusConnected {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "cluster agent is disconnected",
		})
		return
	}

	// Parse request body
	var req ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid request: " + err.Error(),
		})
		return
	}

	// Validate command
	if len(req.Command) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "command is required",
		})
		return
	}

	// Enforce RBAC before routing the command to the agent.
	if !s.authorizeExec(c, clusterName, req) {
		return
	}

	// Determine timeout
	timeout := DefaultExecTimeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
		if timeout > MaxExecTimeout {
			timeout = MaxExecTimeout
		}
	}

	// Queue the command
	requestID, err := s.commandQueue.Enqueue(
		agent.ID,
		clusterName,
		req.Command,
		req.Namespace,
		int32(timeout.Seconds()),
		[]byte(req.Stdin),
	)
	if err != nil {
		log.Printf("Failed to queue command: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to queue command",
		})
		return
	}

	log.Printf("Queued command %s for cluster %s: %v", requestID, clusterName, req.Command)

	// Wait for result with timeout
	start := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+5*time.Second)
	defer cancel()

	result, err := s.commandQueue.WaitForResult(ctx, requestID)

	// Clean up the command from queue
	defer s.commandQueue.Remove(requestID)

	if err != nil {
		log.Printf("Command %s timed out or failed: %v", requestID, err)
		dur := time.Since(start).Milliseconds()
		s.recordExecAudit(c, clusterName, req, AuditStatusTimeout, nil, &dur, "command execution timed out")
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error": "command execution timed out",
		})
		return
	}

	s.recordExecResult(c, clusterName, req, result, time.Since(start).Milliseconds())

	// Build response
	response := ExecResponse{
		ExitCode: result.ExitCode,
	}

	// Combine stdout and stderr for output
	if len(result.Stdout) > 0 {
		response.Output = string(result.Stdout)
	}
	if len(result.Stderr) > 0 {
		if response.Output != "" {
			response.Output += "\n"
		}
		response.Output += string(result.Stderr)
	}

	if result.ErrorMessage != "" {
		response.Error = result.ErrorMessage
	}

	c.JSON(http.StatusOK, response)
}

// authorizeExec checks the requesting user's RBAC permissions for the command.
// It writes the appropriate error response and returns false when the request
// must be rejected. When no authorizer is configured it allows the request.
func (s *HTTPServer) authorizeExec(c *gin.Context, clusterName string, req ExecRequest) bool {
	if s.policy == nil {
		return true
	}

	claims := auth.GetUserFromContext(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return false
	}

	access := parseAccessRequest(clusterName, req.Command, req.Namespace)
	if !s.policy.Allows(claims.Email, access) {
		log.Printf("RBAC denied: user=%s cluster=%s verb=%s resource=%s namespace=%s",
			claims.Email, clusterName, access.Verb, access.Resource, access.Namespace)
		s.recordExecAudit(c, clusterName, req, AuditStatusDenied, nil, nil, "permission denied")
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return false
	}
	return true
}

// recordExecAudit writes an audit entry for an exec attempt, attributing it to
// the authenticated user and client IP. A no-op when auditing is disabled.
func (s *HTTPServer) recordExecAudit(c *gin.Context, cluster string, req ExecRequest, status string, exitCode *int32, durationMs *int64, errMsg string) {
	if s.audit == nil {
		return
	}
	entry := &AuditLog{
		ClusterName:  cluster,
		Command:      strings.Join(req.Command, " "),
		Namespace:    req.Namespace,
		Status:       status,
		ExitCode:     exitCode,
		DurationMs:   durationMs,
		ErrorMessage: errMsg,
		ClientIP:     c.ClientIP(),
	}
	if claims := auth.GetUserFromContext(c); claims != nil {
		entry.UserID = claims.UserID
		entry.UserEmail = claims.Email
	}
	s.audit.Record(entry)
}

// recordExecResult audits a completed command, deriving success/failed from the
// exit code and any error message.
func (s *HTTPServer) recordExecResult(c *gin.Context, cluster string, req ExecRequest, result *CommandResult, durationMs int64) {
	status := AuditStatusSuccess
	if result.ExitCode != 0 || result.ErrorMessage != "" {
		status = AuditStatusFailed
	}
	exitCode := result.ExitCode
	s.recordExecAudit(c, cluster, req, status, &exitCode, &durationMs, result.ErrorMessage)
}

// handleStreamCommand streams a long-running kubectl command (logs -f, get -w)
// to the client over a chunked HTTP response.
func (s *HTTPServer) handleStreamCommand(c *gin.Context) {
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

	var req ExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if len(req.Command) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "command is required"})
		return
	}
	if !s.authorizeExec(c, clusterName, req) {
		return
	}

	sess, err := s.sessions.Start(agent.ID, req.Command, req.Namespace)
	if err != nil {
		if err == ErrTooManyStreams {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many concurrent streams"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "streaming unavailable for this cluster"})
		return
	}

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	start := time.Now()

	s.streamSessionToClient(c, sess, flusher)

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

// streamSessionToClient copies session output to the response until the session
// ends or the client disconnects (which cancels the session).
func (s *HTTPServer) streamSessionToClient(c *gin.Context, sess *Session, flusher http.Flusher) {
	for {
		select {
		case <-c.Request.Context().Done():
			s.sessions.Cancel(sess.ID)
			return
		case chunk, ok := <-sess.Output:
			if !ok {
				return
			}
			c.Writer.Write(chunk.Data) //nolint:errcheck
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// requestLogger returns a middleware that logs HTTP requests.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

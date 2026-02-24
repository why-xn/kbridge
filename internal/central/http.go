package central

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
)

// ClusterResponse represents a cluster in API responses.
type ClusterResponse struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	KubernetesVersion string `json:"kubernetes_version,omitempty"`
	NodeCount         int32  `json:"node_count,omitempty"`
	Region            string `json:"region,omitempty"`
	Provider          string `json:"provider,omitempty"`
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
	router       *gin.Engine
	agentStore   *AgentStore
	commandQueue *CommandQueue
	authHandlers *AuthHandlers
	jwtManager   *auth.JWTManager
}

// NewHTTPServer creates a new HTTP server with configured routes.
func NewHTTPServer(agentStore *AgentStore, cmdQueue *CommandQueue, ah *AuthHandlers, jm *auth.JWTManager) *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())

	s := &HTTPServer{
		router:       router,
		agentStore:   agentStore,
		commandQueue: cmdQueue,
		authHandlers: ah,
		jwtManager:   jm,
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

		// Auth routes that require authentication
		if s.authHandlers != nil {
			api.POST("/auth/logout", s.authHandlers.HandleLogout)
			api.POST("/auth/change-password", s.authHandlers.HandleChangePassword)
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
			Name:              agent.ClusterName,
			Status:            agent.Status,
			KubernetesVersion: agent.KubernetesVersion,
			NodeCount:         agent.NodeCount,
			Region:            agent.Region,
			Provider:          agent.Provider,
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
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout+5*time.Second)
	defer cancel()

	result, err := s.commandQueue.WaitForResult(ctx, requestID)

	// Clean up the command from queue
	defer s.commandQueue.Remove(requestID)

	if err != nil {
		log.Printf("Command %s timed out or failed: %v", requestID, err)
		c.JSON(http.StatusGatewayTimeout, gin.H{
			"error": "command execution timed out",
		})
		return
	}

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

// requestLogger returns a middleware that logs HTTP requests.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

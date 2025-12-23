package central

import (
	"net/http"

	"github.com/gin-gonic/gin"
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

// HTTPServer handles REST API requests from CLI clients.
type HTTPServer struct {
	router     *gin.Engine
	agentStore *AgentStore
}

// NewHTTPServer creates a new HTTP server with configured routes.
func NewHTTPServer(store *AgentStore) *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())

	s := &HTTPServer{
		router:     router,
		agentStore: store,
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

	api := s.router.Group("/api/v1")
	{
		api.GET("/clusters", s.handleListClusters)
		api.POST("/clusters/:name/exec", s.handleExecCommand)
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

	c.JSON(http.StatusNotImplemented, gin.H{
		"error": "not implemented",
	})
}

// requestLogger returns a middleware that logs HTTP requests.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

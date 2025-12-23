package central

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTPServer handles REST API requests from CLI clients.
type HTTPServer struct {
	router *gin.Engine
}

// NewHTTPServer creates a new HTTP server with configured routes.
func NewHTTPServer() *HTTPServer {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())

	s := &HTTPServer{router: router}
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
	c.JSON(http.StatusOK, gin.H{
		"clusters": []interface{}{},
	})
}

// handleExecCommand executes a kubectl command on a cluster.
func (s *HTTPServer) handleExecCommand(c *gin.Context) {
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

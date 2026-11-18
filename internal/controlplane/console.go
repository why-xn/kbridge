package controlplane

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed console
var consoleFiles embed.FS

const consoleCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// registerConsole serves the embedded admin console. The page holds no data,
// so it needs no auth; every API call it makes is authenticated as usual.
func (s *HTTPServer) registerConsole() {
	sub, err := fs.Sub(consoleFiles, "console")
	if err != nil {
		panic(err) // the embedded directory is fixed at build time
	}
	files := http.StripPrefix("/ui/", http.FileServer(http.FS(sub)))
	s.router.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/ui/") })
	s.router.GET("/ui/*file", func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("file"), "/")
		// Directories other than the root would produce a listing.
		if name != "" && strings.HasSuffix(name, "/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", consoleCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if name == "" || name == "index.html" {
			h.Set("Cache-Control", "no-store")
		} else {
			h.Set("Cache-Control", "max-age=300")
		}
		files.ServeHTTP(c.Writer, c.Request)
	})
}

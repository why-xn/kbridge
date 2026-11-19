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
	files := http.FileServer(http.FS(sub))
	index := http.StripPrefix("/", files) // "/" would otherwise redirect from index.html
	// HEAD too: uptime checks and curl -I probe the console with it.
	s.router.Match([]string{http.MethodGet, http.MethodHead}, "/", func(c *gin.Context) {
		s.serveConsole(c, index, "index.html")
	})
	s.router.Match([]string{http.MethodGet, http.MethodHead}, "/assets/*file", func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("file"), "/")
		// A bare or nested-directory path would produce a listing.
		if name == "" || name == "index.html" || strings.HasSuffix(name, "/") {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		s.serveConsole(c, files, name)
	})
}

func (s *HTTPServer) serveConsole(c *gin.Context, h http.Handler, name string) {
	hd := c.Writer.Header()
	hd.Set("Content-Security-Policy", consoleCSP)
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Frame-Options", "DENY")
	if name == "index.html" {
		hd.Set("Cache-Control", "no-store")
	} else {
		hd.Set("Cache-Control", "max-age=300")
	}
	h.ServeHTTP(c.Writer, c.Request)
}

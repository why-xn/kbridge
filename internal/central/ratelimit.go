package central

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// loginLimiter is an in-memory per-key token-bucket rate limiter for auth
// endpoints. Keys combine client IP and email so neither a single IP nor a
// single account can be brute-forced. Single-replica deployment, so per-process
// state is sufficient.
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	rps     rate.Limit
	burst   int
}

func newLoginLimiter(rps float64, burst int) *loginLimiter {
	return &loginLimiter{
		buckets: make(map[string]*rate.Limiter),
		rps:     rate.Limit(rps),
		burst:   burst,
	}
}

const maxBuckets = 10_000

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	b := l.buckets[key]
	if b == nil {
		// Coarse size cap: if the map is already at the limit, reset it entirely.
		// Sufficient for single-replica deployments; prevents unbounded memory growth
		// under IP/email spray attacks.
		if len(l.buckets) >= maxBuckets {
			l.buckets = make(map[string]*rate.Limiter)
		}
		b = rate.NewLimiter(l.rps, l.burst)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.Allow()
}

// loginRateLimitMiddleware limits auth attempts by client IP + email. It reads
// the email from the JSON body without consuming it for the handler.
func loginRateLimitMiddleware(l *loginLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		email := peekEmail(c)
		key := c.ClientIP() + "|" + email
		if !l.allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many attempts, slow down"})
			return
		}
		c.Next()
	}
}

// peekEmail reads the request body to extract the email field, then restores
// the body so the downstream handler can read it normally.
func peekEmail(c *gin.Context) string {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4<<10))
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var v struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Email
}

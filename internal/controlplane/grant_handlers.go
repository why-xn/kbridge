package controlplane

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/why-xn/kbridge/internal/auth"
)

// GrantHandlers serves the just-in-time access endpoints. Requesting is open to
// any authenticated user; deciding is admin-only and routed under /admin.
type GrantHandlers struct {
	grants *GrantService
	limits GrantsConfig
}

// NewGrantHandlers creates handlers over the given service.
func NewGrantHandlers(grants *GrantService, limits GrantsConfig) *GrantHandlers {
	return &GrantHandlers{grants: grants, limits: limits}
}

type createGrantRequest struct {
	Cluster   string `json:"cluster" binding:"required"`
	Namespace string `json:"namespace,omitempty"`
	Duration  string `json:"duration,omitempty"`
	Reason    string `json:"reason" binding:"required"`
}

type decideGrantRequest struct {
	Note     string `json:"note,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// grantView is a Grant plus the status a human should see, which folds a lapsed
// approval into "expired" without rewriting the stored row.
type grantView struct {
	*Grant
	DisplayStatus string `json:"display_status"`
}

func viewGrant(g *Grant) grantView {
	return grantView{Grant: g, DisplayStatus: g.DisplayStatus(time.Now())}
}

func viewGrants(grants []*Grant) []grantView {
	out := make([]grantView, 0, len(grants))
	for _, g := range grants {
		out = append(out, viewGrant(g))
	}
	return out
}

// HandleRequestGrant records a pending access request for the caller.
func (h *GrantHandlers) HandleRequestGrant(c *gin.Context) {
	claims := auth.GetUserFromContext(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	var req createGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	d, err := h.duration(req.Duration)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	g, err := h.grants.Request(c.Request.Context(), claims.Email, claims.UserID,
		req.Cluster, req.Namespace, d, req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, viewGrant(g))
}

// duration resolves a requested window, falling back to the configured default.
func (h *GrantHandlers) duration(s string) (time.Duration, error) {
	if s == "" {
		return h.limits.EffectiveDefaultDuration(), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("invalid duration: use a form like 30m or 2h")
	}
	return d, nil
}

// HandleListMyGrants returns the caller's own grants.
func (h *GrantHandlers) HandleListMyGrants(c *gin.Context) {
	claims := auth.GetUserFromContext(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	grants, err := h.grants.List(c.Request.Context(), GrantFilter{
		Subject: claims.Email,
		Status:  c.Query("status"),
		Limit:   clampGrantLimit(atoiDefault(c.Query("limit"), 50)),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"grants": viewGrants(grants)})
}

// HandleListGrants returns every grant, for administrators triaging requests.
func (h *GrantHandlers) HandleListGrants(c *gin.Context) {
	grants, err := h.grants.List(c.Request.Context(), GrantFilter{
		Subject: c.Query("subject"),
		Status:  c.Query("status"),
		Limit:   clampGrantLimit(atoiDefault(c.Query("limit"), 50)),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"grants": viewGrants(grants)})
}

// HandleApproveGrant activates a pending grant.
func (h *GrantHandlers) HandleApproveGrant(c *gin.Context) {
	h.decide(c, func(approver string, req decideGrantRequest) (*Grant, error) {
		d, err := h.overrideDuration(req.Duration)
		if err != nil {
			return nil, err
		}
		return h.grants.Approve(c.Request.Context(), c.Param("id"), approver, req.Note, d)
	})
}

// HandleDenyGrant rejects a pending grant.
func (h *GrantHandlers) HandleDenyGrant(c *gin.Context) {
	h.decide(c, func(approver string, req decideGrantRequest) (*Grant, error) {
		return h.grants.Deny(c.Request.Context(), c.Param("id"), approver, req.Note)
	})
}

// HandleRevokeGrant ends an approved grant early.
func (h *GrantHandlers) HandleRevokeGrant(c *gin.Context) {
	h.decide(c, func(approver string, req decideGrantRequest) (*Grant, error) {
		return h.grants.Revoke(c.Request.Context(), c.Param("id"), approver, req.Note)
	})
}

// decide runs a decision action, mapping domain errors onto status codes. The
// body is optional, so a bare POST is a valid approval.
func (h *GrantHandlers) decide(c *gin.Context, action func(string, decideGrantRequest) (*Grant, error)) {
	var req decideGrantRequest
	_ = c.ShouldBindJSON(&req)

	approver := ""
	if claims := auth.GetUserFromContext(c); claims != nil {
		approver = claims.Email
	}

	g, err := action(approver, req)
	switch {
	case errors.Is(err, ErrGrantNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "grant not found"})
	case err != nil:
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, viewGrant(g))
	}
}

// overrideDuration parses an approver's shortened window, if given.
func (h *GrantHandlers) overrideDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("invalid duration: use a form like 30m or 2h")
	}
	return d, nil
}

// clampGrantLimit bounds a listing so one request cannot pull the whole table.
func clampGrantLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	default:
		return n
	}
}

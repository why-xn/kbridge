package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// GrantInfo is one access grant as returned by the control plane.
type GrantInfo struct {
	ID            string     `json:"id"`
	Subject       string     `json:"subject"`
	ClusterName   string     `json:"cluster_name"`
	Namespace     string     `json:"namespace,omitempty"`
	Status        string     `json:"status"`
	DisplayStatus string     `json:"display_status"`
	Reason        string     `json:"reason"`
	Duration      string     `json:"duration"`
	RequestedAt   time.Time  `json:"requested_at"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	DecidedBy     string     `json:"decided_by,omitempty"`
	DecisionNote  string     `json:"decision_note,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

type grantsResponse struct {
	Grants []GrantInfo `json:"grants"`
}

// RequestGrant asks for time-boxed access to a cluster.
func (c *ControlPlaneClient) RequestGrant(cluster, namespace, duration, reason string) (*GrantInfo, error) {
	body, _ := json.Marshal(map[string]string{
		"cluster":   cluster,
		"namespace": namespace,
		"duration":  duration,
		"reason":    reason,
	})
	req, err := newJSONRequest(http.MethodPost, c.baseURL+"/api/v1/grants", body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	return c.grantResult(req, http.StatusCreated)
}

// ListMyGrants returns the caller's own grants.
func (c *ControlPlaneClient) ListMyGrants(status string, limit int) ([]GrantInfo, error) {
	return c.listGrants("/api/v1/grants", url.Values{
		"status": {status},
		"limit":  {strconv.Itoa(limit)},
	})
}

// ListAllGrants returns every grant. Admin only.
func (c *ControlPlaneClient) ListAllGrants(subject, status string, limit int) ([]GrantInfo, error) {
	return c.listGrants("/api/v1/admin/grants", url.Values{
		"subject": {subject},
		"status":  {status},
		"limit":   {strconv.Itoa(limit)},
	})
}

// listGrants fetches and decodes a grant listing.
func (c *ControlPlaneClient) listGrants(path string, q url.Values) ([]GrantInfo, error) {
	for k, v := range q {
		if len(v) == 0 || v[0] == "" || v[0] == "0" {
			q.Del(k)
		}
	}
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("permission denied: admin role required")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	var gr grantsResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return gr.Grants, nil
}

// ApproveGrant activates a pending grant, optionally shortening its window.
func (c *ControlPlaneClient) ApproveGrant(id, note, duration string) (*GrantInfo, error) {
	return c.decideGrant(http.MethodPost, "/api/v1/admin/grants/"+url.PathEscape(id)+"/approve",
		map[string]string{"note": note, "duration": duration})
}

// DenyGrant rejects a pending grant.
func (c *ControlPlaneClient) DenyGrant(id, note string) (*GrantInfo, error) {
	return c.decideGrant(http.MethodPost, "/api/v1/admin/grants/"+url.PathEscape(id)+"/deny",
		map[string]string{"note": note})
}

// RevokeGrant ends an approved grant early.
func (c *ControlPlaneClient) RevokeGrant(id, note string) (*GrantInfo, error) {
	return c.decideGrant(http.MethodDelete, "/api/v1/admin/grants/"+url.PathEscape(id),
		map[string]string{"note": note})
}

// decideGrant posts a decision and decodes the updated grant.
func (c *ControlPlaneClient) decideGrant(method, path string, payload map[string]string) (*GrantInfo, error) {
	body, _ := json.Marshal(payload)
	req, err := newJSONRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	return c.grantResult(req, http.StatusOK)
}

// grantResult executes a request and decodes a single grant, turning the
// control plane's error bodies into messages a user can act on.
func (c *ControlPlaneClient) grantResult(req *http.Request, want int) (*GrantInfo, error) {
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		return nil, grantError(resp.StatusCode, body)
	}
	var g GrantInfo
	if err := json.Unmarshal(body, &g); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &g, nil
}

// grantError renders a failed grant call. The control plane returns 404 for an
// unknown ID and 409 for a grant that is not in a state the action allows.
func grantError(status int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)
	switch {
	case payload.Error != "":
		return fmt.Errorf("%s", payload.Error)
	case status == http.StatusNotFound:
		return fmt.Errorf("grant not found")
	case status == http.StatusForbidden:
		return fmt.Errorf("permission denied: admin role required")
	default:
		return fmt.Errorf("server returned %d: %s", status, string(body))
	}
}

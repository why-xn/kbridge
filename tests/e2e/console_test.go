//go:build e2e
// +build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestConsole covers the admin console and the API calls it makes, against the
// real control plane and agent.
func TestConsole(t *testing.T) {
	base := *controlPlaneURL

	t.Run("serves /ui/ with a strict CSP", func(t *testing.T) {
		resp, err := http.Get(base + "/ui/")
		if err != nil {
			t.Fatalf("GET /ui/: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("want 200, got %d", resp.StatusCode)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("missing CSP, got %q", csp)
		}
	})

	t.Run("clusters lists the connected e2e cluster", func(t *testing.T) {
		body, code := httpGetAuth(t, base+"/api/v1/admin/clusters", authToken)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", code, body)
		}
		var resp struct {
			Clusters []struct {
				Name          string  `json:"name"`
				Status        string  `json:"status"`
				LastHeartbeat *string `json:"last_heartbeat"`
				EverConnected bool    `json:"ever_connected"`
			} `json:"clusters"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, c := range resp.Clusters {
			if c.Name != *clusterName {
				continue
			}
			if c.Status != "connected" || !c.EverConnected || c.LastHeartbeat == nil {
				t.Errorf("want connected, ever_connected, heartbeat set; got %+v", c)
			}
			return
		}
		t.Fatalf("cluster %q not listed: %s", *clusterName, body)
	})

	t.Run("users carries roles", func(t *testing.T) {
		body, code := httpGetAuth(t, base+"/api/v1/admin/users", authToken)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", code, body)
		}
		var resp struct {
			Users []struct {
				Email string   `json:"email"`
				Roles []string `json:"roles"`
			} `json:"users"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, u := range resp.Users {
			if u.Email == "admin@e2e.test" {
				if len(u.Roles) != 1 || u.Roles[0] != "admin" {
					t.Errorf("want roles [admin], got %v", u.Roles)
				}
				return
			}
		}
		t.Fatalf("admin@e2e.test not listed: %s", body)
	})

	t.Run("audit filters by namespace and command", func(t *testing.T) {
		_, code := httpPostAuth(t, fmt.Sprintf("%s/api/v1/clusters/%s/exec", base, *clusterName), authToken,
			map[string]any{"command": []string{"get", "pods"}}) // no -n: recorded as the enforced default
		if code != http.StatusOK {
			t.Fatalf("exec: want 200, got %d", code)
		}
		body, code := httpGetAuth(t, base+"/api/v1/admin/audit?namespace=default&command=GET", authToken)
		if code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", code, body)
		}
		var resp struct {
			Logs []struct {
				Namespace string `json:"namespace"`
				Command   string `json:"command"`
			} `json:"logs"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(resp.Logs) == 0 {
			t.Fatal("want at least one matching entry")
		}
		for _, l := range resp.Logs {
			if l.Namespace != "default" || !strings.Contains(strings.ToLower(l.Command), "get") {
				t.Errorf("row does not match both filters: %+v", l)
			}
		}
	})
}

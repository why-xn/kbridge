package cli

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/viper"
)

// CentralClient is an HTTP client for the central service API.
type CentralClient struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// LoginResponse is the response from POST /auth/login.
type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// ClusterInfo represents a cluster returned by the API.
type ClusterInfo struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	KubernetesVersion string `json:"kubernetes_version,omitempty"`
	NodeCount         int32  `json:"node_count,omitempty"`
	Region            string `json:"region,omitempty"`
	Provider          string `json:"provider,omitempty"`
}

// ClustersResponse is the response from GET /api/v1/clusters.
type ClustersResponse struct {
	Clusters []ClusterInfo `json:"clusters"`
}

// NewCentralClient creates a new client for the central service.
func NewCentralClient(baseURL string) *CentralClient {
	return &CentralClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetToken sets the auth token for authenticated requests.
func (c *CentralClient) SetToken(token string) {
	c.token = token
}

// SetInsecureSkipVerify disables TLS certificate verification for HTTPS
// requests. Intended for development with self-signed certificates only.
func (c *CentralClient) SetInsecureSkipVerify(skip bool) {
	if !skip {
		return
	}
	c.httpClient.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

// doRequest executes an HTTP request with the auth token if set.
func (c *CentralClient) doRequest(req *http.Request) (*http.Response, error) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, fmt.Errorf("authentication required: run 'kbridge login' first")
	}
	return resp, nil
}

// Login authenticates with the central service and returns tokens.
func (c *CentralClient) Login(email, password string) (*LoginResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid email or password")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("account is disabled")
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed: %s", string(respBody))
	}

	var loginResp LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &loginResp, nil
}

// Logout invalidates the refresh token on the server.
func (c *CentralClient) Logout(refreshToken string) error {
	body, _ := json.Marshal(map[string]string{
		"refresh_token": refreshToken,
	})

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/auth/logout", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ListClusters fetches the list of clusters from the central service.
func (c *CentralClient) ListClusters() ([]ClusterInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters", c.baseURL)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var clustersResp ClustersResponse
	if err := json.NewDecoder(resp.Body).Decode(&clustersResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return clustersResp.Clusters, nil
}

// GetCluster fetches a specific cluster by name.
func (c *CentralClient) GetCluster(name string) (*ClusterInfo, error) {
	clusters, err := c.ListClusters()
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusters {
		if cluster.Name == name {
			return &cluster, nil
		}
	}

	return nil, fmt.Errorf("cluster %q not found", name)
}

// UserInfo represents a user returned by the admin API.
type UserInfo struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	CreatedAt string `json:"created_at"`
}

// ListUsers fetches all users via the admin API.
func (c *CentralClient) ListUsers() ([]UserInfo, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v1/admin/users", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("admin role required")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Users []UserInfo `json:"users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return out.Users, nil
}

// CreateUser creates a new user via the admin API.
func (c *CentralClient) CreateUser(email, name, password string) (*UserInfo, error) {
	body, _ := json.Marshal(map[string]string{
		"email": email, "name": name, "password": password,
	})
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/admin/users", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		var u UserInfo
		if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		return &u, nil
	case http.StatusConflict:
		return nil, fmt.Errorf("a user with email %q already exists", email)
	case http.StatusForbidden:
		return nil, fmt.Errorf("admin role required")
	default:
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
}

// AuditLogInfo represents one audit record returned by the admin API.
type AuditLogInfo struct {
	UserEmail   string `json:"user_email"`
	ClusterName string `json:"cluster_name"`
	Command     string `json:"command"`
	Namespace   string `json:"namespace"`
	Status      string `json:"status"`
	ExitCode    *int32 `json:"exit_code"`
	CreatedAt   string `json:"created_at"`
}

// ListAuditLogs fetches audit logs, applying the given query filters
// (user, cluster, status, per_page, ...).
func (c *CentralClient) ListAuditLogs(filters map[string]string) ([]AuditLogInfo, int, error) {
	q := url.Values{}
	for k, v := range filters {
		if v != "" {
			q.Set(k, v)
		}
	}
	reqURL := c.baseURL + "/api/v1/admin/audit"
	if encoded := q.Encode(); encoded != "" {
		reqURL += "?" + encoded
	}

	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, 0, fmt.Errorf("admin role required")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Logs  []AuditLogInfo `json:"logs"`
		Total int            `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, fmt.Errorf("decoding response: %w", err)
	}
	return out.Logs, out.Total, nil
}

// AgentTokenInfo represents an agent token returned by the admin API. The
// plaintext Token is only populated by CreateAgentToken.
type AgentTokenInfo struct {
	ID          string `json:"id"`
	ClusterName string `json:"cluster_name"`
	TokenPrefix string `json:"token_prefix"`
	Description string `json:"description,omitempty"`
	IsRevoked   bool   `json:"is_revoked"`
	Token       string `json:"token,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// CreateAgentToken generates a new agent token for a cluster. The returned
// Token is the plaintext secret, shown only once.
func (c *CentralClient) CreateAgentToken(cluster, description string, expiresInDays int) (*AgentTokenInfo, error) {
	payload := map[string]any{"cluster_name": cluster}
	if description != "" {
		payload["description"] = description
	}
	if expiresInDays > 0 {
		payload["expires_in_days"] = expiresInDays
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v1/admin/agent-tokens", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusCreated:
		var tok AgentTokenInfo
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		return &tok, nil
	case http.StatusForbidden:
		return nil, fmt.Errorf("admin role required")
	default:
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
}

// ListAgentTokens lists agent tokens, optionally filtered by cluster.
func (c *CentralClient) ListAgentTokens(cluster string) ([]AgentTokenInfo, error) {
	reqURL := c.baseURL + "/api/v1/admin/agent-tokens"
	if cluster != "" {
		reqURL += "?cluster=" + url.QueryEscape(cluster)
	}
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("admin role required")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		Tokens []AgentTokenInfo `json:"tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return out.Tokens, nil
}

// RevokeAgentToken revokes an agent token by ID.
func (c *CentralClient) RevokeAgentToken(id string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/v1/admin/agent-tokens/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("admin role required")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// CheckHealth checks if the central service is healthy.
func (c *CentralClient) CheckHealth() error {
	url := fmt.Sprintf("%s/health", c.baseURL)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}

// ExecRequest represents a command execution request.
type ExecRequest struct {
	Command   []string `json:"command"`
	Namespace string   `json:"namespace,omitempty"`
	Timeout   int      `json:"timeout,omitempty"`
}

// ExecResponse represents a command execution response.
type ExecResponse struct {
	Output   string `json:"output"`
	ExitCode int32  `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// ExecCommand executes a kubectl command on the specified cluster.
func (c *CentralClient) ExecCommand(clusterName string, command []string, namespace string, timeout int) (*ExecResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/exec", c.baseURL, clusterName)

	reqBody := ExecRequest{
		Command:   command,
		Namespace: namespace,
		Timeout:   timeout,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return c.parseExecResponse(resp, clusterName)
}

// NewCentralClientWithTimeout creates a client with a custom timeout.
func NewCentralClientWithTimeout(baseURL string, timeout time.Duration) *CentralClient {
	return &CentralClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// newAuthenticatedClient creates a client with the stored auth token.
func newAuthenticatedClient(baseURL string) *CentralClient {
	c := NewCentralClient(baseURL)
	if token := viper.GetString(ConfigKeyToken); token != "" {
		c.SetToken(token)
	}
	c.SetInsecureSkipVerify(viper.GetBool(ConfigKeyInsecure))
	return c
}

// newAuthenticatedClientWithTimeout creates a client with auth token and custom timeout.
func newAuthenticatedClientWithTimeout(baseURL string, timeout time.Duration) *CentralClient {
	c := NewCentralClientWithTimeout(baseURL, timeout)
	if token := viper.GetString(ConfigKeyToken); token != "" {
		c.SetToken(token)
	}
	c.SetInsecureSkipVerify(viper.GetBool(ConfigKeyInsecure))
	return c
}

// ExecRequestWithStdin represents a command execution request with stdin input.
type ExecRequestWithStdin struct {
	Command   []string `json:"command"`
	Namespace string   `json:"namespace,omitempty"`
	Timeout   int      `json:"timeout,omitempty"`
	Stdin     string   `json:"stdin,omitempty"`
}

// ExecCommandWithStdin executes a kubectl command with stdin input.
func (c *CentralClient) ExecCommandWithStdin(clusterName string, command []string, namespace string, timeout int, stdin string) (*ExecResponse, error) {
	url := fmt.Sprintf("%s/api/v1/clusters/%s/exec", c.baseURL, clusterName)

	reqBody := ExecRequestWithStdin{
		Command:   command,
		Namespace: namespace,
		Timeout:   timeout,
		Stdin:     stdin,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return c.parseExecResponse(resp, clusterName)
}

func (c *CentralClient) parseExecResponse(resp *http.Response, clusterName string) (*ExecResponse, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("cluster %q not found", clusterName)
	}
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("cluster %q agent is disconnected", clusterName)
	}
	if resp.StatusCode == http.StatusGatewayTimeout {
		return nil, fmt.Errorf("command execution timed out")
	}

	var execResp ExecResponse
	if err := json.Unmarshal(body, &execResp); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &execResp, nil
}

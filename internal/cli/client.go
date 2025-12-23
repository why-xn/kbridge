package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CentralClient is an HTTP client for the central service API.
type CentralClient struct {
	baseURL    string
	httpClient *http.Client
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

// ListClusters fetches the list of clusters from the central service.
func (c *CentralClient) ListClusters() ([]ClusterInfo, error) {
	url := fmt.Sprintf("%s/api/v1/clusters", c.baseURL)

	resp, err := c.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
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

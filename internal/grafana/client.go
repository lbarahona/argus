package grafana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client communicates with a Grafana HTTP API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new Grafana client from config.
func NewClient(cfg GrafanaConfig) *Client {
	return &Client{
		baseURL: strings.TrimRight(cfg.URL, "/"),
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// NewClientWithHTTP creates a client with a custom HTTP client (for testing).
func NewClientWithHTTP(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

// NewClientWithHTTPAndKey creates a client with custom HTTP client and API key (for testing).
func NewClientWithHTTPAndKey(baseURL string, httpClient *http.Client, apiKey string) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
		apiKey:     apiKey,
	}
}

func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return c.httpClient.Do(req)
}

func decodeJSON[T any](resp *http.Response) (T, error) {
	var result T
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return result, fmt.Errorf("decoding JSON: %w", err)
	}
	return result, nil
}

// Search searches dashboards and folders.
// query is optional — empty string returns all.
// kind can be "dash-db", "dash-folder", or empty for both.
func (c *Client) Search(ctx context.Context, query, kind string, limit int) ([]SearchResult, error) {
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	if kind != "" {
		params.Set("type", kind)
	}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := "/api/search"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	resp, err := c.do(ctx, http.MethodGet, path)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}
	return decodeJSON[[]SearchResult](resp)
}

// Dashboards returns all dashboards (not folders).
func (c *Client) Dashboards(ctx context.Context) ([]Dashboard, error) {
	return c.Search(ctx, "", "dash-db", 1000)
}

// Folders returns all folders.
func (c *Client) Folders(ctx context.Context) ([]Folder, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/folders")
	if err != nil {
		return nil, fmt.Errorf("folders request: %w", err)
	}
	return decodeJSON[[]Folder](resp)
}

// GetDashboard returns full dashboard details by UID.
func (c *Client) GetDashboard(ctx context.Context, uid string) (*DashboardMeta, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/dashboards/uid/"+uid)
	if err != nil {
		return nil, fmt.Errorf("get dashboard request: %w", err)
	}
	result, err := decodeJSON[DashboardMeta](resp)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Datasources returns all configured data sources.
func (c *Client) Datasources(ctx context.Context) ([]Datasource, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/datasources")
	if err != nil {
		return nil, fmt.Errorf("datasources request: %w", err)
	}
	return decodeJSON[[]Datasource](resp)
}

// AlertRules returns all provisioned alert rules.
func (c *Client) AlertRules(ctx context.Context) ([]AlertRule, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/provisioning/alert-rules")
	if err != nil {
		return nil, fmt.Errorf("alert rules request: %w", err)
	}
	return decodeJSON[[]AlertRule](resp)
}

// AlertInstances returns firing/pending alert instances from Grafana's built-in Alertmanager.
func (c *Client) AlertInstances(ctx context.Context) ([]GrafanaAlertInstance, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/alertmanager/grafana/api/v2/alerts")
	if err != nil {
		return nil, fmt.Errorf("alert instances request: %w", err)
	}
	return decodeJSON[[]GrafanaAlertInstance](resp)
}

// Health returns the Grafana health status.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/health")
	if err != nil {
		return nil, fmt.Errorf("health request: %w", err)
	}
	result, err := decodeJSON[HealthResponse](resp)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// Org returns the current organization info.
func (c *Client) Org(ctx context.Context) (*OrgInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/org")
	if err != nil {
		return nil, fmt.Errorf("org request: %w", err)
	}
	result, err := decodeJSON[OrgInfo](resp)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// BuildSummary fetches multiple endpoints to build a summary overview.
func (c *Client) BuildSummary(ctx context.Context) (*Summary, error) {
	s := &Summary{}

	// Get dashboards count
	results, err := c.Search(ctx, "", "", 5000)
	if err == nil {
		for _, r := range results {
			switch r.Type {
			case "dash-folder":
				s.Folders++
			default:
				s.Dashboards++
			}
		}
	}

	// Get datasources count
	ds, err := c.Datasources(ctx)
	if err == nil {
		s.Datasources = len(ds)
	}

	// Get alert rules count
	rules, err := c.AlertRules(ctx)
	if err == nil {
		s.AlertRules = len(rules)
	}

	// Get firing alerts
	instances, err := c.AlertInstances(ctx)
	if err == nil {
		for _, inst := range instances {
			if inst.Status.State == "active" {
				s.FiringAlerts++
			}
		}
	}

	// Get version
	health, err := c.Health(ctx)
	if err == nil {
		s.Version = health.Version
	}

	// Get org name
	org, err := c.Org(ctx)
	if err == nil {
		s.OrgName = org.Name
	}

	return s, nil
}

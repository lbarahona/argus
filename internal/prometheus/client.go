package prometheus

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

// Client communicates with a Prometheus HTTP API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	basicAuth  *BasicAuth
}

// NewClient creates a new Prometheus client.
func NewClient(cfg PrometheusConfig) *Client {
	baseURL := strings.TrimRight(cfg.URL, "/")
	c := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	if cfg.BasicAuth.Username != "" {
		c.basicAuth = &cfg.BasicAuth
	}
	return c
}

// NewClientWithHTTP creates a client with a custom HTTP client (for testing).
func NewClientWithHTTP(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: httpClient,
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return nil, err
	}
	if c.basicAuth != nil {
		req.SetBasicAuth(c.basicAuth.Username, c.basicAuth.Password)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return c.httpClient.Do(req)
}

func (c *Client) decodeAPI(resp *http.Response) (*APIResponse, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("prometheus API returned %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var apiResp APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if apiResp.Status != "success" {
		return nil, fmt.Errorf("prometheus error (%s): %s", apiResp.ErrorType, apiResp.Error)
	}
	return &apiResp, nil
}

// Rules fetches alerting and recording rules.
// ruleType can be "alert", "record", or "" for all.
func (c *Client) Rules(ctx context.Context, ruleType string) (*RulesData, error) {
	path := "/api/v1/rules"
	if ruleType != "" {
		path += "?type=" + url.QueryEscape(ruleType)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("rules request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data RulesData
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode rules data: %w", err)
	}
	return &data, nil
}

// Alerts fetches currently firing alerts from Prometheus.
func (c *Client) Alerts(ctx context.Context) (*AlertsData, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/alerts", nil)
	if err != nil {
		return nil, fmt.Errorf("alerts request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data AlertsData
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode alerts data: %w", err)
	}
	return &data, nil
}

// Targets fetches scrape targets status.
func (c *Client) Targets(ctx context.Context) (*TargetsData, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/targets", nil)
	if err != nil {
		return nil, fmt.Errorf("targets request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data TargetsData
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode targets data: %w", err)
	}
	return &data, nil
}

// Query executes an instant PromQL query.
func (c *Client) Query(ctx context.Context, query string, ts *time.Time) (*QueryResult, error) {
	params := url.Values{}
	params.Set("query", query)
	if ts != nil {
		params.Set("time", fmt.Sprintf("%d", ts.Unix()))
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/query", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("query request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data QueryResult
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode query data: %w", err)
	}
	return &data, nil
}

// QueryRange executes a range PromQL query.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryResult, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", fmt.Sprintf("%ds", int(step.Seconds())))
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/query_range", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("query_range request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data QueryResult
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode query_range data: %w", err)
	}
	return &data, nil
}

// RuntimeInfo fetches Prometheus runtime information.
func (c *Client) RuntimeInfo(ctx context.Context) (*RuntimeInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/status/runtimeinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("runtime info request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data RuntimeInfo
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode runtime info: %w", err)
	}
	return &data, nil
}

// BuildInfo fetches Prometheus build information.
func (c *Client) BuildInfo(ctx context.Context) (*BuildInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/status/buildinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("build info request failed: %w", err)
	}
	apiResp, err := c.decodeAPI(resp)
	if err != nil {
		return nil, err
	}
	var data BuildInfo
	if err := json.Unmarshal(apiResp.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to decode build info: %w", err)
	}
	return &data, nil
}

// Healthy checks the Prometheus /-/healthy endpoint.
func (c *Client) Healthy(ctx context.Context) (bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/-/healthy", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

// Ready checks the Prometheus /-/ready endpoint.
func (c *Client) Ready(ctx context.Context) (bool, error) {
	resp, err := c.do(ctx, http.MethodGet, "/-/ready", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}

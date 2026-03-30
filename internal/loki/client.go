package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client communicates with a Loki HTTP API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	basicAuth  *BasicAuth
	tenantID   string
}

// NewClient creates a new Loki client from config.
func NewClient(cfg LokiConfig) *Client {
	baseURL := strings.TrimRight(cfg.URL, "/")
	c := &Client{
		baseURL:  baseURL,
		tenantID: cfg.TenantID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
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
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) decodeJSON(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("loki API error (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// Healthy checks if Loki is reachable via /ready.
func (c *Client) Healthy(ctx context.Context) (bool, time.Duration, error) {
	start := time.Now()
	resp, err := c.do(ctx, http.MethodGet, "/ready", nil)
	latency := time.Since(start)
	if err != nil {
		return false, latency, err
	}
	resp.Body.Close()
	return resp.StatusCode == 200, latency, nil
}

// BuildInfo returns Loki version and build info from /loki/api/v1/status/buildinfo.
func (c *Client) BuildInfo(ctx context.Context) (*BuildInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/loki/api/v1/status/buildinfo", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching build info: %w", err)
	}
	var result BuildInfoResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// Query runs an instant query via /loki/api/v1/query.
func (c *Client) Query(ctx context.Context, logQL string, limit int, at time.Time) (*QueryResult, error) {
	params := url.Values{}
	params.Set("query", logQL)
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}
	if !at.IsZero() {
		params.Set("time", strconv.FormatInt(at.UnixNano(), 10))
	}

	path := "/loki/api/v1/query?" + params.Encode()
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	var result QueryResult
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// QueryRange runs a range query via /loki/api/v1/query_range.
func (c *Client) QueryRange(ctx context.Context, logQL string, start, end time.Time, limit int) (*QueryResult, error) {
	params := url.Values{}
	params.Set("query", logQL)
	params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
	}

	path := "/loki/api/v1/query_range?" + params.Encode()
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("query_range: %w", err)
	}
	var result QueryResult
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Labels returns all label names from /loki/api/v1/labels.
func (c *Client) Labels(ctx context.Context, start, end time.Time) ([]string, error) {
	params := url.Values{}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}

	path := "/loki/api/v1/labels"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("labels: %w", err)
	}
	var result LabelNamesResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// LabelValues returns values for a label from /loki/api/v1/label/<name>/values.
func (c *Client) LabelValues(ctx context.Context, name string, start, end time.Time) ([]string, error) {
	params := url.Values{}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}

	path := "/loki/api/v1/label/" + url.PathEscape(name) + "/values"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("label values for %s: %w", name, err)
	}
	var result LabelValuesResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// Series returns matching series from /loki/api/v1/series.
func (c *Client) Series(ctx context.Context, matchers []string, start, end time.Time) ([]map[string]string, error) {
	params := url.Values{}
	for _, m := range matchers {
		params.Add("match[]", m)
	}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}

	path := "/loki/api/v1/series?" + params.Encode()
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}
	var result SeriesResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// IndexStats returns ingestion stats from /loki/api/v1/index/stats.
func (c *Client) IndexStats(ctx context.Context, query string, start, end time.Time) (*StatsResponse, error) {
	params := url.Values{}
	if query != "" {
		params.Set("query", query)
	}
	if !start.IsZero() {
		params.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	}
	if !end.IsZero() {
		params.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	}

	path := "/loki/api/v1/index/stats"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("index stats: %w", err)
	}
	var stats StatsResponse
	if err := c.decodeJSON(resp, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// BuildSummary gathers information for the summary command.
func (c *Client) BuildSummary(ctx context.Context) (*Summary, error) {
	s := &Summary{}

	// Health check
	healthy, latency, _ := c.Healthy(ctx)
	s.Healthy = healthy
	s.Latency = latency

	// Version
	if info, err := c.BuildInfo(ctx); err == nil {
		s.Version = info.Version
	}

	// Labels count
	now := time.Now()
	start := now.Add(-1 * time.Hour)
	if labels, err := c.Labels(ctx, start, now); err == nil {
		s.Labels = len(labels)
	}

	// Series count
	if series, err := c.Series(ctx, []string{`{__name__=~".+"}`}, start, now); err == nil {
		s.Series = len(series)
	}

	// Stats
	if stats, err := c.IndexStats(ctx, "", start, now); err == nil {
		s.Stats = stats
	}

	return s, nil
}

// ParseEntries flattens streams into individual log entries sorted by time.
func ParseEntries(result *QueryResult) []LogEntry {
	if result == nil {
		return nil
	}
	var entries []LogEntry
	for _, stream := range result.Data.Result {
		for _, v := range stream.Values {
			if len(v) < 2 {
				continue
			}
			ts, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				continue
			}
			entries = append(entries, LogEntry{
				Timestamp: time.Unix(0, ts),
				Line:      v[1],
				Labels:    stream.Labels,
			})
		}
	}
	return entries
}

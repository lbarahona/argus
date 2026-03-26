package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client communicates with an Alertmanager API (v2).
type Client struct {
	baseURL    string
	httpClient *http.Client
	basicAuth  *BasicAuth
}

// NewClient creates a new Alertmanager client.
func NewClient(cfg AlertmanagerConfig) *Client {
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
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

func (c *Client) decodeJSON(resp *http.Response, target interface{}) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("alertmanager API error (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

// Status returns the Alertmanager status.
func (c *Client) Status(ctx context.Context) (*AMStatus, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/status", nil)
	if err != nil {
		return nil, fmt.Errorf("fetching status: %w", err)
	}
	var status AMStatus
	if err := c.decodeJSON(resp, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// Healthy checks if Alertmanager is reachable.
func (c *Client) Healthy(ctx context.Context) (bool, time.Duration, error) {
	start := time.Now()
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/status", nil)
	latency := time.Since(start)
	if err != nil {
		return false, latency, err
	}
	resp.Body.Close()
	return resp.StatusCode == 200, latency, nil
}

// ListAlerts returns all alerts, optionally filtered.
func (c *Client) ListAlerts(ctx context.Context, opts *AlertListOptions) ([]Alert, error) {
	path := "/api/v2/alerts"
	if opts != nil {
		q := opts.QueryParams()
		if q != "" {
			path += "?" + q
		}
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("listing alerts: %w", err)
	}
	var alerts []Alert
	if err := c.decodeJSON(resp, &alerts); err != nil {
		return nil, err
	}
	return alerts, nil
}

// ListAlertGroups returns alert groups.
func (c *Client) ListAlertGroups(ctx context.Context) ([]AlertGroup, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/alerts/groups", nil)
	if err != nil {
		return nil, fmt.Errorf("listing alert groups: %w", err)
	}
	var groups []AlertGroup
	if err := c.decodeJSON(resp, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// ListSilences returns all silences.
func (c *Client) ListSilences(ctx context.Context) ([]Silence, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/silences", nil)
	if err != nil {
		return nil, fmt.Errorf("listing silences: %w", err)
	}
	var silences []Silence
	if err := c.decodeJSON(resp, &silences); err != nil {
		return nil, err
	}
	return silences, nil
}

// GetSilence returns a specific silence by ID.
func (c *Client) GetSilence(ctx context.Context, id string) (*Silence, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/silence/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, fmt.Errorf("getting silence %s: %w", id, err)
	}
	var silence Silence
	if err := c.decodeJSON(resp, &silence); err != nil {
		return nil, err
	}
	return &silence, nil
}

// CreateSilence creates a new silence and returns its ID.
func (c *Client) CreateSilence(ctx context.Context, req SilenceRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshaling silence: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/v2/silences", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating silence: %w", err)
	}
	var result SilenceResponse
	if err := c.decodeJSON(resp, &result); err != nil {
		return "", err
	}
	return result.SilenceID, nil
}

// DeleteSilence expires a silence by ID.
func (c *Client) DeleteSilence(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v2/silence/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("deleting silence %s: %w", id, err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("delete silence failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// ListReceivers returns configured receivers.
func (c *Client) ListReceivers(ctx context.Context) ([]Receiver, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v2/receivers", nil)
	if err != nil {
		return nil, fmt.Errorf("listing receivers: %w", err)
	}
	var receivers []Receiver
	if err := c.decodeJSON(resp, &receivers); err != nil {
		return nil, err
	}
	return receivers, nil
}

// AlertListOptions controls filtering of the alert list.
type AlertListOptions struct {
	Active       *bool    // Show active alerts
	Silenced     *bool    // Show silenced alerts
	Inhibited    *bool    // Show inhibited alerts
	Unprocessed  *bool    // Show unprocessed alerts
	Filter       []string // Label filter (PromQL-style: alertname="Foo")
	Receiver     string   // Filter by receiver
}

// QueryParams converts options to URL query string.
func (o *AlertListOptions) QueryParams() string {
	params := url.Values{}
	if o.Active != nil {
		params.Set("active", fmt.Sprintf("%t", *o.Active))
	}
	if o.Silenced != nil {
		params.Set("silenced", fmt.Sprintf("%t", *o.Silenced))
	}
	if o.Inhibited != nil {
		params.Set("inhibited", fmt.Sprintf("%t", *o.Inhibited))
	}
	if o.Unprocessed != nil {
		params.Set("unprocessed", fmt.Sprintf("%t", *o.Unprocessed))
	}
	for _, f := range o.Filter {
		params.Add("filter", f)
	}
	if o.Receiver != "" {
		params.Set("receiver", o.Receiver)
	}
	return params.Encode()
}

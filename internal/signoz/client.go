package signoz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// Client communicates with a Signoz instance.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// New creates a new Signoz client.
func New(instance types.Instance) *Client {
	return &Client{
		baseURL: instance.URL,
		apiKey:  instance.APIKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("SIGNOZ-API-KEY", c.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(req)
}

// Health checks if the Signoz instance is reachable.
func (c *Client) Health(ctx context.Context) (bool, time.Duration, error) {
	start := time.Now()
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/health", nil)
	latency := time.Since(start)
	if err != nil {
		return false, latency, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, latency, nil
	}
	return false, latency, fmt.Errorf("status %d", resp.StatusCode)
}

// ListServices returns services known to Signoz.
func (c *Client) ListServices(ctx context.Context) ([]types.Service, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/services", nil)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	defer resp.Body.Close()

	var services []types.Service
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("decoding services: %w", err)
	}
	return services, nil
}

// QueryLogs queries logs from Signoz.
// TODO: Implement full query_range API integration when testing against a real instance.
func (c *Client) QueryLogs(ctx context.Context, service string, durationMinutes int) (*types.QueryResult, error) {
	// Placeholder: build proper query_range request
	// The Signoz v3 query_range API uses a composite query body.
	// For now we return a descriptive placeholder.
	_ = service
	_ = durationMinutes
	return &types.QueryResult{
		Raw: fmt.Sprintf("[placeholder] would query logs for service=%q last %dm from %s", service, durationMinutes, c.baseURL),
	}, nil
}

// QueryMetrics queries metrics from Signoz.
// TODO: Implement when testing against a real instance.
func (c *Client) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	_ = metricName
	_ = durationMinutes
	return &types.QueryResult{
		Raw: fmt.Sprintf("[placeholder] would query metric=%q last %dm from %s", metricName, durationMinutes, c.baseURL),
	}, nil
}

// QueryTraces queries traces from Signoz.
// TODO: Implement when testing against a real instance.
func (c *Client) QueryTraces(ctx context.Context, service string, durationMinutes int) (*types.QueryResult, error) {
	_ = service
	_ = durationMinutes
	return &types.QueryResult{
		Raw: fmt.Sprintf("[placeholder] would query traces for service=%q last %dm from %s", service, durationMinutes, c.baseURL),
	}, nil
}

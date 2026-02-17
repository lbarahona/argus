package signoz

import (
	"bytes"
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
	apiVersion string
	httpClient *http.Client
}

// New creates a new Signoz client.
func New(instance types.Instance) *Client {
	return &Client{
		baseURL:    instance.URL,
		apiKey:     instance.APIKey,
		apiVersion: instance.GetAPIVersion(),
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

// queryRangePath returns the appropriate query_range endpoint.
func (c *Client) queryRangePath() string {
	return fmt.Sprintf("/api/%s/query_range", c.apiVersion)
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

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("listing services: status %d: %s", resp.StatusCode, string(body))
	}

	var services []types.Service
	if err := json.NewDecoder(resp.Body).Decode(&services); err != nil {
		return nil, fmt.Errorf("decoding services: %w", err)
	}

	// Calculate error rates
	for i := range services {
		if services[i].NumCalls > 0 {
			services[i].ErrorRate = float64(services[i].NumErrors) / float64(services[i].NumCalls) * 100
		}
	}

	return services, nil
}

// compositeQuery builds a query_range request payload.
func buildQueryRangePayload(signal, requestType string, durationMinutes, limit int, filter, orderField string, aggregations []map[string]interface{}, groupBy []map[string]interface{}) map[string]interface{} {
	now := time.Now()
	start := now.Add(-time.Duration(durationMinutes) * time.Minute)

	spec := map[string]interface{}{
		"name":         "A",
		"signal":       signal,
		"stepInterval": 60,
		"disabled":     false,
	}

	if limit > 0 {
		spec["limit"] = limit
	}

	if filter != "" {
		spec["filter"] = map[string]interface{}{
			"expression": filter,
		}
	}

	if orderField != "" {
		spec["order"] = []map[string]interface{}{
			{"key": map[string]string{"name": orderField}, "direction": "desc"},
		}
	}

	if aggregations != nil {
		spec["aggregations"] = aggregations
	}

	if groupBy != nil {
		spec["groupBy"] = groupBy
	}

	return map[string]interface{}{
		"start":       start.UnixMilli(),
		"end":         now.UnixMilli(),
		"requestType": requestType,
		"compositeQuery": map[string]interface{}{
			"queries": []map[string]interface{}{
				{
					"type": "builder_query",
					"spec": spec,
				},
			},
		},
	}
}

// queryRangeResponse represents the API response structure.
type queryRangeResponse struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
}

// queryRangeResultItem represents a single result series.
type queryRangeResultItem struct {
	QueryName string                   `json:"queryName"`
	Series    []json.RawMessage        `json:"series,omitempty"`
	List      []map[string]interface{} `json:"list,omitempty"`
}

func (c *Client) postQueryRange(ctx context.Context, payload map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling query: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, c.queryRangePath(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("querying: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// QueryLogs queries logs from Signoz.
func (c *Client) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if limit <= 0 {
		limit = 100
	}

	filter := ""
	parts := []string{}
	if service != "" {
		parts = append(parts, fmt.Sprintf("service_name = '%s'", service))
	}
	if severityFilter != "" {
		parts = append(parts, fmt.Sprintf("severity_text = '%s'", severityFilter))
	}
	if len(parts) > 0 {
		filter = joinFilters(parts)
	}

	payload := buildQueryRangePayload("logs", "raw", durationMinutes, limit, filter, "timestamp", nil, nil)

	respBody, err := c.postQueryRange(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("querying logs: %w", err)
	}

	logs, err := parseLogsResponse(respBody)
	if err != nil {
		return nil, err
	}

	return &types.QueryResult{
		Logs: logs,
		Raw:  string(respBody),
	}, nil
}

// QueryMetrics queries metrics from Signoz.
func (c *Client) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	aggregations := []map[string]interface{}{
		{"expression": "avg()"},
	}

	filter := ""
	if metricName != "" {
		filter = fmt.Sprintf("metric_name = '%s'", metricName)
	}

	payload := buildQueryRangePayload("metrics", "time_series", durationMinutes, 0, filter, "", aggregations, nil)

	respBody, err := c.postQueryRange(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}

	metrics, err := parseMetricsResponse(respBody)
	if err != nil {
		return nil, err
	}

	return &types.QueryResult{
		Metrics: metrics,
		Raw:     string(respBody),
	}, nil
}

// QueryTraces queries traces from Signoz.
func (c *Client) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	if limit <= 0 {
		limit = 100
	}

	filter := ""
	if service != "" {
		filter = fmt.Sprintf("service_name = '%s'", service)
	}

	payload := buildQueryRangePayload("traces", "raw", durationMinutes, limit, filter, "timestamp", nil, nil)

	respBody, err := c.postQueryRange(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("querying traces: %w", err)
	}

	traces, err := parseTracesResponse(respBody)
	if err != nil {
		return nil, err
	}

	return &types.QueryResult{
		Traces: traces,
		Raw:    string(respBody),
	}, nil
}

func joinFilters(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += " AND " + p
	}
	return result
}

// parseLogsResponse extracts log entries from the query_range response.
func parseLogsResponse(data []byte) ([]types.LogEntry, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing logs response: %w", err)
	}

	result := resp["data"]
	if result == nil {
		// Try top-level "result"
		result = resp["result"]
	}
	if result == nil {
		return nil, nil
	}

	// The response can have different shapes. Try to extract from list or records.
	resultBytes, _ := json.Marshal(result)
	return extractLogs(resultBytes)
}

func extractLogs(data []byte) ([]types.LogEntry, error) {
	// Try as array of result items with "list" field
	var items []queryRangeResultItem
	if err := json.Unmarshal(data, &items); err == nil && len(items) > 0 {
		var logs []types.LogEntry
		for _, item := range items {
			for _, record := range item.List {
				logs = append(logs, mapToLogEntry(record))
			}
		}
		if len(logs) > 0 {
			return logs, nil
		}
	}

	// Try as flat array of records
	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err == nil {
		var logs []types.LogEntry
		for _, r := range records {
			logs = append(logs, mapToLogEntry(r))
		}
		return logs, nil
	}

	return nil, nil
}

func mapToLogEntry(m map[string]interface{}) types.LogEntry {
	entry := types.LogEntry{
		Attributes: make(map[string]string),
	}

	// Handle nested "data" field (Signoz wraps some responses)
	if data, ok := m["data"].(map[string]interface{}); ok {
		m = data
	}

	if v, ok := m["body"].(string); ok {
		entry.Body = v
	}
	if v, ok := m["severity_text"].(string); ok {
		entry.SeverityText = v
	} else if v, ok := m["severityText"].(string); ok {
		entry.SeverityText = v
	}
	if v, ok := m["service_name"].(string); ok {
		entry.ServiceName = v
	} else if v, ok := m["serviceName"].(string); ok {
		entry.ServiceName = v
	}

	// Parse timestamp
	if v, ok := m["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			entry.Timestamp = t
		}
	} else if v, ok := m["timestamp"].(float64); ok {
		entry.Timestamp = time.UnixMilli(int64(v))
	}

	// Collect remaining fields as attributes
	for k, v := range m {
		switch k {
		case "body", "severity_text", "severityText", "service_name", "serviceName", "timestamp":
			continue
		default:
			if s, ok := v.(string); ok {
				entry.Attributes[k] = s
			}
		}
	}

	return entry
}

func parseTracesResponse(data []byte) ([]types.TraceEntry, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing traces response: %w", err)
	}

	result := resp["data"]
	if result == nil {
		result = resp["result"]
	}
	if result == nil {
		return nil, nil
	}

	resultBytes, _ := json.Marshal(result)

	// Try as array of result items with "list"
	var items []queryRangeResultItem
	if err := json.Unmarshal(resultBytes, &items); err == nil && len(items) > 0 {
		var traces []types.TraceEntry
		for _, item := range items {
			for _, record := range item.List {
				traces = append(traces, mapToTraceEntry(record))
			}
		}
		if len(traces) > 0 {
			return traces, nil
		}
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(resultBytes, &records); err == nil {
		var traces []types.TraceEntry
		for _, r := range records {
			traces = append(traces, mapToTraceEntry(r))
		}
		return traces, nil
	}

	return nil, nil
}

func mapToTraceEntry(m map[string]interface{}) types.TraceEntry {
	entry := types.TraceEntry{
		Attributes: make(map[string]string),
	}

	if data, ok := m["data"].(map[string]interface{}); ok {
		m = data
	}

	if v, ok := m["traceID"].(string); ok {
		entry.TraceID = v
	} else if v, ok := m["trace_id"].(string); ok {
		entry.TraceID = v
	}
	if v, ok := m["spanID"].(string); ok {
		entry.SpanID = v
	} else if v, ok := m["span_id"].(string); ok {
		entry.SpanID = v
	}
	if v, ok := m["parentSpanID"].(string); ok {
		entry.ParentSpanID = v
	}
	if v, ok := m["serviceName"].(string); ok {
		entry.ServiceName = v
	} else if v, ok := m["service_name"].(string); ok {
		entry.ServiceName = v
	}
	if v, ok := m["name"].(string); ok {
		entry.OperationName = v
	} else if v, ok := m["operationName"].(string); ok {
		entry.OperationName = v
	} else if v, ok := m["operation_name"].(string); ok {
		entry.OperationName = v
	}
	if v, ok := m["durationNano"].(float64); ok {
		entry.DurationNano = int64(v)
	} else if v, ok := m["duration_nano"].(float64); ok {
		entry.DurationNano = int64(v)
	}
	if v, ok := m["statusCode"].(string); ok {
		entry.StatusCode = v
	} else if v, ok := m["status_code"].(string); ok {
		entry.StatusCode = v
	}

	if v, ok := m["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			entry.Timestamp = t
		}
	} else if v, ok := m["timestamp"].(float64); ok {
		entry.Timestamp = time.UnixMilli(int64(v))
	}

	return entry
}

func parseMetricsResponse(data []byte) ([]types.MetricEntry, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing metrics response: %w", err)
	}

	result := resp["data"]
	if result == nil {
		result = resp["result"]
	}
	if result == nil {
		return nil, nil
	}

	resultBytes, _ := json.Marshal(result)

	var items []queryRangeResultItem
	if err := json.Unmarshal(resultBytes, &items); err == nil && len(items) > 0 {
		var metrics []types.MetricEntry
		for _, item := range items {
			for _, series := range item.Series {
				var s struct {
					Labels map[string]string `json:"labels"`
					Values [][]interface{}   `json:"values"`
				}
				if err := json.Unmarshal(series, &s); err != nil {
					continue
				}
				for _, v := range s.Values {
					if len(v) >= 2 {
						ts, _ := v[0].(float64)
						val, _ := v[1].(float64)
						metrics = append(metrics, types.MetricEntry{
							Timestamp:  time.UnixMilli(int64(ts)),
							Value:      val,
							Labels:     s.Labels,
						})
					}
				}
			}
		}
		return metrics, nil
	}

	return nil, nil
}

// BuildQueryPayload exposes payload building for testing.
func BuildQueryPayload(signal, requestType string, durationMinutes, limit int, filter, orderField string, aggregations []map[string]interface{}, groupBy []map[string]interface{}) map[string]interface{} {
	return buildQueryRangePayload(signal, requestType, durationMinutes, limit, filter, orderField, aggregations, groupBy)
}

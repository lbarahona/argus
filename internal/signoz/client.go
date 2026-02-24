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

// SignozQuerier defines the interface for querying a Signoz instance.
type SignozQuerier interface {
	Health(ctx context.Context) (bool, time.Duration, error)
	ListServices(ctx context.Context) ([]types.Service, error)
	QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
	QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error)
	QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error)
}

// Compile-time check that Client implements SignozQuerier.
var _ SignozQuerier = (*Client)(nil)

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
	now := time.Now()
	start := now.Add(-6 * time.Hour)

	// Signoz v1/services requires a POST with start/end timestamps (epoch nanoseconds as strings).
	reqBody := map[string]interface{}{
		"start": fmt.Sprintf("%d", start.UnixNano()),
		"end":   fmt.Sprintf("%d", now.UnixNano()),
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	resp, err := c.do(ctx, http.MethodPost, "/api/v1/services", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("listing services: status %d: %s", resp.StatusCode, string(respBody))
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

// ──────────────────────────────────────────────
// V3 Query Payload Types
// ──────────────────────────────────────────────

// FilterKey identifies a filter field.
type FilterKey struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
}

// FilterItem is one filter condition.
type FilterItem struct {
	Key   FilterKey   `json:"key"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}

// Filters groups filter items with a logical operator.
type Filters struct {
	Op    string       `json:"op"`
	Items []FilterItem `json:"items"`
}

// OrderByItem specifies a sort column and direction.
type OrderByItem struct {
	ColumnName string `json:"columnName"`
	Order      string `json:"order"`
}

// AggregateAttribute identifies the attribute to aggregate on.
type AggregateAttribute struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
}

// SelectColumn identifies a column to include in list results.
type SelectColumn struct {
	Key      string `json:"key"`
	DataType string `json:"dataType"`
	Type     string `json:"type"`
	IsColumn bool   `json:"isColumn"`
	IsJSON   bool   `json:"isJSON,omitempty"`
}

// BuilderQuery is a single query within the composite query.
type BuilderQuery struct {
	QueryName          string              `json:"queryName"`
	StepInterval       int                 `json:"stepInterval"`
	DataSource         string              `json:"dataSource"`
	AggregateOperator  string              `json:"aggregateOperator"`
	AggregateAttribute *AggregateAttribute `json:"aggregateAttribute,omitempty"`
	Filters            Filters             `json:"filters"`
	Expression         string              `json:"expression"`
	Disabled           bool                `json:"disabled"`
	Limit              int                 `json:"limit,omitempty"`
	Offset             int                 `json:"offset"`
	OrderBy            []OrderByItem       `json:"orderBy,omitempty"`
	GroupBy            []interface{}        `json:"groupBy"`
	SelectColumns      []SelectColumn      `json:"selectColumns,omitempty"`
}

// CompositeQuery wraps the builder queries with panel and query type.
type CompositeQuery struct {
	BuilderQueries map[string]*BuilderQuery `json:"builderQueries"`
	PanelType      string                   `json:"panelType"`
	QueryType      string                   `json:"queryType"`
}

// QueryRangePayload is the top-level request body for query_range.
type QueryRangePayload struct {
	Start          int64          `json:"start"`
	End            int64          `json:"end"`
	Step           int            `json:"step"`
	CompositeQuery CompositeQuery `json:"compositeQuery"`
}

// QueryRangeParams captures the inputs for building a query payload.
type QueryRangeParams struct {
	DataSource         string
	PanelType          string // "list" or "graph"
	AggregateOperator  string // "noop", "avg", "sum", etc.
	AggregateAttribute *AggregateAttribute
	Filters            []FilterItem
	OrderBy            []OrderByItem
	SelectColumns      []SelectColumn
	Limit              int
	DurationMinutes    int
}

// BuildQueryRangePayload constructs a v3-compatible query_range request.
func BuildQueryRangePayload(params QueryRangeParams) QueryRangePayload {
	now := time.Now()
	start := now.Add(-time.Duration(params.DurationMinutes) * time.Minute)

	step := 60
	if params.PanelType == "graph" && params.DurationMinutes > 0 {
		step = params.DurationMinutes * 60 / 60 // ~60 data points
		if step < 60 {
			step = 60
		}
	}

	bq := &BuilderQuery{
		QueryName:         "A",
		StepInterval:      60,
		DataSource:        params.DataSource,
		AggregateOperator: params.AggregateOperator,
		Filters: Filters{
			Op:    "AND",
			Items: params.Filters,
		},
		Expression: "A",
		Disabled:   false,
		GroupBy:    []interface{}{},
	}

	if params.AggregateAttribute != nil {
		bq.AggregateAttribute = params.AggregateAttribute
	}
	if params.Limit > 0 {
		bq.Limit = params.Limit
	}
	if len(params.OrderBy) > 0 {
		bq.OrderBy = params.OrderBy
	}
	// Ensure filters items is never nil in JSON
	if bq.Filters.Items == nil {
		bq.Filters.Items = []FilterItem{}
	}

	// Set selectColumns for list queries (required by Signoz v3)
	if params.PanelType == "list" {
		if len(params.SelectColumns) > 0 {
			bq.SelectColumns = params.SelectColumns
		} else {
			bq.SelectColumns = defaultSelectColumns(params.DataSource)
		}
	}

	return QueryRangePayload{
		Start:     start.UnixMilli(),
		End:       now.UnixMilli(),
		Step:      step,
		CompositeQuery: CompositeQuery{
			BuilderQueries: map[string]*BuilderQuery{"A": bq},
			PanelType:      params.PanelType,
			QueryType:      "builder",
		},
	}
}

// defaultSelectColumns returns the standard columns for list queries per data source.
func defaultSelectColumns(dataSource string) []SelectColumn {
	switch dataSource {
	case "logs":
		return []SelectColumn{
			{Key: "body", DataType: "string", Type: "", IsColumn: true},
			{Key: "severity_text", DataType: "string", Type: "", IsColumn: true},
			{Key: "service_name", DataType: "string", Type: "resource", IsColumn: false},
		}
	case "traces":
		return []SelectColumn{
			{Key: "serviceName", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "name", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "durationNano", DataType: "float64", Type: "tag", IsColumn: true},
			{Key: "httpMethod", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "responseStatusCode", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "traceID", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "spanID", DataType: "string", Type: "tag", IsColumn: true},
			{Key: "statusCode", DataType: "int64", Type: "tag", IsColumn: true},
		}
	default:
		return nil
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

func (c *Client) postQueryRange(ctx context.Context, payload QueryRangePayload) ([]byte, error) {
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

	var filters []FilterItem
	if service != "" {
		filters = append(filters, FilterItem{
			Key:   FilterKey{Key: "service_name", DataType: "string", Type: "resource", IsColumn: false},
			Op:    "=",
			Value: service,
		})
	}
	if severityFilter != "" {
		filters = append(filters, FilterItem{
			Key:   FilterKey{Key: "severity_text", DataType: "string", Type: "tag", IsColumn: false},
			Op:    "=",
			Value: severityFilter,
		})
	}

	payload := BuildQueryRangePayload(QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters:           filters,
		OrderBy:           []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:             limit,
		DurationMinutes:   durationMinutes,
	})

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
	var aggAttr *AggregateAttribute
	if metricName != "" {
		aggAttr = &AggregateAttribute{
			Key:      metricName,
			DataType: "float64",
			Type:     "Gauge",
			IsColumn: true,
		}
	}

	payload := BuildQueryRangePayload(QueryRangeParams{
		DataSource:         "metrics",
		PanelType:          "graph",
		AggregateOperator:  "avg",
		AggregateAttribute: aggAttr,
		DurationMinutes:    durationMinutes,
	})

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

	var filters []FilterItem
	if service != "" {
		filters = append(filters, FilterItem{
			Key:   FilterKey{Key: "serviceName", DataType: "string", Type: "tag", IsColumn: true},
			Op:    "=",
			Value: service,
		})
	}

	payload := BuildQueryRangePayload(QueryRangeParams{
		DataSource:        "traces",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters:           filters,
		OrderBy:           []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:             limit,
		DurationMinutes:   durationMinutes,
	})

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

// extractResultArray unwraps the Signoz response envelope to get the result array.
// Signoz v3 returns: {"status":"success","data":{"result":[...]}}
// This function handles both {data: {result: [...]}} and {data: [...]} shapes.
func extractResultArray(data []byte) ([]byte, error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	result := resp["data"]
	if result == nil {
		result = resp["result"]
	}
	if result == nil {
		return nil, nil
	}

	// If data is an object with a "result" key, unwrap it
	if m, ok := result.(map[string]interface{}); ok {
		if inner, ok := m["result"]; ok {
			result = inner
		}
	}

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return resultBytes, nil
}

// parseLogsResponse extracts log entries from the query_range response.
func parseLogsResponse(data []byte) ([]types.LogEntry, error) {
	resultBytes, err := extractResultArray(data)
	if err != nil {
		return nil, fmt.Errorf("parsing logs response: %w", err)
	}
	if resultBytes == nil {
		return nil, nil
	}
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

	// Handle nested "data" field (Signoz wraps list items as {timestamp, data: {...}})
	// Preserve outer-level timestamp before unwrapping.
	if data, ok := m["data"].(map[string]interface{}); ok {
		if ts, hasTS := m["timestamp"]; hasTS {
			if _, innerHasTS := data["timestamp"]; !innerHasTS {
				data["timestamp"] = ts
			}
		}
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
	resultBytes, err := extractResultArray(data)
	if err != nil {
		return nil, fmt.Errorf("parsing traces response: %w", err)
	}
	if resultBytes == nil {
		return nil, nil
	}

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
		if ts, hasTS := m["timestamp"]; hasTS {
			if _, innerHasTS := data["timestamp"]; !innerHasTS {
				data["timestamp"] = ts
			}
		}
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
	resultBytes, err := extractResultArray(data)
	if err != nil {
		return nil, fmt.Errorf("parsing metrics response: %w", err)
	}
	if resultBytes == nil {
		return nil, nil
	}

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
							Timestamp: time.UnixMilli(int64(ts)),
							Value:     val,
							Labels:    s.Labels,
						})
					}
				}
			}
		}
		return metrics, nil
	}

	return nil, nil
}

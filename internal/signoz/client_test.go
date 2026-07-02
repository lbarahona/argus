package signoz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Payload Builder Tests (TDD for v3 format)
// ──────────────────────────────────────────────

func TestBuildLogsListPayload(t *testing.T) {
	params := QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters: []FilterItem{
			{Key: FilterKey{Key: "service_name", DataType: "string", Type: "resource", IsColumn: false}, Op: "=", Value: "api-server"},
			{Key: FilterKey{Key: "severity_text", DataType: "string", Type: "tag", IsColumn: false}, Op: "=", Value: "ERROR"},
		},
		OrderBy:         []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:           100,
		DurationMinutes: 60,
	}

	payload := BuildQueryRangePayload(params)

	// Check timestamps
	if payload.End <= payload.Start {
		t.Error("end should be after start")
	}
	diff := payload.End - payload.Start
	expectedDiff := int64(60 * 60 * 1000) // 60 minutes in ms
	if diff < expectedDiff-1000 || diff > expectedDiff+1000 {
		t.Errorf("duration should be ~60min in ms, got %d", diff)
	}

	// Check composite query structure
	cq := payload.CompositeQuery
	if cq.PanelType != "list" {
		t.Errorf("expected panelType=list, got %s", cq.PanelType)
	}
	if cq.QueryType != "builder" {
		t.Errorf("expected queryType=builder, got %s", cq.QueryType)
	}

	bq, ok := cq.BuilderQueries["A"]
	if !ok {
		t.Fatal("expected builderQueries to have key 'A'")
	}

	if bq.DataSource != "logs" {
		t.Errorf("expected dataSource=logs, got %s", bq.DataSource)
	}
	if bq.AggregateOperator != "noop" {
		t.Errorf("expected aggregateOperator=noop, got %s", bq.AggregateOperator)
	}
	if bq.Expression != "A" {
		t.Errorf("expected expression=A, got %s", bq.Expression)
	}
	if bq.Limit != 100 {
		t.Errorf("expected limit=100, got %d", bq.Limit)
	}

	// Check filters
	if bq.Filters.Op != "AND" {
		t.Errorf("expected filters op=AND, got %s", bq.Filters.Op)
	}
	if len(bq.Filters.Items) != 2 {
		t.Fatalf("expected 2 filter items, got %d", len(bq.Filters.Items))
	}
	f0 := bq.Filters.Items[0]
	if f0.Key.Key != "service_name" || f0.Key.Type != "resource" || f0.Op != "=" {
		t.Errorf("unexpected first filter: %+v", f0)
	}
	f1 := bq.Filters.Items[1]
	if f1.Key.Key != "severity_text" || f1.Key.Type != "tag" || f1.Op != "=" {
		t.Errorf("unexpected second filter: %+v", f1)
	}

	// Check orderBy
	if len(bq.OrderBy) != 1 || bq.OrderBy[0].ColumnName != "timestamp" || bq.OrderBy[0].Order != "desc" {
		t.Errorf("unexpected orderBy: %+v", bq.OrderBy)
	}
}

func TestBuildTracesListPayload(t *testing.T) {
	params := QueryRangeParams{
		DataSource:        "traces",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters: []FilterItem{
			{Key: FilterKey{Key: "serviceName", DataType: "string", Type: "tag", IsColumn: true}, Op: "=", Value: "frontend"},
		},
		OrderBy:         []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:           50,
		DurationMinutes: 30,
	}

	payload := BuildQueryRangePayload(params)
	bq := payload.CompositeQuery.BuilderQueries["A"]

	if bq.DataSource != "traces" {
		t.Errorf("expected dataSource=traces, got %s", bq.DataSource)
	}
	if bq.Limit != 50 {
		t.Errorf("expected limit=50, got %d", bq.Limit)
	}

	f := bq.Filters.Items[0]
	if f.Key.Key != "serviceName" || !f.Key.IsColumn || f.Key.Type != "tag" {
		t.Errorf("traces filter should use serviceName tag isColumn=true: %+v", f)
	}
}

func TestBuildMetricsGraphPayload(t *testing.T) {
	params := QueryRangeParams{
		DataSource:        "metrics",
		PanelType:         "graph",
		AggregateOperator: "avg",
		AggregateAttribute: &AggregateAttribute{
			Key:      "cpu_usage",
			DataType: "float64",
			Type:     "Gauge",
			IsColumn: true,
		},
		DurationMinutes: 60,
	}

	payload := BuildQueryRangePayload(params)

	cq := payload.CompositeQuery
	if cq.PanelType != "graph" {
		t.Errorf("expected panelType=graph, got %s", cq.PanelType)
	}

	bq := cq.BuilderQueries["A"]
	if bq.AggregateOperator != "avg" {
		t.Errorf("expected aggregateOperator=avg, got %s", bq.AggregateOperator)
	}
	if bq.AggregateAttribute == nil {
		t.Fatal("expected aggregateAttribute to be set")
	}
	if bq.AggregateAttribute.Key != "cpu_usage" {
		t.Errorf("expected aggregateAttribute.Key=cpu_usage, got %s", bq.AggregateAttribute.Key)
	}
	if bq.Limit != 0 {
		t.Errorf("expected no limit for metrics, got %d", bq.Limit)
	}
}

func TestBuildPayloadNoFilters(t *testing.T) {
	params := QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		DurationMinutes:   5,
	}

	payload := BuildQueryRangePayload(params)
	bq := payload.CompositeQuery.BuilderQueries["A"]

	if bq.Filters.Op != "AND" {
		t.Errorf("empty filters should still have op=AND, got %s", bq.Filters.Op)
	}
	if len(bq.Filters.Items) != 0 {
		t.Errorf("expected 0 filter items, got %d", len(bq.Filters.Items))
	}
}

func TestBuildPayloadTimestamps(t *testing.T) {
	before := time.Now()
	params := QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		DurationMinutes:   30,
	}
	payload := BuildQueryRangePayload(params)
	after := time.Now()

	// end should be approximately now (within 2 seconds, accounting for UnixMilli truncation)
	endTime := time.UnixMilli(payload.End)
	if endTime.Before(before.Add(-2*time.Second)) || endTime.After(after.Add(2*time.Second)) {
		t.Errorf("end timestamp %v not close to now (%v - %v)", endTime, before, after)
	}

	// start should be ~30 minutes before end
	startTime := time.UnixMilli(payload.Start)
	expectedStart := endTime.Add(-30 * time.Minute)
	if startTime.Before(expectedStart.Add(-time.Second)) || startTime.After(expectedStart.Add(time.Second)) {
		t.Errorf("start %v not ~30min before end %v", startTime, endTime)
	}
}

func TestBuildPayloadJSONRoundTrip(t *testing.T) {
	params := QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters: []FilterItem{
			{Key: FilterKey{Key: "service_name", DataType: "string", Type: "resource", IsColumn: false}, Op: "=", Value: "api"},
		},
		OrderBy:         []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:           10,
		DurationMinutes: 5,
	}

	payload := BuildQueryRangePayload(params)
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Must have builderQueries, not queries
	cq := raw["compositeQuery"].(map[string]interface{})
	if _, ok := cq["builderQueries"]; !ok {
		t.Error("JSON should have compositeQuery.builderQueries")
	}
	if _, ok := cq["queries"]; ok {
		t.Error("JSON should NOT have compositeQuery.queries (v5 format)")
	}

	if _, ok := cq["panelType"]; !ok {
		t.Error("JSON should have compositeQuery.panelType")
	}
	if _, ok := cq["queryType"]; !ok {
		t.Error("JSON should have compositeQuery.queryType")
	}

	// Should NOT have top-level requestType
	if _, ok := raw["requestType"]; ok {
		t.Error("JSON should NOT have top-level requestType (v5 format)")
	}

	// Should have top-level step
	if _, ok := raw["step"]; !ok {
		t.Error("JSON should have top-level step")
	}
}

// ──────────────────────────────────────────────
// HTTP Client Tests
// ──────────────────────────────────────────────

func TestHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("SIGNOZ-API-KEY") != "test-key" {
			t.Errorf("missing or wrong API key header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "test-key"})
	healthy, latency, err := client.Health(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy=true")
	}
	if latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestHealthUnhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "test-key"})
	healthy, _, err := client.Health(context.Background())

	if healthy {
		t.Error("expected healthy=false")
	}
	if err == nil {
		t.Error("expected error")
	}
}

func TestListServices(t *testing.T) {
	services := []types.Service{
		{Name: "frontend", NumCalls: 1000, NumErrors: 50},
		{Name: "backend", NumCalls: 500, NumErrors: 0},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		// Verify request body has start/end
		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody["start"] == nil || reqBody["end"] == nil {
			t.Error("expected start and end in request body")
		}
		json.NewEncoder(w).Encode(services)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key"})
	result, err := client.ListServices(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 services, got %d", len(result))
	}
	if result[0].ErrorRate != 5.0 {
		t.Errorf("expected error rate 5.0, got %.1f", result[0].ErrorRate)
	}
	if result[1].ErrorRate != 0.0 {
		t.Errorf("expected error rate 0.0, got %.1f", result[1].ErrorRate)
	}
}

func TestQueryLogs(t *testing.T) {
	// Real Signoz v3 response envelope: {status, data: {result: [...]}}
	response := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"queryName": "A",
					"list": []map[string]interface{}{
						{
							"timestamp": "2026-02-16T10:00:00Z",
							"data": map[string]interface{}{
								"body":          "Connection refused",
								"severity_text": "ERROR",
								"service_name":  "api-server",
							},
						},
						{
							"timestamp": "2026-02-16T10:01:00Z",
							"data": map[string]interface{}{
								"body":          "Request completed",
								"severity_text": "INFO",
								"service_name":  "api-server",
							},
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v3/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Verify v3 request body structure
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)

		// Should NOT have requestType
		if _, ok := payload["requestType"]; ok {
			t.Error("v3 payload should not have requestType")
		}

		// Should have compositeQuery with builderQueries
		cq, ok := payload["compositeQuery"].(map[string]interface{})
		if !ok {
			t.Fatal("missing compositeQuery")
		}
		if _, ok := cq["builderQueries"]; !ok {
			t.Error("should have builderQueries, not queries")
		}
		if cq["panelType"] != "list" {
			t.Errorf("expected panelType=list, got %v", cq["panelType"])
		}

		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key"})
	result, err := client.QueryLogs(context.Background(), "api-server", 60, 100, "ERROR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(result.Logs))
	}
	if result.Logs[0].Body != "Connection refused" {
		t.Errorf("unexpected body: %s", result.Logs[0].Body)
	}
	if result.Logs[0].SeverityText != "ERROR" {
		t.Errorf("unexpected severity: %s", result.Logs[0].SeverityText)
	}
}

func TestQueryLogsAPIv5(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v5/query_range" {
			t.Errorf("expected v5 path, got: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key", APIVersion: "v5"})
	_, err := client.QueryLogs(context.Background(), "", 60, 10, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQueryTraces(t *testing.T) {
	response := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"queryName": "A",
					"list": []map[string]interface{}{
						{
							"timestamp": "2026-02-16T10:00:00Z",
							"data": map[string]interface{}{
								"traceID":      "abc123def456",
								"spanID":       "span001",
								"serviceName":  "frontend",
								"name":         "GET /api/users",
								"durationNano": float64(15000000),
								"statusCode":   "OK",
							},
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify v3 structure
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)

		cq := payload["compositeQuery"].(map[string]interface{})
		if _, ok := cq["builderQueries"]; !ok {
			t.Error("traces should use builderQueries")
		}
		if cq["panelType"] != "list" {
			t.Errorf("expected panelType=list, got %v", cq["panelType"])
		}

		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key"})
	result, err := client.QueryTraces(context.Background(), "frontend", 60, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(result.Traces))
	}
	if result.Traces[0].TraceID != "abc123def456" {
		t.Errorf("unexpected trace ID: %s", result.Traces[0].TraceID)
	}
	if result.Traces[0].DurationMs() != 15.0 {
		t.Errorf("expected 15ms, got %.1f", result.Traces[0].DurationMs())
	}
}

func TestQueryMetrics(t *testing.T) {
	response := map[string]interface{}{
		"status": "success",
		"data": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"queryName": "A",
					"series": []map[string]interface{}{
						{
							"labels": map[string]string{"host": "web-1"},
							"values": [][]interface{}{
								{float64(1708070400000), float64(42.5)},
								{float64(1708070460000), float64(43.1)},
							},
						},
					},
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify v3 structure
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)

		cq := payload["compositeQuery"].(map[string]interface{})
		if cq["panelType"] != "graph" {
			t.Errorf("expected panelType=graph for metrics, got %v", cq["panelType"])
		}

		bqs := cq["builderQueries"].(map[string]interface{})
		bqA := bqs["A"].(map[string]interface{})
		if bqA["aggregateOperator"] != "avg" {
			t.Errorf("expected aggregateOperator=avg, got %v", bqA["aggregateOperator"])
		}

		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key"})
	result, err := client.QueryMetrics(context.Background(), "cpu_usage", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(result.Metrics))
	}
	if result.Metrics[0].Value != 42.5 {
		t.Errorf("expected 42.5, got %f", result.Metrics[0].Value)
	}
}

func TestQueryMetricsRealV3ObjectValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","series":[{"labels":{"host":"web-1"},"values":[{"timestamp":1708070400000,"value":"42.5"},{"timestamp":1708070460000,"value":"43.1"}]}]}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryMetrics(context.Background(), "cpu_usage", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Metrics) != 2 {
		t.Fatalf("expected 2 metric points from v3 object-shaped values, got %d", len(result.Metrics))
	}
	if result.Metrics[0].Value != 42.5 {
		t.Errorf("point 0 value = %v, want 42.5", result.Metrics[0].Value)
	}
	if result.Metrics[0].Labels["host"] != "web-1" {
		t.Errorf("labels not preserved: %v", result.Metrics[0].Labels)
	}
	if result.Metrics[0].Timestamp.UnixMilli() != 1708070400000 {
		t.Errorf("timestamp = %v, want 1708070400000ms", result.Metrics[0].Timestamp.UnixMilli())
	}
}

func TestQueryLogsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL, APIKey: "key"})
	_, err := client.QueryLogs(context.Background(), "", 60, 10, "")
	if err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestSignozQuerierInterface(t *testing.T) {
	// Compile-time check is already in client.go, but verify at runtime too.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{})
	}))
	defer server.Close()

	var querier SignozQuerier = New(types.Instance{URL: server.URL})
	if querier == nil {
		t.Error("Client should implement SignozQuerier")
	}
}

func TestQueryLogsEmptyResultNoPhantomEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryLogs(context.Background(), "api", 60, 10, "ERROR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Logs) != 0 {
		t.Fatalf("expected 0 logs for empty result, got %d: %+v", len(result.Logs), result.Logs)
	}
}

func TestQueryTracesEmptyResultNoPhantomEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryTraces(context.Background(), "api", 60, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 0 {
		t.Fatalf("expected 0 traces for empty result, got %d: %+v", len(result.Traces), result.Traces)
	}
}

func TestSeverityCasings(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"error", []string{"ERROR", "error", "Error"}},
		{"ERROR", []string{"ERROR", "error", "Error"}},
		{"Error", []string{"ERROR", "error", "Error"}},
		{"warn", []string{"WARN", "warn", "Warn"}},
	}
	for _, tt := range tests {
		got := severityCasings(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("severityCasings(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("severityCasings(%q) = %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

func TestQueryLogsSeverityFilterMatchesAnyCasing(t *testing.T) {
	var payload QueryRangePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	if _, err := client.QueryLogs(context.Background(), "", 60, 10, "error"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := payload.CompositeQuery.BuilderQueries["A"].Filters.Items
	if len(items) != 1 {
		t.Fatalf("expected 1 filter item, got %d", len(items))
	}
	if items[0].Op != "in" {
		t.Errorf("severity filter op = %q, want \"in\"", items[0].Op)
	}
	values, ok := items[0].Value.([]interface{})
	if !ok {
		t.Fatalf("severity filter value is %T, want JSON array", items[0].Value)
	}
	want := map[string]bool{"ERROR": true, "error": true, "Error": true}
	if len(values) != len(want) {
		t.Fatalf("expected %d casing variants, got %v", len(want), values)
	}
	for _, v := range values {
		if s, _ := v.(string); !want[s] {
			t.Errorf("unexpected casing variant %v", v)
		}
	}
}

func TestMapToTraceEntryNumericStatusCode(t *testing.T) {
	entry := mapToTraceEntry(map[string]interface{}{
		"traceID":     "abc",
		"serviceName": "api",
		"statusCode":  float64(2), // numeric, as declared int64 in defaultSelectColumns
	})
	if entry.StatusCode != "STATUS_CODE_ERROR" {
		t.Errorf("numeric statusCode 2 should map to STATUS_CODE_ERROR, got %q", entry.StatusCode)
	}

	entry = mapToTraceEntry(map[string]interface{}{"statusCode": float64(1)})
	if entry.StatusCode != "STATUS_CODE_OK" {
		t.Errorf("numeric statusCode 1 should map to STATUS_CODE_OK, got %q", entry.StatusCode)
	}

	entry = mapToTraceEntry(map[string]interface{}{"statusCode": "STATUS_CODE_ERROR"})
	if entry.StatusCode != "STATUS_CODE_ERROR" {
		t.Errorf("string statusCode must pass through unchanged, got %q", entry.StatusCode)
	}

	entry = mapToTraceEntry(map[string]interface{}{"status_code": float64(2)})
	if entry.StatusCode != "STATUS_CODE_ERROR" {
		t.Errorf("numeric snake_case status_code 2 should map to STATUS_CODE_ERROR, got %q", entry.StatusCode)
	}
}

func TestParseMetricPointNumericObjectValue(t *testing.T) {
	entry, ok := parseMetricPoint(json.RawMessage(`{"timestamp":1708070400000,"value":42.5}`))
	if !ok {
		t.Fatal("expected numeric object-shaped point to parse, got ok=false")
	}
	if entry.Value != 42.5 {
		t.Errorf("value = %v, want 42.5", entry.Value)
	}
	if entry.Timestamp.UnixMilli() != 1708070400000 {
		t.Errorf("timestamp = %v, want 1708070400000ms", entry.Timestamp.UnixMilli())
	}
}

// ──────────────────────────────────────────────
// WindowedQuerier: absolute time-range queries
// ──────────────────────────────────────────────

func TestQueryLogsRangeUsesAbsoluteWindow(t *testing.T) {
	var payload QueryRangePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	start := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 11, 0, 0, 0, time.UTC)

	client := New(types.Instance{URL: server.URL})
	if _, err := client.QueryLogsRange(context.Background(), "api", start, end, 50, "ERROR"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload.Start != start.UnixMilli() || payload.End != end.UnixMilli() {
		t.Errorf("payload window = [%d, %d], want [%d, %d]",
			payload.Start, payload.End, start.UnixMilli(), end.UnixMilli())
	}
}

func TestListServicesRangeSendsAbsoluteWindow(t *testing.T) {
	var gotStart, gotEnd string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotStart, gotEnd = body["start"], body["end"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	start := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 11, 0, 0, 0, time.UTC)

	client := New(types.Instance{URL: server.URL})
	if _, err := client.ListServicesRange(context.Background(), start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotStart != fmt.Sprintf("%d", start.UnixNano()) || gotEnd != fmt.Sprintf("%d", end.UnixNano()) {
		t.Errorf("services window = [%s, %s], want the absolute range in ns", gotStart, gotEnd)
	}
}

func TestClientImplementsWindowedQuerier(t *testing.T) {
	var _ WindowedQuerier = (*Client)(nil)
}

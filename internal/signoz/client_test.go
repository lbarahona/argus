package signoz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
)

func TestBuildQueryPayload(t *testing.T) {
	payload := BuildQueryPayload("logs", "raw", 60, 100, "severity_text = 'ERROR'", "timestamp", nil, nil)

	// Check top-level fields
	if payload["requestType"] != "raw" {
		t.Errorf("expected requestType=raw, got %v", payload["requestType"])
	}

	start, ok := payload["start"].(int64)
	if !ok {
		t.Fatal("start should be int64")
	}
	end, ok := payload["end"].(int64)
	if !ok {
		t.Fatal("end should be int64")
	}
	if end <= start {
		t.Error("end should be after start")
	}

	// Check composite query structure
	cq := payload["compositeQuery"].(map[string]interface{})
	queries := cq["queries"].([]map[string]interface{})
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}

	q := queries[0]
	if q["type"] != "builder_query" {
		t.Errorf("expected type=builder_query, got %v", q["type"])
	}

	spec := q["spec"].(map[string]interface{})
	if spec["signal"] != "logs" {
		t.Errorf("expected signal=logs, got %v", spec["signal"])
	}
	if spec["limit"] != 100 {
		t.Errorf("expected limit=100, got %v", spec["limit"])
	}

	filter := spec["filter"].(map[string]interface{})
	if filter["expression"] != "severity_text = 'ERROR'" {
		t.Errorf("unexpected filter: %v", filter["expression"])
	}
}

func TestBuildQueryPayloadNoFilter(t *testing.T) {
	payload := BuildQueryPayload("traces", "raw", 30, 50, "", "", nil, nil)

	cq := payload["compositeQuery"].(map[string]interface{})
	queries := cq["queries"].([]map[string]interface{})
	spec := queries[0]["spec"].(map[string]interface{})

	if _, ok := spec["filter"]; ok {
		t.Error("expected no filter when empty")
	}
	if _, ok := spec["order"]; ok {
		t.Error("expected no order when empty")
	}
}

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
	response := map[string]interface{}{
		"data": []map[string]interface{}{
			{
				"queryName": "A",
				"list": []map[string]interface{}{
					{
						"timestamp":     "2026-02-16T10:00:00Z",
						"body":          "Connection refused",
						"severity_text": "ERROR",
						"service_name":  "api-server",
					},
					{
						"timestamp":     "2026-02-16T10:01:00Z",
						"body":          "Request completed",
						"severity_text": "INFO",
						"service_name":  "api-server",
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

		// Verify request body
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["requestType"] != "raw" {
			t.Errorf("expected requestType=raw, got %v", payload["requestType"])
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
		"data": []map[string]interface{}{
			{
				"queryName": "A",
				"list": []map[string]interface{}{
					{
						"timestamp":    "2026-02-16T10:00:00Z",
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
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		"data": []map[string]interface{}{
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
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestJoinFilters(t *testing.T) {
	tests := []struct {
		parts    []string
		expected string
	}{
		{[]string{"a = 'b'"}, "a = 'b'"},
		{[]string{"a = 'b'", "c = 'd'"}, "a = 'b' AND c = 'd'"},
	}
	for _, tt := range tests {
		got := joinFilters(tt.parts)
		if got != tt.expected {
			t.Errorf("joinFilters(%v) = %q, want %q", tt.parts, got, tt.expected)
		}
	}
}

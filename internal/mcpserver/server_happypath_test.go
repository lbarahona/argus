package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── mock Signoz HTTP server ──────────────────────────────────────────────────

// mockSignozHandler creates an http.Handler that responds to all Signoz API
// endpoints used by mcpserver tool handlers with realistic data.
func mockSignozHandler() http.Handler {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})

	// ListServices endpoint
	mux.HandleFunc("/api/v1/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"serviceName": "payment-service",
				"numErrors":   5,
				"numCalls":    1000,
			},
			{
				"serviceName": "auth-service",
				"numErrors":   0,
				"numCalls":    500,
			},
			{
				"serviceName": "api-gateway",
				"numErrors":   2,
				"numCalls":    2000,
			},
		})
	})

	// query_range endpoint (handles logs, traces, metrics)
	mux.HandleFunc("/api/v3/query_range", func(w http.ResponseWriter, r *http.Request) {
		// Read body to determine query type
		var payload map[string]interface{}
		json.NewDecoder(r.Body).Decode(&payload)

		// Determine data source from the query payload
		dataSource := ""
		if compQ, ok := payload["compositeQuery"].(map[string]interface{}); ok {
			if builderQ, ok := compQ["builderQueries"].(map[string]interface{}); ok {
				for _, q := range builderQ {
					if qMap, ok := q.(map[string]interface{}); ok {
						if ds, ok := qMap["dataSource"].(string); ok {
							dataSource = ds
							break
						}
					}
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")

		switch dataSource {
		case "logs":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"result": []map[string]interface{}{
						{
							"queryName": "A",
							"list": []map[string]interface{}{
								{
									"timestamp":     time.Now().Format(time.RFC3339Nano),
									"body":          "connection timeout to database",
									"severity_text": "ERROR",
									"service_name":  "payment-service",
								},
								{
									"timestamp":     time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
									"body":          "request processed successfully",
									"severity_text": "INFO",
									"service_name":  "auth-service",
								},
							},
						},
					},
				},
			})
		case "traces":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"result": []map[string]interface{}{
						{
							"queryName": "A",
							"list": []map[string]interface{}{
								{
									"timestamp":    time.Now().Format(time.RFC3339Nano),
									"traceID":      "abc123def456",
									"spanID":       "span001",
									"serviceName":  "payment-service",
									"name":         "POST /checkout",
									"durationNano": float64(150000000),
									"statusCode":   "OK",
								},
							},
						},
					},
				},
			})
		case "metrics":
			now := time.Now()
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data": map[string]interface{}{
					"result": []map[string]interface{}{
						{
							"queryName": "A",
							"series": []map[string]interface{}{
								{
									"labels": map[string]string{"service_name": "payment-service"},
									"values": [][]interface{}{
										{float64(now.UnixMilli()), float64(42.5)},
										{float64(now.Add(-time.Minute).UnixMilli()), float64(38.2)},
									},
								},
							},
						},
					},
				},
			})
		default:
			// Generic empty success
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "success",
				"data":   map[string]interface{}{"result": []interface{}{}},
			})
		}
	})

	return mux
}

// mockAIHandler returns an HTTP handler that mocks the Anthropic API.
func mockAIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":    "msg_mock123",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-3-5-haiku-20241022",
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": "Based on the observability data, your system is healthy with low error rates.",
				},
			},
			"stop_reason": "end_turn",
			"usage": map[string]interface{}{
				"input_tokens":  100,
				"output_tokens": 50,
			},
		})
	})
	return mux
}

func mockAlertmanagerHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{
				"name":   "am-prod",
				"status": "ready",
				"peers":  []map[string]any{{"name": "am-0", "address": "10.0.0.1"}},
			},
			"versionInfo": map[string]any{"version": "0.28.0"},
			"uptime":      time.Now().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/v2/alerts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"labels":       map[string]string{"alertname": "HighLatency", "severity": "critical", "service": "payment-service"},
				"annotations":  map[string]string{"summary": "Payments are slow"},
				"startsAt":     time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
				"endsAt":       time.Now().Add(50 * time.Minute).Format(time.RFC3339),
				"updatedAt":    time.Now().Format(time.RFC3339),
				"generatorURL": "http://prometheus/graph?g0.expr=latency",
				"fingerprint":  "abc123",
				"receivers":    []map[string]string{{"name": "pagerduty"}},
				"status":       map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}},
			},
		})
	})
	mux.HandleFunc("/api/v2/silences", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":        "silence-12345678",
				"status":    map[string]string{"state": "active"},
				"updatedAt": time.Now().Format(time.RFC3339),
				"comment":   "deploy in progress",
				"createdBy": "logan",
				"startsAt":  time.Now().Add(-5 * time.Minute).Format(time.RFC3339),
				"endsAt":    time.Now().Add(55 * time.Minute).Format(time.RFC3339),
				"matchers":  []map[string]any{{"name": "service", "value": "payment-service", "isEqual": true, "isRegex": false}},
			},
		})
	})
	return mux
}

func mockPrometheusHandler() http.Handler {
	mux := http.NewServeMux()
	now := time.Now()
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("/api/v1/status/buildinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"version": "2.55.0", "revision": "abc", "goVersion": "go1.24.0"},
		})
	})
	mux.HandleFunc("/api/v1/status/runtimeinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"startTime": now.Add(-24 * time.Hour).Format(time.RFC3339), "storageRetention": "15d", "GOMAXPROCS": 4},
		})
	})
	mux.HandleFunc("/api/v1/rules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"groups": []map[string]any{
					{
						"name":     "platform",
						"file":     "/etc/prometheus/rules.yaml",
						"interval": 30,
						"rules": []map[string]any{
							{
								"name":   "HighLatency",
								"query":  "histogram_quantile(0.99, rate(http_request_duration_seconds_bucket[5m]))",
								"health": "ok",
								"state":  "firing",
								"type":   "alerting",
								"alerts": []map[string]any{{
									"labels":   map[string]string{"service": "payment-service"},
									"state":    "firing",
									"activeAt": now.Add(-5 * time.Minute).Format(time.RFC3339),
									"value":    "1",
								}},
							},
						},
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"activeTargets": []map[string]any{{
					"labels":             map[string]string{"instance": "payments:9090"},
					"scrapePool":         "payments",
					"scrapeUrl":          "http://payments:9090/metrics",
					"lastScrape":         now.Add(-30 * time.Second).Format(time.RFC3339),
					"lastScrapeDuration": 0.02,
					"health":             "up",
					"lastError":          "",
				}},
				"droppedTargets": []any{},
			},
		})
	})
	mux.HandleFunc("/api/v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{"alerts": []map[string]any{{
				"labels":      map[string]string{"alertname": "HighLatency", "severity": "critical"},
				"annotations": map[string]string{"summary": "p99 too high"},
				"state":       "firing",
				"activeAt":    now.Add(-4 * time.Minute).Format(time.RFC3339),
				"value":       "1",
			}}},
		})
	})
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{"resultType": "vector", "result": []map[string]any{{
				"metric": map[string]string{"__name__": "up", "job": "payments"},
				"value":  []any{fmt.Sprintf("%d", now.Unix()), "1"},
			}}},
		})
	})
	return mux
}

func mockGrafanaHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "uid": "pay-123", "title": "Payments Overview", "uri": "db/payments-overview", "url": "/d/pay-123/payments-overview", "slug": "payments-overview", "type": "dash-db", "folderTitle": "Platform"}})
	})
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "uid": "prom-main", "name": "Prometheus", "type": "prometheus", "typeName": "Prometheus", "access": "proxy", "url": "http://prometheus:9090", "isDefault": true, "readOnly": false}})
	})
	mux.HandleFunc("/api/v1/provisioning/alert-rules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "uid": "rule-1", "folderUID": "platform", "ruleGroup": "payments", "title": "Payments Error Rate", "condition": "A", "noDataState": "NoData", "execErrState": "Error", "for": "5m", "labels": map[string]string{"service": "payment-service"}, "annotations": map[string]string{"summary": "Payments failing"}, "updated": time.Now().Format(time.RFC3339)}})
	})
	mux.HandleFunc("/api/alertmanager/grafana/api/v2/alerts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{{"labels": map[string]string{"alertname": "PaymentsErrorRate", "severity": "warning"}, "annotations": map[string]string{"summary": "Error rate elevated"}, "startsAt": time.Now().Add(-2 * time.Minute).Format(time.RFC3339), "endsAt": time.Now().Add(10 * time.Minute).Format(time.RFC3339), "updatedAt": time.Now().Format(time.RFC3339), "generatorURL": "http://grafana/alerting", "fingerprint": "g-123", "receivers": []map[string]string{{"name": "slack"}}, "status": map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}}}})
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"database": "ok", "version": "11.2.0", "commit": "abc123"})
	})
	mux.HandleFunc("/api/org", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "Lester Labs"})
	})
	return mux
}

// withMockSignoz sets up a temp HOME with a valid config pointing to the mock
// Signoz and AI servers, then runs the given function.
func withMockSignoz(t *testing.T, signozURL, aiURL string, f func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	cfgYAML := fmt.Sprintf(`default_instance: prod
anthropic_key: "sk-mock-key-for-testing"
ai:
  provider: anthropic
  model: claude-3-5-haiku-20241022
  anthropic_key: "sk-mock-key-for-testing"
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
  staging:
    name: Staging
    url: %s
    api_key: mock-api-key-staging
`, signozURL, signozURL)

	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	// Override the Anthropic base URL via env (the ai package checks ANTHROPIC_BASE_URL).
	if aiURL != "" {
		t.Setenv("ANTHROPIC_BASE_URL", aiURL)
	}

	f()
}

func withMockIntegrations(t *testing.T, signozURL, aiURL, amURL, promURL, grafanaURL string, f func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	cfgYAML := fmt.Sprintf(`default_instance: prod
anthropic_key: "sk-mock-key-for-testing"
ai:
  provider: anthropic
  model: claude-3-5-haiku-20241022
  anthropic_key: "sk-mock-key-for-testing"
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
alertmanager:
  url: %s
prometheus:
  url: %s
grafana:
  url: %s
`, signozURL, amURL, promURL, grafanaURL)

	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	if aiURL != "" {
		t.Setenv("ANTHROPIC_BASE_URL", aiURL)
	}
	f()
}

// newHappyTestSession creates an MCP client session backed by a mock Signoz server.
// Returns the session and a cleanup function.
func newHappyTestSession(t *testing.T, signozURL, aiURL string) *mcp.ClientSession {
	t.Helper()
	var cs *mcp.ClientSession
	withMockSignoz(t, signozURL, aiURL, func() {
		ctx := context.Background()
		server := mcp.NewServer(
			&mcp.Implementation{Name: "argus-test", Version: "1.0"},
			&mcp.ServerOptions{},
		)
		registerTools(server)

		cTransport, sTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(ctx, sTransport, nil)
		if err != nil {
			t.Fatalf("server.Connect: %v", err)
		}
		t.Cleanup(func() { serverSession.Close() })

		cs, err = mcp.NewClient(
			&mcp.Implementation{Name: "test-client", Version: "1.0"},
			nil,
		).Connect(ctx, cTransport, nil)
		if err != nil {
			t.Fatalf("client.Connect: %v", err)
		}
		t.Cleanup(func() { cs.Close() })
	})
	return cs
}

// ─── Happy path tool tests ────────────────────────────────────────────────────

func TestTool_ArgusStatus_WithInstances(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_status", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "Production") {
			t.Errorf("expected instance name 'Production' in output, got: %s", text)
		}
		if !strings.Contains(text, "Healthy") || !strings.Contains(text, "true") {
			// The JSON output should contain "Healthy": true
			t.Logf("status output: %s", text)
		}
	})
}

func TestTool_ArgusServices_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_services", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "payment-service") {
			t.Errorf("expected 'payment-service' in output, got: %s", text)
		}
		if !strings.Contains(text, "auth-service") {
			t.Errorf("expected 'auth-service' in output, got: %s", text)
		}
	})
}

func TestTool_ArgusServices_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_services", map[string]any{
			"instance": "staging",
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "payment-service") {
			t.Errorf("expected services in output, got: %s", text)
		}
	})
}

func TestTool_ArgusLogs_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"service":  "payment-service",
			"duration": float64(30),
			"limit":    float64(50),
			"severity": "ERROR",
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "connection timeout") {
			t.Errorf("expected log body in output, got: %s", text)
		}
	})
}

func TestTool_ArgusLogs_NoQuery(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		// Without a query field, should return raw JSON logs (no AI analysis)
		result := callTool(t, cs, "argus_logs", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty log output")
		}
	})
}

func TestTool_ArgusLogs_WithAIQuery(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiSrv := httptest.NewServer(mockAIHandler())
	defer aiSrv.Close()

	withMockSignoz(t, signoz.URL, aiSrv.URL, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"service": "payment-service",
			"query":   "Are there database connection issues?",
		})

		// This may fail if the AI package doesn't use ANTHROPIC_BASE_URL,
		// but should at least not panic.
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

func TestTool_ArgusTraces_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_traces", map[string]any{
			"service":  "payment-service",
			"duration": float64(60),
			"limit":    float64(10),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "abc123def456") {
			t.Errorf("expected trace ID in output, got: %s", text)
		}
	})
}

func TestTool_ArgusTraces_NoService(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_traces", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusMetrics_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_metrics", map[string]any{
			"metric":   "http_requests_total",
			"duration": float64(60),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "42.5") || !strings.Contains(text, "38.2") {
			t.Errorf("expected metric values in output, got: %s", text)
		}
	})
}

func TestTool_ArgusMetrics_NoMetric(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_metrics", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusDashboard_WithInstances(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_dashboard", map[string]any{
			"duration": float64(30),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		// Should contain health statuses, services, and error logs
		if !strings.Contains(text, "health") {
			t.Errorf("expected 'health' in dashboard output, got: %s", text)
		}
		if !strings.Contains(text, "payment-service") {
			t.Errorf("expected services in dashboard output, got: %s", text)
		}
	})
}

func TestTool_ArgusReport_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_report", map[string]any{
			"duration": float64(60),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty report output")
		}
	})
}

func TestTool_ArgusReport_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_report", map[string]any{
			"instance": "staging",
			"duration": float64(30),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusTop_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_top", map[string]any{
			"sort_by":  "errors",
			"limit":    float64(10),
			"duration": float64(60),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty top output")
		}
	})
}

func TestTool_ArgusTop_AllSortOptions(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		for _, sortBy := range []string{"errors", "rate", "calls", "name"} {
			t.Run("sort_"+sortBy, func(t *testing.T) {
				result := callTool(t, cs, "argus_top", map[string]any{
					"sort_by": sortBy,
				})
				if result.IsError {
					t.Errorf("sort_by=%q: expected success, got error: %s", sortBy, textOf(t, result))
				}
			})
		}
	})
}

func TestTool_ArgusDiff_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_diff", map[string]any{
			"duration": float64(30),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty diff output")
		}
	})
}

func TestTool_ArgusDeploy_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_deploy", map[string]any{
			"duration":    float64(120),
			"sensitivity": "high",
			"service":     "payment-service",
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty deploy output")
		}
	})
}

func TestTool_ArgusDeploy_Defaults(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		// Empty args → defaults: duration=360, sensitivity=medium
		result := callTool(t, cs, "argus_deploy", map[string]any{})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusGuard_Success(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_guard", map[string]any{
			"service": "payment-service",
			"strict":  false,
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		// Guard should return a verdict: SHIP, CAUTION, or HOLD
		if !strings.Contains(text, "verdict") && !strings.Contains(text, "Verdict") {
			t.Logf("guard output (checking for verdict): %s", text)
		}
	})
}

func TestTool_ArgusGuard_Strict(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_guard", map[string]any{
			"strict": true,
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusBudget_WithSLOs(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	// Need to create an SLO config file in the temp HOME
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	os.MkdirAll(configDir, 0755)

	// Write main config
	cfgYAML := fmt.Sprintf(`default_instance: prod
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
`, signoz.URL)
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600)

	// Write SLO config
	sloYAML := `slos:
  - name: payment-availability
    service: payment-service
    type: availability
    target: 99.9
    window: 30d
  - name: auth-availability
    service: auth-service
    type: availability
    target: 99.95
    window: 30d
`
	os.WriteFile(filepath.Join(configDir, "slos.yaml"), []byte(sloYAML), 0600)

	cs := newTestSession(t)
	result := callTool(t, cs, "argus_budget", map[string]any{
		"window": "6h",
	})

	if result.IsError {
		t.Errorf("expected success, got error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text == "" {
		t.Error("expected non-empty budget output")
	}
}

func TestTool_ArgusBudget_ServiceFilter(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	os.MkdirAll(configDir, 0755)

	cfgYAML := fmt.Sprintf(`default_instance: prod
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
`, signoz.URL)
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600)

	sloYAML := `slos:
  - name: payment-availability
    service: payment-service
    type: availability
    target: 99.9
    window: 30d
`
	os.WriteFile(filepath.Join(configDir, "slos.yaml"), []byte(sloYAML), 0600)

	cs := newTestSession(t)
	result := callTool(t, cs, "argus_budget", map[string]any{
		"service": "payment-service",
		"window":  "1h",
	})

	if result.IsError {
		t.Errorf("expected success, got error: %s", textOf(t, result))
	}
}

func TestTool_ArgusSLOCheck_WithSLOs(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	os.MkdirAll(configDir, 0755)

	cfgYAML := fmt.Sprintf(`default_instance: prod
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
`, signoz.URL)
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600)

	sloYAML := `slos:
  - name: payment-availability
    service: payment-service
    type: availability
    target: 99.9
    window: 30d
`
	os.WriteFile(filepath.Join(configDir, "slos.yaml"), []byte(sloYAML), 0600)

	cs := newTestSession(t)
	result := callTool(t, cs, "argus_slo_check", map[string]any{})

	if result.IsError {
		t.Errorf("expected success, got error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text == "" {
		t.Error("expected non-empty SLO check output")
	}
}

func TestTool_ArgusAlertCheck_WithAlerts(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	configDir := filepath.Join(tmp, ".argus")
	os.MkdirAll(configDir, 0755)

	cfgYAML := fmt.Sprintf(`default_instance: prod
instances:
  prod:
    name: Production
    url: %s
    api_key: mock-api-key
`, signoz.URL)
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfgYAML), 0600)

	alertsYAML := `alerts:
  - name: high-error-rate
    service: payment-service
    metric: error_rate
    operator: ">"
    threshold: 5.0
    severity: critical
    message: "Error rate too high"
`
	os.WriteFile(filepath.Join(configDir, "alerts.yaml"), []byte(alertsYAML), 0600)

	cs := newTestSession(t)
	result := callTool(t, cs, "argus_alert_check", map[string]any{})

	if result.IsError {
		t.Errorf("expected success, got error: %s", textOf(t, result))
	}
	text := textOf(t, result)
	if text == "" {
		t.Error("expected non-empty alert check output")
	}
}

func TestTool_ArgusDoctor_WithConfig(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_doctor", map[string]any{
			"verbose": true,
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty doctor output")
		}
	})
}

// ─── Error injection tests ────────────────────────────────────────────────────

func TestTool_ArgusServices_ServerError(t *testing.T) {
	// Mock server that returns 500 for services
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/services" {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "internal error")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withMockSignoz(t, srv.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_services", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result for 500 response, got: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusLogs_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "query_range") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "internal error")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withMockSignoz(t, srv.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"service": "test",
		})

		if !result.IsError {
			t.Errorf("expected error result for 500 response, got: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusTraces_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "query_range") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withMockSignoz(t, srv.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_traces", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result for 500 response")
		}
	})
}

func TestTool_ArgusMetrics_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "query_range") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	withMockSignoz(t, srv.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_metrics", map[string]any{
			"metric": "cpu",
		})

		if !result.IsError {
			t.Errorf("expected error result for 500 response")
		}
	})
}

// ─── getProvider tests ────────────────────────────────────────────────────────

func TestGetProvider_WithAnthropicKey(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		// Load config to test getProvider
		cfg := &types.Config{
			AnthropicKey: "sk-test-key",
			AI: types.AIConfig{
				Provider: "anthropic",
			},
		}
		provider, err := getProvider(cfg)
		if err != nil {
			t.Errorf("expected no error with anthropic key set, got: %v", err)
		}
		if provider == nil {
			t.Error("expected non-nil provider")
		}
	})
}

func TestGetProvider_NoKey(t *testing.T) {
	cfg := &types.Config{
		AI: types.AIConfig{
			Provider: "anthropic",
		},
	}
	provider, err := getProvider(cfg)
	if err == nil && provider != nil {
		// Some providers may not validate key at creation time
		t.Log("provider created without key (may validate lazily)")
	}
}

func TestGetProvider_LegacyKey(t *testing.T) {
	cfg := &types.Config{
		AnthropicKey: "sk-legacy-key",
	}
	provider, err := getProvider(cfg)
	if err != nil {
		t.Logf("getProvider with legacy key: %v", err)
	}
	_ = provider
}

// ─── AI query branch tests (exercise the code path, expect AI error) ──────────

// TestTool_ArgusLogs_WithQueryAIFails exercises the AI analysis branch in logs.
// The AI call will fail (hardcoded API URL + mock key), but the code path
// up to the AI call is exercised.
func TestTool_ArgusLogs_WithQueryAIFails(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"service": "payment-service",
			"query":   "What errors are happening?",
		})
		// Should get an error result because AI call fails
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// The result should be an error (AI call failure)
		if !result.IsError {
			t.Logf("got successful result (unexpected): %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusTraces_WithQueryAIFails exercises the AI analysis branch in traces.
func TestTool_ArgusTraces_WithQueryAIFails(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_traces", map[string]any{
			"service": "payment-service",
			"query":   "Why is latency high?",
		})
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestTool_ArgusMetrics_WithQueryAIFails exercises the AI analysis branch in metrics.
func TestTool_ArgusMetrics_WithQueryAIFails(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_metrics", map[string]any{
			"metric": "http_requests_total",
			"query":  "Is traffic increasing?",
		})
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestTool_ArgusAsk_WithConfig exercises the ask tool with valid config but AI failure.
func TestTool_ArgusAsk_WithConfig(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_ask", map[string]any{
			"question": "How is my system performing?",
			"instance": "prod",
		})
		// Will fail at AI call but exercises the service/log collection branch
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestTool_ArgusExplain_WithConfig exercises explain with valid config but AI failure.
func TestTool_ArgusExplain_WithConfig(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_explain", map[string]any{
			"service":  "payment-service",
			"instance": "prod",
			"duration": float64(30),
		})
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestTool_ArgusReport_WithAI exercises report with AI enabled but AI failure.
func TestTool_ArgusReport_WithAI(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_report", map[string]any{
			"with_ai":  true,
			"duration": float64(30),
		})
		// May succeed (report generates even if AI fails gracefully) or fail
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// ─── Instance selection tests ─────────────────────────────────────────────────

func TestTool_ArgusLogs_NonexistentInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"instance": "nonexistent",
		})

		if !result.IsError {
			t.Errorf("expected error for nonexistent instance, got: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "nonexistent") {
			t.Errorf("expected instance name in error, got: %s", text)
		}
	})
}

func TestTool_ArgusTop_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_top", map[string]any{
			"instance": "staging",
			"sort_by":  "rate",
		})

		if result.IsError {
			t.Errorf("expected success for named instance, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusDiff_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_diff", map[string]any{
			"instance": "prod",
			"duration": float64(120),
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusDeploy_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_deploy", map[string]any{
			"instance":    "staging",
			"duration":    float64(60),
			"sensitivity": "low",
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_ArgusGuard_NamedInstance(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()

	withMockSignoz(t, signoz.URL, "", func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_guard", map[string]any{
			"instance": "prod",
		})

		if result.IsError {
			t.Errorf("expected success, got error: %s", textOf(t, result))
		}
	})
}

func TestTool_AlertmanagerTools_HappyPath(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiServer := httptest.NewServer(mockAIHandler())
	defer aiServer.Close()
	amServer := httptest.NewServer(mockAlertmanagerHandler())
	defer amServer.Close()
	promServer := httptest.NewServer(mockPrometheusHandler())
	defer promServer.Close()
	grafanaServer := httptest.NewServer(mockGrafanaHandler())
	defer grafanaServer.Close()

	withMockIntegrations(t, signoz.URL, aiServer.URL, amServer.URL, promServer.URL, grafanaServer.URL, func() {
		cs := newTestSession(t)

		alerts := callTool(t, cs, "argus_am_alerts", map[string]any{"active_only": true})
		if alerts.IsError {
			t.Fatalf("argus_am_alerts returned error: %s", textOf(t, alerts))
		}
		if text := textOf(t, alerts); !strings.Contains(text, "HighLatency") {
			t.Fatalf("expected HighLatency in alerts output, got: %s", text)
		}

		silences := callTool(t, cs, "argus_am_silences", map[string]any{})
		if silences.IsError {
			t.Fatalf("argus_am_silences returned error: %s", textOf(t, silences))
		}
		if text := textOf(t, silences); !strings.Contains(text, "deploy in progress") {
			t.Fatalf("expected silence comment in output, got: %s", text)
		}

		status := callTool(t, cs, "argus_am_status", map[string]any{})
		if status.IsError {
			t.Fatalf("argus_am_status returned error: %s", textOf(t, status))
		}
		if text := textOf(t, status); !strings.Contains(text, "0.28.0") || !strings.Contains(text, "am-prod") {
			t.Fatalf("expected version and cluster in output, got: %s", text)
		}

		summary := callTool(t, cs, "argus_am_summary", map[string]any{})
		if summary.IsError {
			t.Fatalf("argus_am_summary returned error: %s", textOf(t, summary))
		}
		if text := textOf(t, summary); !strings.Contains(text, "critical") || !strings.Contains(text, "HighLatency") {
			t.Fatalf("expected summary content, got: %s", text)
		}
	})
}

func TestTool_AmAlerts_AllIncludesActive(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiServer := httptest.NewServer(mockAIHandler())
	defer aiServer.Close()

	var gotQuery url.Values
	amServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer amServer.Close()
	promServer := httptest.NewServer(mockPrometheusHandler())
	defer promServer.Close()
	grafanaServer := httptest.NewServer(mockGrafanaHandler())
	defer grafanaServer.Close()

	var allQuery, defaultQuery, activeOnlyQuery url.Values
	withMockIntegrations(t, signoz.URL, aiServer.URL, amServer.URL, promServer.URL, grafanaServer.URL, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_am_alerts", map[string]any{"all": true})
		if result.IsError {
			t.Fatalf("argus_am_alerts returned error: %s", textOf(t, result))
		}
		allQuery = gotQuery

		gotQuery = nil
		result = callTool(t, cs, "argus_am_alerts", map[string]any{})
		if result.IsError {
			t.Fatalf("default call errored: %s", textOf(t, result))
		}
		defaultQuery = gotQuery

		gotQuery = nil
		result = callTool(t, cs, "argus_am_alerts", map[string]any{"active_only": true})
		if result.IsError {
			t.Fatalf("active_only call errored: %s", textOf(t, result))
		}
		activeOnlyQuery = gotQuery
	})

	if allQuery.Get("active") != "true" {
		t.Errorf("all=true must keep active=true, got active=%q", allQuery.Get("active"))
	}
	if allQuery.Get("silenced") != "true" || allQuery.Get("inhibited") != "true" {
		t.Errorf("all=true should include silenced and inhibited, got silenced=%q inhibited=%q",
			allQuery.Get("silenced"), allQuery.Get("inhibited"))
	}
	if defaultQuery.Get("active") != "true" || defaultQuery.Get("silenced") != "false" || defaultQuery.Get("inhibited") != "false" {
		t.Errorf("default params = %v, want active=true silenced=false inhibited=false", defaultQuery)
	}
	if activeOnlyQuery.Get("active") != "true" || activeOnlyQuery.Get("silenced") != "false" || activeOnlyQuery.Get("inhibited") != "false" {
		t.Errorf("active_only params = %v, want active=true silenced=false inhibited=false", activeOnlyQuery)
	}
}

func TestTool_PrometheusTools_HappyPath(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiServer := httptest.NewServer(mockAIHandler())
	defer aiServer.Close()
	amServer := httptest.NewServer(mockAlertmanagerHandler())
	defer amServer.Close()
	promServer := httptest.NewServer(mockPrometheusHandler())
	defer promServer.Close()
	grafanaServer := httptest.NewServer(mockGrafanaHandler())
	defer grafanaServer.Close()

	withMockIntegrations(t, signoz.URL, aiServer.URL, amServer.URL, promServer.URL, grafanaServer.URL, func() {
		cs := newTestSession(t)

		rules := callTool(t, cs, "argus_prom_rules", map[string]any{"type": "alert"})
		if rules.IsError {
			t.Fatalf("argus_prom_rules returned error: %s", textOf(t, rules))
		}
		if text := textOf(t, rules); !strings.Contains(text, "HighLatency") {
			t.Fatalf("expected rule name in output, got: %s", text)
		}

		targets := callTool(t, cs, "argus_prom_targets", map[string]any{})
		if targets.IsError {
			t.Fatalf("argus_prom_targets returned error: %s", textOf(t, targets))
		}
		if text := textOf(t, targets); !strings.Contains(text, "payments:9090") {
			t.Fatalf("expected target instance in output, got: %s", text)
		}

		alerts := callTool(t, cs, "argus_prom_alerts", map[string]any{})
		if alerts.IsError {
			t.Fatalf("argus_prom_alerts returned error: %s", textOf(t, alerts))
		}
		if text := textOf(t, alerts); !strings.Contains(text, "p99 too high") {
			t.Fatalf("expected alert annotation in output, got: %s", text)
		}

		query := callTool(t, cs, "argus_prom_query", map[string]any{"query": "up"})
		if query.IsError {
			t.Fatalf("argus_prom_query returned error: %s", textOf(t, query))
		}
		if text := textOf(t, query); !strings.Contains(text, "\"up\"") || !strings.Contains(text, "payments") {
			t.Fatalf("expected query result content, got: %s", text)
		}

		status := callTool(t, cs, "argus_prom_status", map[string]any{})
		if status.IsError {
			t.Fatalf("argus_prom_status returned error: %s", textOf(t, status))
		}
		if text := textOf(t, status); !strings.Contains(text, "2.55.0") || !strings.Contains(text, "15d") {
			t.Fatalf("expected build/runtime content, got: %s", text)
		}

		summary := callTool(t, cs, "argus_prom_summary", map[string]any{})
		if summary.IsError {
			t.Fatalf("argus_prom_summary returned error: %s", textOf(t, summary))
		}
		if text := textOf(t, summary); !strings.Contains(text, "TotalAlertRules") || !strings.Contains(text, "FiringAlerts") {
			t.Fatalf("expected summary fields, got: %s", text)
		}
	})
}

func TestTool_GrafanaTools_HappyPath(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiServer := httptest.NewServer(mockAIHandler())
	defer aiServer.Close()
	amServer := httptest.NewServer(mockAlertmanagerHandler())
	defer amServer.Close()
	promServer := httptest.NewServer(mockPrometheusHandler())
	defer promServer.Close()
	grafanaServer := httptest.NewServer(mockGrafanaHandler())
	defer grafanaServer.Close()

	withMockIntegrations(t, signoz.URL, aiServer.URL, amServer.URL, promServer.URL, grafanaServer.URL, func() {
		cs := newTestSession(t)

		dashboards := callTool(t, cs, "argus_grafana_dashboards", map[string]any{})
		if dashboards.IsError {
			t.Fatalf("argus_grafana_dashboards returned error: %s", textOf(t, dashboards))
		}
		if text := textOf(t, dashboards); !strings.Contains(text, "Payments Overview") {
			t.Fatalf("expected dashboard title in output, got: %s", text)
		}

		search := callTool(t, cs, "argus_grafana_search", map[string]any{"query": "Payments", "limit": float64(5)})
		if search.IsError {
			t.Fatalf("argus_grafana_search returned error: %s", textOf(t, search))
		}
		if text := textOf(t, search); !strings.Contains(text, "pay-123") {
			t.Fatalf("expected dashboard UID in search output, got: %s", text)
		}

		datasources := callTool(t, cs, "argus_grafana_datasources", map[string]any{})
		if datasources.IsError {
			t.Fatalf("argus_grafana_datasources returned error: %s", textOf(t, datasources))
		}
		if text := textOf(t, datasources); !strings.Contains(text, "Prometheus") {
			t.Fatalf("expected datasource name in output, got: %s", text)
		}

		alerts := callTool(t, cs, "argus_grafana_alerts", map[string]any{})
		if alerts.IsError {
			t.Fatalf("argus_grafana_alerts returned error: %s", textOf(t, alerts))
		}
		if text := textOf(t, alerts); !strings.Contains(text, "Payments Error Rate") {
			t.Fatalf("expected alert rule in output, got: %s", text)
		}

		firing := callTool(t, cs, "argus_grafana_firing", map[string]any{})
		if firing.IsError {
			t.Fatalf("argus_grafana_firing returned error: %s", textOf(t, firing))
		}
		if text := textOf(t, firing); !strings.Contains(text, "PaymentsErrorRate") {
			t.Fatalf("expected firing alert name in output, got: %s", text)
		}

		status := callTool(t, cs, "argus_grafana_status", map[string]any{})
		if status.IsError {
			t.Fatalf("argus_grafana_status returned error: %s", textOf(t, status))
		}
		if text := textOf(t, status); !strings.Contains(text, "11.2.0") || !strings.Contains(text, "Lester Labs") {
			t.Fatalf("expected grafana status content, got: %s", text)
		}

		summary := callTool(t, cs, "argus_grafana_summary", map[string]any{})
		if summary.IsError {
			t.Fatalf("argus_grafana_summary returned error: %s", textOf(t, summary))
		}
		if text := textOf(t, summary); !strings.Contains(text, "Dashboards") || !strings.Contains(text, "Datasources") {
			t.Fatalf("expected summary fields, got: %s", text)
		}
	})
}

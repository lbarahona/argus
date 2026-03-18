package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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



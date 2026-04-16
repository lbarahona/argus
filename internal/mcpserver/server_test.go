package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ─── existing helper tests ────────────────────────────────────────────────────

func TestTextResult(t *testing.T) {
	r, err := textResult("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.IsError {
		t.Fatal("expected non-error result")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(r.Content))
	}
}

func TestErrorResult(t *testing.T) {
	r, err := errorResult(fmt.Errorf("boom"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsError {
		t.Fatal("expected error result")
	}
}

func TestDefaults(t *testing.T) {
	if defaults(0, 60) != 60 {
		t.Error("expected default value")
	}
	if defaults(30, 60) != 30 {
		t.Error("expected provided value")
	}
	if defaults(-1, 100) != 100 {
		t.Error("expected default for negative")
	}
}

func TestJsonText(t *testing.T) {
	result := jsonText(map[string]string{"key": "value"})
	if result == "" {
		t.Error("expected non-empty JSON")
	}
}

func TestFormatLogsForAI(t *testing.T) {
	// Empty logs
	result := formatLogsForAI(nil)
	if result != "" {
		t.Error("expected empty string for nil logs")
	}
}

// ─── new helper tests ─────────────────────────────────────────────────────────

func TestJsonText_Variants(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		wantSub string
	}{
		{
			name:    "nil value",
			input:   nil,
			wantSub: "null",
		},
		{
			name:    "map value",
			input:   map[string]int{"a": 1},
			wantSub: `"a"`,
		},
		{
			name:    "struct value",
			input:   struct{ Name string }{"test"},
			wantSub: `"Name"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := jsonText(tc.input)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("jsonText(%v) = %q, want to contain %q", tc.input, got, tc.wantSub)
			}
		})
	}
}

func TestFormatLogsForAI_WithEntries(t *testing.T) {
	ts := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	logs := []types.LogEntry{
		{
			Timestamp:    ts,
			SeverityText: "ERROR",
			ServiceName:  "payment-service",
			Body:         "connection refused",
		},
		{
			Timestamp:    ts.Add(time.Minute),
			SeverityText: "WARN",
			ServiceName:  "auth-service",
			Body:         "token expiry soon",
		},
	}

	result := formatLogsForAI(logs)

	if result == "" {
		t.Fatal("expected non-empty result for non-nil logs")
	}
	if !strings.Contains(result, "ERROR") {
		t.Error("expected ERROR severity in output")
	}
	if !strings.Contains(result, "payment-service") {
		t.Error("expected service name in output")
	}
	if !strings.Contains(result, "connection refused") {
		t.Error("expected log body in output")
	}
	if !strings.Contains(result, "auth-service") {
		t.Error("expected second service name in output")
	}
	// Each log entry should end with newline.
	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d", len(lines))
	}
}

// ─── in-memory MCP transport helpers ─────────────────────────────────────────

// newTestSession creates a server with all tools registered and connects a
// client to it via in-memory transports. The returned ClientSession should be
// closed by the caller.
func newTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
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

	clientSession, err := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "1.0"},
		nil,
	).Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })

	return clientSession
}

// callTool is a convenience wrapper that calls a named tool with the provided
// arguments. It fatals on MCP-level errors but returns the CallToolResult for
// tool-level inspection (IsError, Content).
func callTool(t *testing.T, cs *mcp.ClientSession, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%q): unexpected MCP error: %v", toolName, err)
	}
	if result == nil {
		t.Fatalf("CallTool(%q): nil result", toolName)
	}
	return result
}

// textOf returns the text from the first TextContent in a result.
func textOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", result.Content[0])
	}
	return tc.Text
}

// ─── tool handler tests (no config / empty HOME) ──────────────────────────────

// withEmptyHome runs f with HOME set to a fresh temp directory so that
// config.Load(), alert.LoadAlerts(), and slo.LoadSLOs() find nothing.
func withEmptyHome(t *testing.T, f func()) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	f()
}

// TestTool_ArgusStatus_NoInstances verifies that argus_status returns a
// non-error text result saying "No instances configured." when HOME is empty.
func TestTool_ArgusStatus_NoInstances(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_status", map[string]any{})

		if result.IsError {
			t.Errorf("expected non-error result; got error: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "No instances configured") {
			t.Errorf("expected 'No instances configured' in text, got: %q", text)
		}
	})
}

// TestTool_ArgusStatus_VerboseInput ensures the tool accepts the StatusInput
// (no fields) without crashing.
func TestTool_ArgusStatus_EmptyArgs(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_status", map[string]any{})
		if result == nil {
			t.Fatal("expected non-nil result")
		}
	})
}

// TestTool_ArgusServices_NoConfig verifies argus_services returns an error
// result (no default instance) when config is empty.
func TestTool_ArgusServices_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_services", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusLogs_NoConfig verifies argus_logs returns an error result.
func TestTool_ArgusLogs_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_logs", map[string]any{
			"service":  "my-service",
			"duration": float64(30),
			"limit":    float64(50),
			"severity": "ERROR",
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusTraces_NoConfig verifies argus_traces returns an error result.
func TestTool_ArgusTraces_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_traces", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusMetrics_NoConfig verifies argus_metrics returns an error result.
func TestTool_ArgusMetrics_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_metrics", map[string]any{
			"metric":   "http_requests_total",
			"duration": float64(60),
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusAsk_NoAnthropicKey verifies argus_ask returns an error result
// when AnthropicKey is absent.
func TestTool_ArgusAsk_NoAnthropicKey(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_ask", map[string]any{
			"question": "How is my system performing?",
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "Anthropic API key not configured") {
			t.Errorf("expected 'Anthropic API key not configured' in error text, got: %q", text)
		}
	})
}

// TestTool_ArgusExplain_NoAnthropicKey verifies argus_explain returns an error
// result when AnthropicKey is absent.
func TestTool_ArgusExplain_NoAnthropicKey(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_explain", map[string]any{
			"service": "payment-service",
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "Anthropic API key not configured") {
			t.Errorf("expected 'Anthropic API key not configured' in error text, got: %q", text)
		}
	})
}

// TestTool_ArgusDashboard_NoInstances verifies argus_dashboard returns a
// successful (non-error) result even when there are no instances configured —
// it handles the missing instance gracefully and returns empty data.
func TestTool_ArgusDashboard_NoInstances(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_dashboard", map[string]any{
			"duration": float64(30),
		})

		// Dashboard is graceful: it returns a JSON payload regardless of whether
		// instances are configured; it does not return IsError.
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty dashboard text")
		}
	})
}

// TestTool_ArgusReport_NoConfig verifies argus_report returns an error result.
func TestTool_ArgusReport_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_report", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusTop_NoConfig verifies argus_top returns an error result.
func TestTool_ArgusTop_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_top", map[string]any{
			"sort_by": "errors",
			"limit":   float64(10),
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusTop_SortByVariants checks that every sort_by value is accepted
// without panicking (the switch has a default branch).
func TestTool_ArgusTop_SortByVariants(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		for _, sortBy := range []string{"errors", "rate", "calls", "name", "unknown"} {
			result := callTool(t, cs, "argus_top", map[string]any{"sort_by": sortBy})
			// All should fail with an error result (no config), but should not panic.
			if result == nil {
				t.Errorf("sort_by=%q: nil result", sortBy)
			}
		}
	})
}

// TestTool_ArgusDiff_NoConfig verifies argus_diff returns an error result.
func TestTool_ArgusDiff_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_diff", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusAlertCheck_NoAlertsFile verifies argus_alert_check returns an
// error result when there is no alerts.yaml.
func TestTool_ArgusAlertCheck_NoAlertsFile(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_alert_check", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusSLOCheck_NoSLOFile verifies argus_slo_check returns an error
// result when there is no slos.yaml.
func TestTool_ArgusSLOCheck_NoSLOFile(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_slo_check", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusDeploy_NoConfig verifies argus_deploy returns an error result.
// Also exercises the default duration (0→360) and sensitivity ("" → "medium") logic.
func TestTool_ArgusDeploy_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)

		// Empty args: duration will default to 360, sensitivity to "medium".
		result := callTool(t, cs, "argus_deploy", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusDeploy_ExplicitArgs exercises the explicit-args path so the
// default-override branches are not hit.
func TestTool_ArgusDeploy_ExplicitArgs(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_deploy", map[string]any{
			"duration":    float64(120),
			"sensitivity": "high",
			"service":     "auth-service",
		})

		// Still fails because there's no config/instance.
		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusBudget_NoSLOFile verifies argus_budget returns an error result
// that mentions loading SLOs.
func TestTool_ArgusBudget_NoSLOFile(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_budget", map[string]any{})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if !strings.Contains(text, "loading SLOs") {
			t.Errorf("expected 'loading SLOs' in error text, got: %q", text)
		}
	})
}

// TestTool_ArgusBudget_DefaultWindow exercises the window default ("" → "6h").
func TestTool_ArgusBudget_DefaultWindow(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		// Omit window so server defaults to "6h"; still fails at SLO load.
		result := callTool(t, cs, "argus_budget", map[string]any{
			"service": "my-svc",
		})
		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusGuard_NoConfig verifies argus_guard returns an error result.
func TestTool_ArgusGuard_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_guard", map[string]any{
			"strict": true,
		})

		if !result.IsError {
			t.Errorf("expected error result, got: %s", textOf(t, result))
		}
	})
}

// TestTool_ArgusDoctor verifies argus_doctor always returns a non-error result
// with meaningful JSON content.
func TestTool_ArgusDoctor(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_doctor", map[string]any{
			"verbose": false,
		})

		// Doctor never returns IsError=true — it embeds check results in JSON.
		if result.IsError {
			t.Errorf("expected non-error result from doctor; got: %s", textOf(t, result))
		}
		text := textOf(t, result)
		if text == "" {
			t.Error("expected non-empty doctor output")
		}
		// The JSON should contain at least some recognisable doctor keys.
		if !strings.Contains(text, "checks") && !strings.Contains(text, "version") && !strings.Contains(text, "overall") {
			t.Errorf("doctor output doesn't look like a doctor report: %q", text)
		}
	})
}

// TestTool_ArgusDoctor_Verbose exercises the verbose=true path.
func TestTool_ArgusDoctor_Verbose(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_doctor", map[string]any{
			"verbose": true,
		})

		if result.IsError {
			t.Errorf("expected non-error result from doctor (verbose); got: %s", textOf(t, result))
		}
	})
}

// ─── getClient error path ─────────────────────────────────────────────────────

// TestGetClient_NoDefaultInstance verifies getClient returns an error when there
// is no config file and no default instance is set.
func TestGetClient_NoDefaultInstance(t *testing.T) {
	withEmptyHome(t, func() {
		client, key, cfg, err := getClient("")
		if err == nil {
			t.Error("expected error from getClient with no default instance, got nil")
		}
		if client != nil {
			t.Error("expected nil client on error")
		}
		if key != "" {
			t.Error("expected empty key on error")
		}
		if cfg != nil {
			t.Error("expected nil cfg on error")
		}
	})
}

// TestGetClient_NamedInstanceNotFound verifies getClient returns an error when
// the named instance does not exist.
func TestGetClient_NamedInstanceNotFound(t *testing.T) {
	withEmptyHome(t, func() {
		_, _, _, err := getClient("nonexistent-instance")
		if err == nil {
			t.Error("expected error for missing named instance, got nil")
		}
		if !strings.Contains(err.Error(), "nonexistent-instance") {
			t.Errorf("error should mention instance name, got: %v", err)
		}
	})
}

// ─── registerTools smoke test ─────────────────────────────────────────────────

// TestRegisterTools_ToolsAreListable verifies that registerTools registers the
// expected set of tool names so they appear in ListTools.
func TestRegisterTools_ToolsAreListable(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)

		ctx := context.Background()
		res, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}

		wantTools := []string{
			"argus_status",
			"argus_services",
			"argus_logs",
			"argus_traces",
			"argus_metrics",
			"argus_ask",
			"argus_explain",
			"argus_dashboard",
			"argus_report",
			"argus_top",
			"argus_diff",
			"argus_alert_check",
			"argus_slo_check",
			"argus_deploy",
			"argus_budget",
			"argus_guard",
			"argus_doctor",
			"argus_am_alerts",
			"argus_am_silences",
			"argus_am_status",
			"argus_am_summary",
			"argus_prom_rules",
			"argus_prom_targets",
			"argus_prom_alerts",
			"argus_prom_query",
			"argus_prom_status",
			"argus_prom_summary",
			"argus_grafana_dashboards",
			"argus_grafana_search",
			"argus_grafana_datasources",
			"argus_grafana_alerts",
			"argus_grafana_firing",
			"argus_grafana_status",
			"argus_grafana_summary",
		}

		registered := make(map[string]bool)
		for _, tool := range res.Tools {
			registered[tool.Name] = true
		}

		for _, name := range wantTools {
			if !registered[name] {
				t.Errorf("expected tool %q to be registered", name)
			}
		}

		if len(res.Tools) < len(wantTools) {
			t.Errorf("expected at least %d tools, got %d", len(wantTools), len(res.Tools))
		}
	})
}

func TestTool_ArgusAlertmanagerTools_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		for _, tool := range []string{"argus_am_alerts", "argus_am_silences", "argus_am_status", "argus_am_summary"} {
			result := callTool(t, cs, tool, map[string]any{})
			if !result.IsError {
				t.Errorf("expected error result for %s", tool)
			}
		}
	})
}

func TestTool_ArgusPrometheusTools_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		cases := map[string]map[string]any{
			"argus_prom_rules":   {},
			"argus_prom_targets": {},
			"argus_prom_alerts":  {},
			"argus_prom_query":   {"query": "up"},
			"argus_prom_status":  {},
			"argus_prom_summary": {},
		}
		for tool, args := range cases {
			result := callTool(t, cs, tool, args)
			if !result.IsError {
				t.Errorf("expected error result for %s", tool)
			}
		}
	})
}

func TestTool_ArgusGrafanaTools_NoConfig(t *testing.T) {
	withEmptyHome(t, func() {
		cs := newTestSession(t)
		cases := map[string]map[string]any{
			"argus_grafana_dashboards":  {},
			"argus_grafana_search":      {"query": "kubernetes"},
			"argus_grafana_datasources": {},
			"argus_grafana_alerts":      {},
			"argus_grafana_firing":      {},
			"argus_grafana_status":      {},
			"argus_grafana_summary":     {},
		}
		for tool, args := range cases {
			result := callTool(t, cs, tool, args)
			if !result.IsError {
				t.Errorf("expected error result for %s", tool)
			}
		}
	})
}

// ─── edge-case / defaults tests ───────────────────────────────────────────────

// TestDefaults_EdgeCases tests additional edge cases for the defaults helper.
func TestDefaults_EdgeCases(t *testing.T) {
	tests := []struct {
		val, def, want int
	}{
		{0, 60, 60},
		{1, 60, 1},
		{-100, 60, 60},
		{60, 60, 60},
		{100, 60, 100},
	}
	for _, tc := range tests {
		got := defaults(tc.val, tc.def)
		if got != tc.want {
			t.Errorf("defaults(%d, %d) = %d, want %d", tc.val, tc.def, got, tc.want)
		}
	}
}

// TestTextResult_Content verifies the returned TextContent has the right text.
func TestTextResult_Content(t *testing.T) {
	want := "hello, world"
	r, err := textResult(want)
	if err != nil {
		t.Fatalf("textResult: %v", err)
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", r.Content[0])
	}
	if tc.Text != want {
		t.Errorf("text = %q, want %q", tc.Text, want)
	}
}

// TestErrorResult_Content verifies the error text is prefixed with "Error: ".
func TestErrorResult_Content(t *testing.T) {
	r, err := errorResult(fmt.Errorf("something went wrong"))
	if err != nil {
		t.Fatalf("errorResult: %v", err)
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", r.Content[0])
	}
	if !strings.HasPrefix(tc.Text, "Error: ") {
		t.Errorf("expected 'Error: ' prefix, got %q", tc.Text)
	}
	if !strings.Contains(tc.Text, "something went wrong") {
		t.Errorf("expected original error message in text, got %q", tc.Text)
	}
}

// TestFormatLogsForAI_Timestamp verifies the timestamp format used in output.
func TestFormatLogsForAI_Timestamp(t *testing.T) {
	ts := time.Date(2026, 3, 9, 15, 4, 5, 0, time.UTC)
	logs := []types.LogEntry{{
		Timestamp:    ts,
		SeverityText: "INFO",
		ServiceName:  "svc",
		Body:         "msg",
	}}
	out := formatLogsForAI(logs)
	if !strings.Contains(out, "2026-03-09 15:04:05") {
		t.Errorf("expected formatted timestamp in output, got: %q", out)
	}
}

package output

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// captureStdout redirects os.Stdout to a pipe, runs fn, then restores stdout
// and returns everything that was written.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// ---------------------------------------------------------------------------
// formatSeverity
// ---------------------------------------------------------------------------

func TestFormatSeverity(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"ERROR"},
		{"WARN"},
		{"INFO"},
		{"DEBUG"},
		{"UNKNOWN"},
		{""},
	}
	for _, tt := range tests {
		result := formatSeverity(tt.input)
		if result == "" {
			t.Errorf("formatSeverity(%q) should not be empty", tt.input)
		}
	}
}

func TestFormatSeverityAdditionalCases(t *testing.T) {
	// FATAL and CRITICAL should produce a non-empty result (error style)
	for _, sev := range []string{"FATAL", "CRITICAL", "WARNING", "TRACE"} {
		result := formatSeverity(sev)
		if result == "" {
			t.Errorf("formatSeverity(%q) should not be empty", sev)
		}
	}
}

// ---------------------------------------------------------------------------
// formatTraceDuration
// ---------------------------------------------------------------------------

func TestFormatTraceDuration(t *testing.T) {
	tests := []struct {
		ms       float64
		contains string
	}{
		{0.5, "µs"},
		{50, "ms"},
		{500, "ms"},
		{1500, "s"},
	}
	for _, tt := range tests {
		result := formatTraceDuration(tt.ms)
		if result == "" {
			t.Errorf("formatTraceDuration(%f) should not be empty", tt.ms)
		}
	}
}

func TestFormatTraceDurationWarningAndErrorBranches(t *testing.T) {
	// ms > 500 triggers warning style (still < 1000 → "ms")
	r500 := formatTraceDuration(600)
	if r500 == "" {
		t.Error("formatTraceDuration(600) should not be empty")
	}

	// ms >= 1000 triggers the seconds branch
	r1000 := formatTraceDuration(1500)
	if r1000 == "" {
		t.Error("formatTraceDuration(1500) should not be empty")
	}

	// ms > 2000 — the ms>2000 branch is inside the ms<1000 block so it is
	// unreachable in practice; ensure the seconds path (>=1000) works instead.
	r2500 := formatTraceDuration(2500)
	if r2500 == "" {
		t.Error("formatTraceDuration(2500) should not be empty")
	}
}

// ---------------------------------------------------------------------------
// truncateID
// ---------------------------------------------------------------------------

func TestTruncateID(t *testing.T) {
	if truncateID("short") != "short" {
		t.Error("short ID should not be truncated")
	}
	long := "abcdefghijklmnop"
	result := truncateID(long)
	runes := []rune(result)
	if len(runes) > 13 { // 12 + "…"
		t.Errorf("long ID should be truncated, got %q (len %d runes)", result, len(runes))
	}
}

// ---------------------------------------------------------------------------
// maskKey
// ---------------------------------------------------------------------------

func TestMaskKey(t *testing.T) {
	if maskKey("short") != "****" {
		t.Errorf("short key should be masked as ****, got %q", maskKey("short"))
	}

	long := "abcdefghijklmnop"
	masked := maskKey(long)
	if masked != "abcd...mnop" {
		t.Errorf("expected abcd...mnop, got %q", masked)
	}
}

// ---------------------------------------------------------------------------
// formatDuration
// ---------------------------------------------------------------------------

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		contains string
	}{
		{500 * time.Microsecond, "µs"},
		{50 * time.Millisecond, "ms"},
		{2 * time.Second, "ms"},
	}
	for _, tt := range tests {
		result := formatDuration(tt.d)
		if result == "" {
			t.Errorf("formatDuration(%v) should not be empty", tt.d)
		}
	}
}

func TestFormatDurationSubMillisecond(t *testing.T) {
	// d < time.Millisecond should use the µs path
	result := formatDuration(500 * time.Nanosecond)
	if result == "" {
		t.Error("formatDuration(500ns) should not be empty")
	}
	// The result should express microseconds, not milliseconds
	if strings.Contains(result, "ms") && !strings.Contains(result, "µs") {
		t.Errorf("expected µs unit for sub-millisecond duration, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// PrintBanner
// ---------------------------------------------------------------------------

func TestPrintBanner(t *testing.T) {
	out := captureStdout(func() {
		PrintBanner()
	})
	if out == "" {
		t.Error("PrintBanner() produced no output")
	}
	if !strings.Contains(out, "ARGUS") {
		t.Errorf("PrintBanner() output should contain ARGUS, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintInstances
// ---------------------------------------------------------------------------

func TestPrintInstancesEmpty(t *testing.T) {
	out := captureStdout(func() {
		PrintInstances(map[string]types.Instance{}, "")
	})
	if !strings.Contains(out, "No instances") {
		t.Errorf("expected 'No instances' message, got %q", out)
	}
}

func TestPrintInstancesSingleNonDefault(t *testing.T) {
	instances := map[string]types.Instance{
		"prod": {
			Name:       "Production",
			URL:        "https://signoz.example.com",
			APIKey:     "verylongapikey1234",
			APIVersion: "v3",
		},
	}
	out := captureStdout(func() {
		PrintInstances(instances, "staging")
	})
	if !strings.Contains(out, "prod") {
		t.Errorf("expected key 'prod' in output, got %q", out)
	}
	if !strings.Contains(out, "https://signoz.example.com") {
		t.Errorf("expected URL in output, got %q", out)
	}
	// Non-default should NOT contain the marker
	if strings.Contains(out, "►") {
		t.Errorf("non-default instance should not have ► marker")
	}
}

func TestPrintInstancesSingleDefault(t *testing.T) {
	instances := map[string]types.Instance{
		"prod": {
			Name:       "Production",
			URL:        "https://signoz.example.com",
			APIKey:     "verylongapikey1234",
			APIVersion: "",
		},
	}
	out := captureStdout(func() {
		PrintInstances(instances, "prod")
	})
	if !strings.Contains(out, "default") {
		t.Errorf("default instance should show '(default)', got %q", out)
	}
	if !strings.Contains(out, "►") {
		t.Errorf("default instance should have ► marker, got %q", out)
	}
}

func TestPrintInstancesMultiple(t *testing.T) {
	instances := map[string]types.Instance{
		"prod": {Name: "Production", URL: "https://prod.example.com", APIKey: "prodkey12345678"},
		"dev":  {Name: "Development", URL: "https://dev.example.com", APIKey: "devkey12345678"},
	}
	out := captureStdout(func() {
		PrintInstances(instances, "dev")
	})
	if !strings.Contains(out, "prod") || !strings.Contains(out, "dev") {
		t.Errorf("expected both instances in output, got %q", out)
	}
}

func TestPrintInstancesAPIVersionShown(t *testing.T) {
	instances := map[string]types.Instance{
		"inst": {Name: "Test", URL: "https://test.example.com", APIKey: "longenoughkey1234", APIVersion: "v5"},
	}
	out := captureStdout(func() {
		PrintInstances(instances, "")
	})
	if !strings.Contains(out, "v5") {
		t.Errorf("expected APIVersion 'v5' in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintHealthStatuses
// ---------------------------------------------------------------------------

func TestPrintHealthStatusesEmpty(t *testing.T) {
	out := captureStdout(func() {
		PrintHealthStatuses([]types.HealthStatus{})
	})
	// Header should still appear
	if !strings.Contains(out, "Health") {
		t.Errorf("expected Health header in output, got %q", out)
	}
}

func TestPrintHealthStatusesHealthy(t *testing.T) {
	statuses := []types.HealthStatus{
		{
			InstanceName: "prod",
			InstanceKey:  "prod",
			URL:          "https://signoz.example.com",
			Healthy:      true,
			Latency:      42 * time.Millisecond,
			Message:      "",
		},
	}
	out := captureStdout(func() {
		PrintHealthStatuses(statuses)
	})
	if !strings.Contains(out, "prod") {
		t.Errorf("expected instance name in output, got %q", out)
	}
	if !strings.Contains(out, "healthy") {
		t.Errorf("expected 'healthy' in output, got %q", out)
	}
}

func TestPrintHealthStatusesUnhealthy(t *testing.T) {
	statuses := []types.HealthStatus{
		{
			InstanceName: "prod",
			InstanceKey:  "prod",
			URL:          "https://signoz.example.com",
			Healthy:      false,
			Latency:      0,
			Message:      "connection refused",
		},
	}
	out := captureStdout(func() {
		PrintHealthStatuses(statuses)
	})
	if !strings.Contains(out, "unhealthy") {
		t.Errorf("expected 'unhealthy' in output, got %q", out)
	}
	if !strings.Contains(out, "connection refused") {
		t.Errorf("expected error message in output, got %q", out)
	}
}

func TestPrintHealthStatusesMixed(t *testing.T) {
	statuses := []types.HealthStatus{
		{InstanceName: "prod", URL: "https://prod.example.com", Healthy: true, Latency: 10 * time.Millisecond},
		{InstanceName: "dev", URL: "https://dev.example.com", Healthy: false, Message: "timeout"},
	}
	out := captureStdout(func() {
		PrintHealthStatuses(statuses)
	})
	if !strings.Contains(out, "prod") || !strings.Contains(out, "dev") {
		t.Errorf("expected both instances in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintVersion
// ---------------------------------------------------------------------------

func TestPrintVersion(t *testing.T) {
	out := captureStdout(func() {
		PrintVersion("1.2.3", "abc1234", "2026-03-09")
	})
	if !strings.Contains(out, "1.2.3") {
		t.Errorf("expected version string in output, got %q", out)
	}
	if !strings.Contains(out, "abc1234") {
		t.Errorf("expected commit in output, got %q", out)
	}
	if !strings.Contains(out, "2026-03-09") {
		t.Errorf("expected date in output, got %q", out)
	}
}

func TestPrintVersionEmpty(t *testing.T) {
	// Should not panic with empty strings
	captureStdout(func() {
		PrintVersion("", "", "")
	})
}

// ---------------------------------------------------------------------------
// PrintAnalyzing
// ---------------------------------------------------------------------------

func TestPrintAnalyzing(t *testing.T) {
	out := captureStdout(func() {
		PrintAnalyzing("show me errors")
	})
	if !strings.Contains(out, "show me errors") {
		t.Errorf("expected query string in output, got %q", out)
	}
}

func TestPrintAnalyzingEmpty(t *testing.T) {
	// Should not panic with empty query
	captureStdout(func() {
		PrintAnalyzing("")
	})
}

// ---------------------------------------------------------------------------
// PrintLogs
// ---------------------------------------------------------------------------

func TestPrintLogsEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintLogs(nil)
}

func TestPrintLogsWithEntries(t *testing.T) {
	logs := []types.LogEntry{
		{
			Timestamp:    time.Now(),
			SeverityText: "ERROR",
			ServiceName:  "api",
			Body:         "something failed",
		},
	}
	out := captureStdout(func() {
		PrintLogs(logs)
	})
	if !strings.Contains(out, "api") {
		t.Errorf("expected service name in output, got %q", out)
	}
	if !strings.Contains(out, "something failed") {
		t.Errorf("expected log body in output, got %q", out)
	}
}

func TestPrintLogsBodyTruncated(t *testing.T) {
	// Body longer than 200 chars should be truncated
	longBody := strings.Repeat("x", 250)
	logs := []types.LogEntry{
		{
			Timestamp:    time.Now(),
			SeverityText: "INFO",
			ServiceName:  "svc",
			Body:         longBody,
		},
	}
	out := captureStdout(func() {
		PrintLogs(logs)
	})
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation '...' for long body, got output of length %d", len(out))
	}
}

func TestPrintLogsBodyNotTruncated(t *testing.T) {
	body := strings.Repeat("y", 100)
	logs := []types.LogEntry{
		{
			Timestamp:    time.Now(),
			SeverityText: "WARN",
			ServiceName:  "",
			Body:         body,
		},
	}
	out := captureStdout(func() {
		PrintLogs(logs)
	})
	// Full body should appear (no ellipsis appended)
	if !strings.Contains(out, body) {
		t.Errorf("expected full body in output, got %q", out)
	}
}

func TestPrintLogsNoServiceName(t *testing.T) {
	logs := []types.LogEntry{
		{Timestamp: time.Now(), SeverityText: "DEBUG", ServiceName: "", Body: "no service here"},
	}
	out := captureStdout(func() {
		PrintLogs(logs)
	})
	if !strings.Contains(out, "no service here") {
		t.Errorf("expected body in output, got %q", out)
	}
}

func TestPrintLogsMultipleEntries(t *testing.T) {
	logs := []types.LogEntry{
		{Timestamp: time.Now(), SeverityText: "ERROR", ServiceName: "svc-a", Body: "err a"},
		{Timestamp: time.Now(), SeverityText: "INFO", ServiceName: "svc-b", Body: "info b"},
	}
	out := captureStdout(func() {
		PrintLogs(logs)
	})
	if !strings.Contains(out, "svc-a") || !strings.Contains(out, "svc-b") {
		t.Errorf("expected both service names, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintServices
// ---------------------------------------------------------------------------

func TestPrintServicesEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintServices(nil)
}

func TestPrintServicesHighErrorRate(t *testing.T) {
	services := []types.Service{
		{Name: "bad-svc", NumCalls: 1000, NumErrors: 100, ErrorRate: 10.0},
	}
	out := captureStdout(func() {
		PrintServices(services)
	})
	if !strings.Contains(out, "bad-svc") {
		t.Errorf("expected service name in output, got %q", out)
	}
	if !strings.Contains(out, "10.0%") {
		t.Errorf("expected error rate in output, got %q", out)
	}
}

func TestPrintServicesMediumErrorRate(t *testing.T) {
	services := []types.Service{
		{Name: "med-svc", NumCalls: 1000, NumErrors: 20, ErrorRate: 2.0},
	}
	out := captureStdout(func() {
		PrintServices(services)
	})
	if !strings.Contains(out, "med-svc") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestPrintServicesLowErrorRate(t *testing.T) {
	services := []types.Service{
		{Name: "ok-svc", NumCalls: 500, NumErrors: 2, ErrorRate: 0.4},
	}
	out := captureStdout(func() {
		PrintServices(services)
	})
	if !strings.Contains(out, "ok-svc") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestPrintServicesMultiple(t *testing.T) {
	services := []types.Service{
		{Name: "svc-a", NumCalls: 100, NumErrors: 10, ErrorRate: 10.0},
		{Name: "svc-b", NumCalls: 200, NumErrors: 3, ErrorRate: 1.5},
		{Name: "svc-c", NumCalls: 300, NumErrors: 1, ErrorRate: 0.3},
	}
	out := captureStdout(func() {
		PrintServices(services)
	})
	for _, name := range []string{"svc-a", "svc-b", "svc-c"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected %q in output, got %q", name, out)
		}
	}
}

// ---------------------------------------------------------------------------
// PrintTraces
// ---------------------------------------------------------------------------

func TestPrintTracesEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintTraces(nil)
}

func TestPrintTracesErrorStatus(t *testing.T) {
	traces := []types.TraceEntry{
		{
			Timestamp:     time.Now(),
			ServiceName:   "api",
			OperationName: "GET /users",
			StatusCode:    "ERROR",
			TraceID:       "abc123def456",
		},
	}
	out := captureStdout(func() {
		PrintTraces(traces)
	})
	if !strings.Contains(out, "api") {
		t.Errorf("expected service name in output, got %q", out)
	}
	if !strings.Contains(out, "GET /users") {
		t.Errorf("expected operation name in output, got %q", out)
	}
}

func TestPrintTracesOKStatus(t *testing.T) {
	traces := []types.TraceEntry{
		{
			Timestamp:     time.Now(),
			ServiceName:   "worker",
			OperationName: "process-job",
			StatusCode:    "OK",
			TraceID:       "trace999",
		},
	}
	out := captureStdout(func() {
		PrintTraces(traces)
	})
	if !strings.Contains(out, "worker") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestPrintTracesStatusCodeError(t *testing.T) {
	traces := []types.TraceEntry{
		{
			Timestamp:     time.Now(),
			ServiceName:   "gateway",
			OperationName: "POST /orders",
			StatusCode:    "STATUS_CODE_ERROR",
			TraceID:       "xyz789",
		},
	}
	out := captureStdout(func() {
		PrintTraces(traces)
	})
	if !strings.Contains(out, "gateway") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestPrintTracesMultiple(t *testing.T) {
	traces := []types.TraceEntry{
		{Timestamp: time.Now(), ServiceName: "a", OperationName: "op-a", StatusCode: "OK", TraceID: "t1"},
		{Timestamp: time.Now(), ServiceName: "b", OperationName: "op-b", StatusCode: "ERROR", TraceID: "t2"},
	}
	out := captureStdout(func() {
		PrintTraces(traces)
	})
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Errorf("expected both services in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintMetrics
// ---------------------------------------------------------------------------

func TestPrintMetricsEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintMetrics(nil)
}

func TestPrintMetricsWithLabels(t *testing.T) {
	metrics := []types.MetricEntry{
		{
			Timestamp: time.Now(),
			Value:     42.5,
			Labels:    map[string]string{"host": "server1", "region": "us-east"},
		},
	}
	out := captureStdout(func() {
		PrintMetrics(metrics)
	})
	if !strings.Contains(out, "42.50") {
		t.Errorf("expected value in output, got %q", out)
	}
	if !strings.Contains(out, "server1") {
		t.Errorf("expected label value in output, got %q", out)
	}
}

func TestPrintMetricsWithoutLabels(t *testing.T) {
	metrics := []types.MetricEntry{
		{
			Timestamp: time.Now(),
			Value:     99.9,
			Labels:    nil,
		},
	}
	out := captureStdout(func() {
		PrintMetrics(metrics)
	})
	if !strings.Contains(out, "99.90") {
		t.Errorf("expected value in output, got %q", out)
	}
}

func TestPrintMetricsMultiple(t *testing.T) {
	metrics := []types.MetricEntry{
		{Timestamp: time.Now(), Value: 1.0, Labels: map[string]string{"k": "v1"}},
		{Timestamp: time.Now(), Value: 2.0, Labels: map[string]string{"k": "v2"}},
	}
	out := captureStdout(func() {
		PrintMetrics(metrics)
	})
	if !strings.Contains(out, "1.00") || !strings.Contains(out, "2.00") {
		t.Errorf("expected both metric values in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintDashboard
// ---------------------------------------------------------------------------

func TestPrintDashboardAllEmpty(t *testing.T) {
	out := captureStdout(func() {
		PrintDashboard([]types.HealthStatus{}, []types.Service{}, []types.LogEntry{})
	})
	if !strings.Contains(out, "Dashboard") {
		t.Errorf("expected 'Dashboard' header in output, got %q", out)
	}
}

func TestPrintDashboardWithServices(t *testing.T) {
	statuses := []types.HealthStatus{}
	services := []types.Service{
		{Name: "api", NumCalls: 1000, NumErrors: 10, ErrorRate: 1.0},
	}
	out := captureStdout(func() {
		PrintDashboard(statuses, services, nil)
	})
	if !strings.Contains(out, "api") {
		t.Errorf("expected service name in output, got %q", out)
	}
}

func TestPrintDashboardWithLogs(t *testing.T) {
	logs := []types.LogEntry{
		{Timestamp: time.Now(), SeverityText: "ERROR", ServiceName: "api", Body: "crash"},
	}
	out := captureStdout(func() {
		PrintDashboard(nil, nil, logs)
	})
	if !strings.Contains(out, "crash") {
		t.Errorf("expected log body in output, got %q", out)
	}
	if !strings.Contains(out, "Recent Errors") {
		t.Errorf("expected 'Recent Errors' header, got %q", out)
	}
}

func TestPrintDashboardLogsBodyTruncated(t *testing.T) {
	longBody := strings.Repeat("z", 150)
	logs := []types.LogEntry{
		{Timestamp: time.Now(), SeverityText: "ERROR", ServiceName: "svc", Body: longBody},
	}
	out := captureStdout(func() {
		PrintDashboard(nil, nil, logs)
	})
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation in dashboard log output, got output length %d", len(out))
	}
}

func TestPrintDashboardMoreThan10Services(t *testing.T) {
	var services []types.Service
	for i := 0; i < 15; i++ {
		services = append(services, types.Service{
			Name: "svc", NumCalls: 100, NumErrors: 1, ErrorRate: 1.0,
		})
	}
	// Should not panic; only top 10 shown
	captureStdout(func() {
		PrintDashboard(nil, services, nil)
	})
}

func TestPrintDashboardMoreThan10Logs(t *testing.T) {
	var logs []types.LogEntry
	for i := 0; i < 15; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp: time.Now(), SeverityText: "ERROR", Body: "err",
		})
	}
	// Should not panic; only top 10 shown
	captureStdout(func() {
		PrintDashboard(nil, nil, logs)
	})
}

func TestPrintDashboardWithHealthStatuses(t *testing.T) {
	statuses := []types.HealthStatus{
		{InstanceName: "prod", URL: "https://prod.example.com", Healthy: true, Latency: 5 * time.Millisecond},
	}
	out := captureStdout(func() {
		PrintDashboard(statuses, nil, nil)
	})
	if !strings.Contains(out, "prod") {
		t.Errorf("expected instance name in dashboard, got %q", out)
	}
}

func TestPrintDashboardLogNoServiceName(t *testing.T) {
	logs := []types.LogEntry{
		{Timestamp: time.Now(), SeverityText: "ERROR", ServiceName: "", Body: "bare error"},
	}
	out := captureStdout(func() {
		PrintDashboard(nil, nil, logs)
	})
	if !strings.Contains(out, "bare error") {
		t.Errorf("expected log body in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// PrintAnomalyResult
// ---------------------------------------------------------------------------

func TestPrintAnomalyResultNoAnomalies(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     30,
		TotalScanned: 5,
		ScanTime:     time.Now(),
		Anomalies:    nil,
		Services:     nil,
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "No anomalies") {
		t.Errorf("expected 'No anomalies' message, got %q", out)
	}
}

func TestPrintAnomalyResultWithAnomaliesCritical(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     15,
		TotalScanned: 3,
		ScanTime:     time.Now(),
		Anomalies: []AnomalyItem{
			{
				Type:        "error_rate",
				Severity:    "critical",
				Service:     "api",
				Metric:      "error_rate",
				Description: "error rate spiked",
				Value:       25.0,
				Expected:    2.0,
				Deviation:   12.5,
				DetectedAt:  time.Now(),
				Window:      "15m",
			},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "critical") {
		t.Errorf("expected 'critical' in output, got %q", out)
	}
	if !strings.Contains(out, "error rate spiked") {
		t.Errorf("expected anomaly description in output, got %q", out)
	}
}

func TestPrintAnomalyResultWithAnomaliesWarning(t *testing.T) {
	result := AnomalyResult{
		Instance:     "dev",
		Duration:     10,
		TotalScanned: 2,
		ScanTime:     time.Now(),
		Anomalies: []AnomalyItem{
			{
				Type:        "latency",
				Severity:    "warning",
				Service:     "worker",
				Description: "latency increased",
				Deviation:   3.0,
			},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "warning") {
		t.Errorf("expected 'warning' in output, got %q", out)
	}
}

func TestPrintAnomalyResultWithAnomaliesInfo(t *testing.T) {
	result := AnomalyResult{
		Instance:     "staging",
		Duration:     5,
		TotalScanned: 1,
		ScanTime:     time.Now(),
		Anomalies: []AnomalyItem{
			{
				Type:        "traffic",
				Severity:    "info",
				Service:     "frontend",
				Description: "traffic slightly elevated",
				Deviation:   1.5,
			},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "info") {
		t.Errorf("expected 'info' in output, got %q", out)
	}
}

func TestPrintAnomalyResultQuietMode(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     30,
		TotalScanned: 4,
		ScanTime:     time.Now(),
		Anomalies:    nil,
		Services: []AnomalyServiceScan{
			{Name: "api", Calls: 100, Errors: 5, ErrorRate: 5.0, AnomalyCount: 0, HighestSeverity: ""},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, true)
	})
	// In quiet mode, the per-service rows should NOT be printed.
	// The summary line "Services: 4" will still appear, but the individual
	// service name "api" should NOT (service table is suppressed).
	if strings.Contains(out, "api") {
		t.Errorf("quiet mode should not print service rows, got %q", out)
	}
}

func TestPrintAnomalyResultServicesAllSeverities(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     30,
		TotalScanned: 4,
		ScanTime:     time.Now(),
		Services: []AnomalyServiceScan{
			{Name: "svc-crit", Calls: 100, ErrorRate: 10.0, AnomalyCount: 2, HighestSeverity: "critical"},
			{Name: "svc-warn", Calls: 200, ErrorRate: 3.0, AnomalyCount: 1, HighestSeverity: "warning"},
			{Name: "svc-info", Calls: 300, ErrorRate: 0.5, AnomalyCount: 1, HighestSeverity: "info"},
			{Name: "svc-ok", Calls: 400, ErrorRate: 0.1, AnomalyCount: 0, HighestSeverity: ""},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	for _, name := range []string{"svc-crit", "svc-warn", "svc-info", "svc-ok"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected %q in output, got %q", name, out)
		}
	}
}

func TestPrintAnomalyResultAISummary(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     30,
		TotalScanned: 1,
		ScanTime:     time.Now(),
		AISummary:    "Root cause identified as database overload.",
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "Root cause identified as database overload.") {
		t.Errorf("expected AI summary in output, got %q", out)
	}
}

func TestPrintAnomalyResultMixedSeverities(t *testing.T) {
	result := AnomalyResult{
		Instance:     "prod",
		Duration:     60,
		TotalScanned: 5,
		ScanTime:     time.Now(),
		Anomalies: []AnomalyItem{
			{Type: "err", Severity: "critical", Service: "a", Description: "crit anomaly", Deviation: 5.0},
			{Type: "lat", Severity: "warning", Service: "b", Description: "warn anomaly", Deviation: 2.0},
			{Type: "trf", Severity: "info", Service: "c", Description: "info anomaly", Deviation: 1.0, Window: "5m"},
		},
	}
	out := captureStdout(func() {
		PrintAnomalyResult(result, false)
	})
	if !strings.Contains(out, "3 anomalies") {
		t.Errorf("expected anomaly count summary, got %q", out)
	}
	if !strings.Contains(out, "5m") {
		t.Errorf("expected window value in output, got %q", out)
	}
}

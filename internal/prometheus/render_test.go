package prometheus

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatRulesEmpty(t *testing.T) {
	result := FormatRules(nil, "")
	if result != "No rule groups found." {
		t.Errorf("expected no groups message, got %q", result)
	}

	result = FormatRules(&RulesData{}, "")
	if result != "No rule groups found." {
		t.Errorf("expected no groups message, got %q", result)
	}
}

func TestFormatRulesWithData(t *testing.T) {
	data := &RulesData{
		Groups: []RuleGroup{
			{
				Name:     "test-rules",
				File:     "/etc/rules.yml",
				Interval: 60,
				Rules: []Rule{
					{
						Name:     "HighCPU",
						Query:    `avg(rate(cpu_usage[5m])) > 0.9`,
						Type:     "alerting",
						State:    "firing",
						Health:   "ok",
						Duration: 300,
						Labels:   map[string]string{"severity": "critical"},
						Alerts: []RuleAlert{
							{State: "firing", Labels: map[string]string{"instance": "web-1"}},
							{State: "pending", Labels: map[string]string{"instance": "web-2"}},
						},
					},
					{
						Name:   "cpu_rate:5m",
						Query:  `rate(cpu_usage[5m])`,
						Type:   "recording",
						Health: "ok",
					},
				},
			},
		},
	}

	result := FormatRules(data, "")
	if !strings.Contains(result, "test-rules") {
		t.Error("expected group name in output")
	}
	if !strings.Contains(result, "HighCPU") {
		t.Error("expected rule name in output")
	}
	if !strings.Contains(result, "FIRING") {
		t.Error("expected FIRING state in output")
	}
	if !strings.Contains(result, "1 firing") {
		t.Error("expected firing count in output")
	}
	if !strings.Contains(result, "1 pending") {
		t.Error("expected pending count in output")
	}
	if !strings.Contains(result, "cpu_rate:5m") {
		t.Error("expected recording rule in output")
	}
}

func TestFormatRulesFilterType(t *testing.T) {
	data := &RulesData{
		Groups: []RuleGroup{
			{
				Name: "mixed",
				Rules: []Rule{
					{Name: "AlertRule", Type: "alerting"},
					{Name: "RecordRule", Type: "recording"},
				},
			},
		},
	}

	result := FormatRules(data, "alerting")
	if !strings.Contains(result, "AlertRule") {
		t.Error("expected alert rule in filtered output")
	}
	if strings.Contains(result, "RecordRule") {
		t.Error("did not expect recording rule in filtered output")
	}
}

func TestFormatRulesWithError(t *testing.T) {
	data := &RulesData{
		Groups: []RuleGroup{
			{
				Name: "error-group",
				Rules: []Rule{
					{
						Name:      "BrokenRule",
						Type:      "alerting",
						Health:    "err",
						LastError: "parse error at line 1",
						Query:     "invalid(",
					},
				},
			},
		},
	}

	result := FormatRules(data, "")
	if !strings.Contains(result, "parse error") {
		t.Error("expected error message in output")
	}
	if !strings.Contains(result, "err") {
		t.Error("expected health status in output")
	}
}

func TestFormatRulesLongQuery(t *testing.T) {
	longQuery := strings.Repeat("a", 200)
	data := &RulesData{
		Groups: []RuleGroup{
			{
				Name:  "long",
				Rules: []Rule{{Name: "Long", Query: longQuery, Type: "recording"}},
			},
		},
	}

	result := FormatRules(data, "")
	if !strings.Contains(result, "...") {
		t.Error("expected truncated query")
	}
}

func TestFormatTargetsEmpty(t *testing.T) {
	result := FormatTargets(nil)
	if result != "No active targets found." {
		t.Errorf("expected no targets message, got %q", result)
	}

	result = FormatTargets(&TargetsData{})
	if result != "No active targets found." {
		t.Errorf("expected no targets message, got %q", result)
	}
}

func TestFormatTargetsWithData(t *testing.T) {
	data := &TargetsData{
		ActiveTargets: []Target{
			{
				Labels:        map[string]string{"instance": "localhost:9090"},
				ScrapePool:    "prometheus",
				ScrapeURL:     "http://localhost:9090/metrics",
				Health:        "up",
				LastScrape:    time.Now().Add(-10 * time.Second),
				LastScrapeDur: 0.012,
			},
			{
				Labels:        map[string]string{"instance": "node-1:9100"},
				ScrapePool:    "node",
				ScrapeURL:     "http://node-1:9100/metrics",
				Health:        "down",
				LastError:     "connection refused",
				LastScrape:    time.Now().Add(-30 * time.Second),
				LastScrapeDur: 0.001,
			},
		},
	}

	result := FormatTargets(data)
	if !strings.Contains(result, "2 total") {
		t.Error("expected total count")
	}
	if !strings.Contains(result, "1 up") {
		t.Error("expected up count")
	}
	if !strings.Contains(result, "1 down") {
		t.Error("expected down count")
	}
	if !strings.Contains(result, "prometheus") {
		t.Error("expected pool name")
	}
	if !strings.Contains(result, "connection refused") {
		t.Error("expected error message")
	}
}

func TestFormatTargetsNoInstanceLabel(t *testing.T) {
	data := &TargetsData{
		ActiveTargets: []Target{
			{
				Labels:     map[string]string{},
				ScrapePool: "test",
				ScrapeURL:  "http://test:8080/metrics",
				Health:     "up",
			},
		},
	}

	result := FormatTargets(data)
	if !strings.Contains(result, "http://test:8080/metrics") {
		t.Error("expected scrape URL as fallback when no instance label")
	}
}

func TestFormatAlertsEmpty(t *testing.T) {
	result := FormatAlerts(nil)
	if result != "No active alerts." {
		t.Errorf("expected no alerts message, got %q", result)
	}
	result = FormatAlerts(&AlertsData{})
	if result != "No active alerts." {
		t.Errorf("expected no alerts message, got %q", result)
	}
}

func TestFormatAlertsWithData(t *testing.T) {
	data := &AlertsData{
		Alerts: []PromAlert{
			{
				Labels:      map[string]string{"alertname": "HighMem", "severity": "warning"},
				Annotations: map[string]string{"summary": "Memory usage is high"},
				State:       "firing",
				ActiveAt:    time.Now().Add(-5 * time.Minute),
			},
			{
				Labels: map[string]string{"alertname": "DiskFull"},
				State:  "pending",
			},
		},
	}

	result := FormatAlerts(data)
	if !strings.Contains(result, "1 firing") {
		t.Error("expected firing count")
	}
	if !strings.Contains(result, "1 pending") {
		t.Error("expected pending count")
	}
	if !strings.Contains(result, "HighMem") {
		t.Error("expected alert name")
	}
	if !strings.Contains(result, "Memory usage is high") {
		t.Error("expected summary annotation")
	}
}

func TestFormatAlertsSorting(t *testing.T) {
	data := &AlertsData{
		Alerts: []PromAlert{
			{Labels: map[string]string{"alertname": "ZAlert"}, State: "pending"},
			{Labels: map[string]string{"alertname": "AAlert"}, State: "firing"},
		},
	}

	result := FormatAlerts(data)
	// Firing should come before pending
	firingIdx := strings.Index(result, "AAlert")
	pendingIdx := strings.Index(result, "ZAlert")
	if firingIdx > pendingIdx {
		t.Error("expected firing alerts before pending")
	}
}

func TestFormatQueryVector(t *testing.T) {
	samples := []VectorSample{
		{
			Metric: map[string]string{"job": "web"},
			Value:  [2]interface{}{float64(time.Now().Unix()), "42"},
		},
	}
	raw, _ := json.Marshal(samples)
	result := FormatQuery(&QueryResult{ResultType: "vector", Result: raw})
	if !strings.Contains(result, "42") {
		t.Error("expected value in output")
	}
	if !strings.Contains(result, "1 samples") {
		t.Error("expected sample count")
	}
}

func TestFormatQueryEmptyVector(t *testing.T) {
	raw, _ := json.Marshal([]VectorSample{})
	result := FormatQuery(&QueryResult{ResultType: "vector", Result: raw})
	if result != "Empty result." {
		t.Errorf("expected empty result message, got %q", result)
	}
}

func TestFormatQueryMatrix(t *testing.T) {
	series := []MatrixSeries{
		{
			Metric: map[string]string{"job": "api"},
			Values: [][2]interface{}{
				{float64(time.Now().Unix()), "100"},
				{float64(time.Now().Unix()), "200"},
			},
		},
	}
	raw, _ := json.Marshal(series)
	result := FormatQuery(&QueryResult{ResultType: "matrix", Result: raw})
	if !strings.Contains(result, "1 series") {
		t.Error("expected series count")
	}
	if !strings.Contains(result, "100") {
		t.Error("expected value in output")
	}
}

func TestFormatQueryMatrixManyValues(t *testing.T) {
	values := make([][2]interface{}, 10)
	for i := range values {
		values[i] = [2]interface{}{float64(time.Now().Add(time.Duration(-i) * time.Minute).Unix()), "100"}
	}
	series := []MatrixSeries{{Metric: map[string]string{"job": "api"}, Values: values}}
	raw, _ := json.Marshal(series)
	result := FormatQuery(&QueryResult{ResultType: "matrix", Result: raw})
	if !strings.Contains(result, "earlier samples") {
		t.Error("expected truncation message for many values")
	}
}

func TestFormatQueryScalar(t *testing.T) {
	raw, _ := json.Marshal([2]interface{}{float64(time.Now().Unix()), "2"})
	result := FormatQuery(&QueryResult{ResultType: "scalar", Result: raw})
	if !strings.Contains(result, "Scalar") {
		t.Error("expected scalar label")
	}
}

func TestFormatQueryString(t *testing.T) {
	raw, _ := json.Marshal([2]interface{}{float64(time.Now().Unix()), "hello"})
	result := FormatQuery(&QueryResult{ResultType: "string", Result: raw})
	if !strings.Contains(result, "String") {
		t.Error("expected string label")
	}
}

func TestFormatQueryUnknown(t *testing.T) {
	result := FormatQuery(&QueryResult{ResultType: "weird", Result: json.RawMessage(`{}`)})
	if !strings.Contains(result, "Unknown result type") {
		t.Error("expected unknown type message")
	}
}

func TestFormatQueryNil(t *testing.T) {
	result := FormatQuery(nil)
	if result != "No result." {
		t.Errorf("expected no result message, got %q", result)
	}
}

func TestFormatSummary(t *testing.T) {
	s := Summary{
		TotalRuleGroups:  3,
		TotalAlertRules:  10,
		TotalRecordRules: 5,
		FiringAlerts:     2,
		PendingAlerts:    1,
		ActiveTargets:    20,
		HealthyTargets:   18,
		UnhealthyTargets: 2,
	}

	result := FormatSummary(s)
	if !strings.Contains(result, "3 groups") {
		t.Error("expected group count")
	}
	if !strings.Contains(result, "10 alert rules") {
		t.Error("expected alert rules count")
	}
	if !strings.Contains(result, "2 firing") {
		t.Error("expected firing count")
	}
	if !strings.Contains(result, "20 active") {
		t.Error("expected target count")
	}
}

func TestFormatSummaryHealthy(t *testing.T) {
	s := Summary{
		FiringAlerts:     0,
		PendingAlerts:    0,
		UnhealthyTargets: 0,
		HealthyTargets:   5,
		ActiveTargets:    5,
	}
	result := FormatSummary(s)
	if !strings.Contains(result, "0 firing") {
		t.Error("expected zero firing")
	}
}

func TestFormatStatus(t *testing.T) {
	runtime := &RuntimeInfo{
		StorageRetention:    "15d",
		GoroutineCount:      42,
		GOMAXPROCS:          8,
		StartTime:           "2026-03-27",
		ReloadConfigSuccess: true,
	}
	build := &BuildInfo{
		Version:   "2.53.0",
		Branch:    "HEAD",
		GoVersion: "go1.23.0",
	}

	result := FormatStatus(runtime, build, true)
	if !strings.Contains(result, "Healthy") {
		t.Error("expected healthy status")
	}
	if !strings.Contains(result, "2.53.0") {
		t.Error("expected version")
	}
	if !strings.Contains(result, "15d") {
		t.Error("expected retention")
	}
}

func TestFormatStatusUnhealthy(t *testing.T) {
	result := FormatStatus(nil, nil, false)
	if !strings.Contains(result, "Unhealthy") {
		t.Error("expected unhealthy status")
	}
}

func TestFormatStatusConfigReloadFailed(t *testing.T) {
	runtime := &RuntimeInfo{ReloadConfigSuccess: false}
	result := FormatStatus(runtime, nil, true)
	if !strings.Contains(result, "config reload failed") {
		t.Error("expected config reload warning")
	}
}

func TestFormatJSON(t *testing.T) {
	data := map[string]int{"count": 42}
	result := FormatJSON(data)
	if !strings.Contains(result, `"count": 42`) {
		t.Errorf("expected formatted JSON, got %s", result)
	}
}

func TestFormatLabelsEmpty(t *testing.T) {
	result := formatLabels(nil)
	if result != "{}" {
		t.Errorf("expected empty labels, got %q", result)
	}
}

func TestFormatLabelsSorted(t *testing.T) {
	result := formatLabels(map[string]string{"z": "1", "a": "2"})
	aIdx := strings.Index(result, "a=")
	zIdx := strings.Index(result, "z=")
	if aIdx > zIdx {
		t.Error("expected labels sorted alphabetically")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{3 * time.Hour, "3h"},
		{25 * time.Hour, "1d1h"},
		{48 * time.Hour, "2d"},
		{-5 * time.Minute, "5m"}, // negative handled
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestStateIcon(t *testing.T) {
	if stateIcon("firing") != "🔴" {
		t.Error("wrong icon for firing")
	}
	if stateIcon("pending") != "🟡" {
		t.Error("wrong icon for pending")
	}
	if stateIcon("inactive") != "🟢" {
		t.Error("wrong icon for inactive")
	}
	if stateIcon("unknown") != "⚪" {
		t.Error("wrong icon for unknown")
	}
}

func TestStateLabel(t *testing.T) {
	result := stateLabel("firing")
	if !strings.Contains(result, "FIRING") {
		t.Error("expected FIRING label")
	}
	result = stateLabel("pending")
	if !strings.Contains(result, "PENDING") {
		t.Error("expected PENDING label")
	}
	result = stateLabel("inactive")
	if result != "" {
		t.Error("expected empty label for inactive")
	}
}

package alert

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock
// ──────────────────────────────────────────────

type mockSignozClient struct {
	listServicesFunc func(ctx context.Context) ([]types.Service, error)
	queryLogsFunc    func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
}

func (m *mockSignozClient) Health(ctx context.Context) (bool, time.Duration, error) {
	return true, 0, nil
}

func (m *mockSignozClient) ListServices(ctx context.Context) ([]types.Service, error) {
	if m.listServicesFunc != nil {
		return m.listServicesFunc(ctx)
	}
	return nil, nil
}

func (m *mockSignozClient) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if m.queryLogsFunc != nil {
		return m.queryLogsFunc(ctx, service, durationMinutes, limit, severityFilter)
	}
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

// ──────────────────────────────────────────────
// Rule Tests
// ──────────────────────────────────────────────

func TestRuleIsEnabled(t *testing.T) {
	// Default (nil) = enabled
	r := Rule{Name: "test"}
	if !r.IsEnabled() {
		t.Error("nil Enabled should default to true")
	}

	// Explicitly enabled
	enabled := true
	r.Enabled = &enabled
	if !r.IsEnabled() {
		t.Error("explicit true should be enabled")
	}

	// Explicitly disabled
	disabled := false
	r.Enabled = &disabled
	if r.IsEnabled() {
		t.Error("explicit false should be disabled")
	}
}

func TestDurationMinutes(t *testing.T) {
	tests := []struct {
		duration string
		expected int
	}{
		{"5m", 5},
		{"15m", 15},
		{"1h", 60},
		{"2h", 120},
		{"", 5},   // default
		{"0m", 5}, // fallback
	}
	for _, tt := range tests {
		r := Rule{Duration: tt.duration}
		got := r.DurationMinutes()
		if got != tt.expected {
			t.Errorf("DurationMinutes(%q) = %d, want %d", tt.duration, got, tt.expected)
		}
	}
}

// ──────────────────────────────────────────────
// Evaluate Tests
// ──────────────────────────────────────────────

func TestEvaluate(t *testing.T) {
	tests := []struct {
		value     float64
		operator  string
		threshold float64
		expected  bool
	}{
		{10, "gt", 5, true},
		{5, "gt", 5, false},
		{5, "gte", 5, true},
		{4, "gte", 5, false},
		{3, "lt", 5, true},
		{5, "lt", 5, false},
		{5, "lte", 5, true},
		{6, "lte", 5, false},
		{5, "eq", 5, true},
		{6, "eq", 5, false},
		{10, ">", 5, true},
		{10, "unknown", 5, true}, // default to gt
	}
	for _, tt := range tests {
		got := evaluate(tt.value, tt.operator, tt.threshold)
		if got != tt.expected {
			t.Errorf("evaluate(%v, %q, %v) = %v, want %v", tt.value, tt.operator, tt.threshold, got, tt.expected)
		}
	}
}

// ──────────────────────────────────────────────
// Checker Tests
// ──────────────────────────────────────────────

func TestCheckErrorRateCritical(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 20},
			}, nil
		},
	}

	checker := NewChecker(mock, "test-instance")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "high-errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, err := checker.CheckAll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityCritical {
		t.Errorf("expected critical, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckErrorRateWarning(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 8},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Severity != SeverityWarning {
		t.Errorf("expected warning, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckErrorRateOK(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 1},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Severity != SeverityOK {
		t.Errorf("expected ok, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckErrorRateMissingService(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 1},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Service: "missing-svc", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityWarning {
		t.Errorf("expected warning for missing service, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckLogErrors(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{Logs: make([]types.LogEntry, 60)}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "logs", Type: "log_errors", Operator: "gt", Warning: 10, Critical: 50, Duration: "15m"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityCritical {
		t.Errorf("expected critical for 60 > 50 threshold, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckServiceDownNoServices(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "down", Type: "service_down", Operator: "lt", Warning: 1, Critical: 1},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityCritical {
		t.Errorf("expected critical when no services, got %v", rpt.Results[0].Severity)
	}
}

func TestCheckServiceDownZeroCalls(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 0},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "down", Type: "service_down", Operator: "lt", Warning: 1, Critical: 1},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	found := false
	for _, r := range rpt.Results {
		if r.Service == "api" && r.Severity == SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Error("expected warning for service with 0 calls")
	}
}

func TestCheckAllMixed(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 20},
				{Name: "web", NumCalls: 100, NumErrors: 1},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Summary.Total != 2 {
		t.Errorf("expected 2 total, got %d", rpt.Summary.Total)
	}
	if rpt.Summary.Critical != 1 {
		t.Errorf("expected 1 critical, got %d", rpt.Summary.Critical)
	}
	if rpt.Summary.OK != 1 {
		t.Errorf("expected 1 ok, got %d", rpt.Summary.OK)
	}
}

func TestCheckDisabledRule(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100, NumErrors: 50, ErrorRate: 50.0}}, nil
		},
	}

	disabled := false
	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0, Enabled: &disabled},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if len(rpt.Results) != 0 {
		t.Errorf("disabled rule should produce no results, got %d", len(rpt.Results))
	}
}

// ──────────────────────────────────────────────
// Report Tests
// ──────────────────────────────────────────────

func TestReportExitCode(t *testing.T) {
	tests := []struct {
		summary  Summary
		expected int
	}{
		{Summary{Critical: 1}, 2},
		{Summary{Warnings: 1}, 1},
		{Summary{OK: 5}, 0},
		{Summary{Critical: 1, Warnings: 2}, 2},
	}
	for _, tt := range tests {
		r := Report{Summary: tt.summary}
		if got := r.ExitCode(); got != tt.expected {
			t.Errorf("ExitCode(%+v) = %d, want %d", tt.summary, got, tt.expected)
		}
	}
}

func TestFormatText(t *testing.T) {
	rpt := &Report{
		Instance:  "prod",
		Results: []CheckResult{
			{Rule: "r1", Service: "api", Severity: SeverityCritical, Status: "critical", Message: "bad"},
			{Rule: "r2", Service: "web", Severity: SeverityOK, Status: "ok", Message: "good"},
		},
		Summary: Summary{Total: 2, OK: 1, Critical: 1},
	}

	out := FormatText(rpt)
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestFormatJSON(t *testing.T) {
	rpt := &Report{
		Instance: "prod",
		Results: []CheckResult{
			{Rule: "r1", Severity: SeverityOK, Status: "ok"},
		},
		Summary: Summary{Total: 1, OK: 1},
	}

	out, err := FormatJSON(rpt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["instance"] != "prod" {
		t.Errorf("expected instance=prod, got %v", parsed["instance"])
	}
}

func TestSeverityString(t *testing.T) {
	if SeverityOK.String() != "ok" {
		t.Errorf("expected ok, got %s", SeverityOK.String())
	}
	if SeverityWarning.String() != "warning" {
		t.Errorf("expected warning, got %s", SeverityWarning.String())
	}
	if SeverityCritical.String() != "critical" {
		t.Errorf("expected critical, got %s", SeverityCritical.String())
	}
}

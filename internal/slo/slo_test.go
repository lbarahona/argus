package slo

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
	queryTracesFunc  func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error)
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
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	if m.queryTracesFunc != nil {
		return m.queryTracesFunc(ctx, service, durationMinutes, limit)
	}
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

// ──────────────────────────────────────────────
// SLO Config Tests
// ──────────────────────────────────────────────

func TestSLOIsEnabled(t *testing.T) {
	s := SLO{Name: "test"}
	if !s.IsEnabled() {
		t.Error("nil Enabled should default to true")
	}

	enabled := true
	s.Enabled = &enabled
	if !s.IsEnabled() {
		t.Error("explicit true should be enabled")
	}

	disabled := false
	s.Enabled = &disabled
	if s.IsEnabled() {
		t.Error("explicit false should be disabled")
	}
}

func TestWindowMinutes(t *testing.T) {
	tests := []struct {
		window   string
		expected int
	}{
		{"1h", 60},
		{"24h", 1440},
		{"7d", 10080},
		{"30m", 30},
		{"", 1440},        // default
		{"invalid", 1440}, // fallback
	}
	for _, tt := range tests {
		s := SLO{Window: tt.window}
		got := s.WindowMinutes()
		if got != tt.expected {
			t.Errorf("WindowMinutes(%q) = %d, want %d", tt.window, got, tt.expected)
		}
	}
}

// ──────────────────────────────────────────────
// ClassifyStatus Tests
// ──────────────────────────────────────────────

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		consumed float64
		expected string
	}{
		{0, "ok"},
		{49.9, "ok"},
		{50, "warning"},
		{79.9, "warning"},
		{80, "critical"},
		{99.9, "critical"},
		{100, "exhausted"},
		{150, "exhausted"},
	}
	for _, tt := range tests {
		got := classifyStatus(tt.consumed)
		if got != tt.expected {
			t.Errorf("classifyStatus(%v) = %q, want %q", tt.consumed, got, tt.expected)
		}
	}
}

// ──────────────────────────────────────────────
// Checker Tests
// ──────────────────────────────────────────────

func TestCheckAvailabilityOK(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 2},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "avail", Type: "availability", Target: 99.9, Window: "24h"},
		},
	}

	rpt, err := checker.CheckAll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Status != "ok" {
		t.Errorf("expected ok, got %s", rpt.Results[0].Status)
	}
	if rpt.Results[0].Current < 99.9 {
		t.Errorf("expected current >= 99.9, got %.3f", rpt.Results[0].Current)
	}
}

func TestCheckAvailabilityCritical(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 50}, // 5% error = 95% avail
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "avail", Type: "availability", Target: 99.9, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Status != "exhausted" {
		t.Errorf("expected exhausted (budget way over), got %s", rpt.Results[0].Status)
	}
}

func TestCheckAvailabilityNoCalls(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 0, NumErrors: 0},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "avail", Type: "availability", Target: 99.9, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Status != "ok" {
		t.Errorf("expected ok for zero calls, got %s", rpt.Results[0].Status)
	}
	if rpt.Results[0].Current != 100.0 {
		t.Errorf("expected 100%% for zero calls, got %.3f", rpt.Results[0].Current)
	}
}

func TestCheckAvailabilityAllServices(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 1},
				{Name: "web", NumCalls: 1000, NumErrors: 1},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "avail", Type: "availability", Service: "", Target: 99.9, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	// Combined: 2000 calls, 2 errors = 0.1% error = 99.9% avail
	if rpt.Results[0].TotalRequests != 2000 {
		t.Errorf("expected 2000 total requests, got %d", rpt.Results[0].TotalRequests)
	}
}

func TestCheckLatencyOK(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			traces := make([]types.TraceEntry, 100)
			for i := range traces {
				traces[i] = types.TraceEntry{DurationNano: 100_000_000} // 100ms
			}
			return &types.QueryResult{Traces: traces}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "latency", Type: "latency", Target: 99.0, Threshold: 500, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Status != "ok" {
		t.Errorf("expected ok, got %s", rpt.Results[0].Status)
	}
}

func TestCheckLatencyWarning(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			traces := make([]types.TraceEntry, 100)
			// 99 fast, 1 slow -> 99% under threshold, but budget is 1%, consumed = 100%*1/1 = 100%
			// Actually: target 99%, budget = 1%. violation = 1%, consumed = 100%
			for i := range traces {
				if i < 99 {
					traces[i] = types.TraceEntry{DurationNano: 100_000_000} // 100ms
				} else {
					traces[i] = types.TraceEntry{DurationNano: 1_000_000_000} // 1000ms > 500ms threshold
				}
			}
			return &types.QueryResult{Traces: traces}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "latency", Type: "latency", Target: 99.0, Threshold: 500, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	// 1 out of 100 > threshold = 1% violation, budget = 1%, consumed = 100% => "exhausted"
	if rpt.Results[0].Status != "exhausted" {
		t.Errorf("expected exhausted, got %s (budget consumed: %.1f%%)", rpt.Results[0].Status, rpt.Results[0].BudgetConsumed)
	}
}

func TestCheckLatencyNoTraces(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			return &types.QueryResult{}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "latency", Type: "latency", Target: 99.0, Threshold: 500, Window: "24h"},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if rpt.Results[0].Status != "ok" {
		t.Errorf("expected ok for no traces, got %s", rpt.Results[0].Status)
	}
}

func TestCheckDisabledSLO(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
	}

	disabled := false
	checker := NewChecker(mock, "test")
	cfg := &SLOConfig{
		SLOs: []SLO{
			{Name: "avail", Type: "availability", Target: 99.9, Enabled: &disabled},
		},
	}

	rpt, _ := checker.CheckAll(context.Background(), cfg)
	if len(rpt.Results) != 0 {
		t.Errorf("disabled SLO should produce no results, got %d", len(rpt.Results))
	}
}

// ──────────────────────────────────────────────
// Report Tests
// ──────────────────────────────────────────────

func TestReportExitCode(t *testing.T) {
	tests := []struct {
		statuses []string
		expected int
	}{
		{[]string{"ok", "ok"}, 0},
		{[]string{"ok", "warning"}, 1},
		{[]string{"ok", "critical"}, 2},
		{[]string{"ok", "exhausted"}, 2},
		{[]string{}, 0},
	}
	for _, tt := range tests {
		rpt := &Report{}
		for _, s := range tt.statuses {
			rpt.Results = append(rpt.Results, Result{Status: s})
		}
		if got := rpt.ExitCode(); got != tt.expected {
			t.Errorf("ExitCode(%v) = %d, want %d", tt.statuses, got, tt.expected)
		}
	}
}

func TestFormatJSON(t *testing.T) {
	rpt := &Report{
		Instance: "prod",
		Results: []Result{
			{SLO: SLO{Name: "avail"}, Status: "ok", Current: 99.95},
		},
	}

	out, err := FormatJSON(rpt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestFormatText(t *testing.T) {
	rpt := &Report{
		Instance:  "prod",
		Timestamp: "2026-02-23T00:00:00Z",
		Results: []Result{
			{SLO: SLO{Name: "avail", Description: "test slo"}, Status: "ok", Current: 99.95, Target: 99.9, BudgetRemain: 50, BurnRate: 0.5},
		},
	}

	out := FormatText(rpt)
	if out == "" {
		t.Error("expected non-empty output")
	}
}

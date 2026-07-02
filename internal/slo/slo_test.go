package slo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	// A broken/empty trace pipeline must not report a fake-healthy latency
	// SLO — it must surface as "no_data", not "ok".
	if rpt.Results[0].Status != "no_data" {
		t.Errorf("expected no_data for no traces, got %s", rpt.Results[0].Status)
	}
}

func TestCheckLatencyQueryFailureIsNoData(t *testing.T) {
	mock := &mockSignozClient{
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			return nil, fmt.Errorf("boom")
		},
	}
	c := NewChecker(mock, "test")
	s := SLO{Name: "lat", Type: "latency", Service: "api", Target: 99.0, Threshold: 500, Window: "24h"}

	result := c.checkLatency(context.Background(), s, nil)

	if result.Status != "no_data" {
		t.Errorf("failed trace query must be no_data, not fake-ok; got %q", result.Status)
	}
}

func TestCheckLatencyScalesConsumptionByWindow(t *testing.T) {
	// 1000 traces, 20 over threshold → 2% violation on a 1% budget = 2x burn.
	traces := make([]types.TraceEntry, 1000)
	for i := range traces {
		d := int64(100 * 1e6) // 100ms
		if i < 20 {
			d = int64(900 * 1e6) // 900ms, over the 500ms threshold
		}
		traces[i] = types.TraceEntry{DurationNano: d}
	}
	mock := &mockSignozClient{
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			return &types.QueryResult{Traces: traces}, nil
		},
	}
	c := NewChecker(mock, "test")
	s := SLO{Name: "lat", Type: "latency", Service: "api", Target: 99.0, Threshold: 500, Window: "30d"}

	result := c.checkLatency(context.Background(), s, nil)

	// Observed window is min(1440, 43200) = 1440 of 43200 → fraction 1/30.
	// BudgetConsumed = 2.0 * (1/30) * 100 ≈ 6.7, so status stays ok (burn 2x < 6x).
	if result.BurnRate < 1.9 || result.BurnRate > 2.1 {
		t.Errorf("burn rate = %.2f, want ~2.0", result.BurnRate)
	}
	if result.BudgetConsumed > 10 {
		t.Errorf("consumed = %.1f, must be scaled by observed/window fraction", result.BudgetConsumed)
	}
	if result.Status != "ok" {
		t.Errorf("2x burn is below the 6x escalation bar; got %q", result.Status)
	}
}

func TestStatusPriorityNoData(t *testing.T) {
	if got := statusPriority("no_data"); got != 0 {
		t.Errorf("statusPriority(no_data) = %d, want 0", got)
	}
}

func TestFormatTextRendersNoDataDistinctly(t *testing.T) {
	rpt := &Report{
		Instance:  "prod",
		Timestamp: "2026-02-23T00:00:00Z",
		Results: []Result{
			{SLO: SLO{Name: "lat", Description: "test slo"}, Status: "no_data", Current: 0, Target: 99.0, BudgetRemain: 1, BurnRate: 0},
		},
	}

	out := FormatText(rpt)
	if !strings.Contains(out, "no_data") {
		t.Errorf("expected summary to mention no_data, got: %s", out)
	}
	if strings.Contains(out, statusIcon("ok")) {
		t.Errorf("no_data must not render with the ok icon: %s", out)
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

func TestCheckAvailabilitySubUnitBurnIsOK(t *testing.T) {
	c := &Checker{}
	s := SLO{Name: "avail", Type: "availability", Service: "api", Target: 99.9, Window: "30d"}
	// 0.09% error rate over the observed 6h = 0.9x burn.
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}

	result := c.checkAvailability(s, services)

	if result.Status != "ok" {
		t.Errorf("0.9x burn on a 30d window should be ok, got %q (consumed %.2f%%)",
			result.Status, result.BudgetConsumed)
	}
	if result.BurnRate < 0.85 || result.BurnRate > 0.95 {
		t.Errorf("expected burn rate ~0.9, got %.2f", result.BurnRate)
	}
}

func TestCheckAvailabilityBurnRateEscalation(t *testing.T) {
	c := &Checker{}
	// 0.7% error rate on a 99.9% / 30d SLO = 7x burn: consumed is tiny
	// (~5.8%) but the budget dies in ~4 days — must not report "ok".
	s := SLO{Name: "avail", Type: "availability", Service: "api", Target: 99.9, Window: "30d"}
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 700}}
	result := c.checkAvailability(s, services)
	if result.Status != "warning" {
		t.Errorf("7x burn should escalate to warning, got %q (consumed %.2f%%)", result.Status, result.BudgetConsumed)
	}

	// 2% error rate = 20x burn: page-level.
	services = []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 2000}}
	result = c.checkAvailability(s, services)
	if result.Status != "critical" {
		t.Errorf("20x burn should escalate to critical, got %q", result.Status)
	}

	// 0.9x burn stays ok (regression guard for Tier 1 behavior).
	services = []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}
	result = c.checkAvailability(s, services)
	if result.Status != "ok" {
		t.Errorf("0.9x burn should stay ok, got %q", result.Status)
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
		if got := rpt.ExitCode(false); got != tt.expected {
			t.Errorf("ExitCode(%v) = %d, want %d", tt.statuses, got, tt.expected)
		}
	}
}

// TestReportExitCodeFailOnNoData covers the --fail-on-no-data CI gate: a
// report where nothing is worse than no_data should stay clean unless the
// flag is set, and no_data must never downgrade a real critical finding.
func TestReportExitCodeFailOnNoData(t *testing.T) {
	tests := []struct {
		name         string
		statuses     []string
		failOnNoData bool
		expected     int
	}{
		{"all no_data, flag off", []string{"no_data", "no_data"}, false, 0},
		{"all no_data, flag on", []string{"no_data", "no_data"}, true, 1},
		{"ok + no_data, flag on", []string{"ok", "no_data"}, true, 1},
		{"no_data + critical, flag off", []string{"no_data", "critical"}, false, 2},
		{"no_data + critical, flag on", []string{"no_data", "critical"}, true, 2},
		{"no_data + warning, flag on", []string{"no_data", "warning"}, true, 1},
		{"no data at all, flag on", []string{"ok", "ok"}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpt := &Report{}
			for _, s := range tt.statuses {
				rpt.Results = append(rpt.Results, Result{Status: s})
			}
			if got := rpt.ExitCode(tt.failOnNoData); got != tt.expected {
				t.Errorf("ExitCode(%v, failOnNoData=%v) = %d, want %d", tt.statuses, tt.failOnNoData, got, tt.expected)
			}
		})
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

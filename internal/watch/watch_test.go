package watch

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock
// ──────────────────────────────────────────────

type mockSignozClient struct {
	listServicesFunc func(ctx context.Context) ([]types.Service, error)
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
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

// ──────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────

func TestDefaultThresholds(t *testing.T) {
	th := DefaultThresholds()
	if th.ErrorRateWarning != 5.0 {
		t.Errorf("expected ErrorRateWarning=5, got %f", th.ErrorRateWarning)
	}
	if th.ErrorRateCritical != 15.0 {
		t.Errorf("expected ErrorRateCritical=15, got %f", th.ErrorRateCritical)
	}
	if th.P99Warning != 2000 {
		t.Errorf("expected P99Warning=2000, got %f", th.P99Warning)
	}
	if th.P99Critical != 5000 {
		t.Errorf("expected P99Critical=5000, got %f", th.P99Critical)
	}
	if th.ErrorSpike != 3.0 {
		t.Errorf("expected ErrorSpike=3, got %f", th.ErrorSpike)
	}
	if !th.NewErrors {
		t.Error("expected NewErrors=true")
	}
}

func TestBuildSnapshots(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	services := []types.Service{
		{Name: "api", NumCalls: 1000, NumErrors: 50},
		{Name: "web", NumCalls: 500, NumErrors: 0},
	}

	snapshots := w.buildSnapshots(services)
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}

	// Should be sorted by error rate (api first)
	if snapshots[0].Name != "api" {
		t.Errorf("expected api first (higher error rate), got %s", snapshots[0].Name)
	}
	if snapshots[0].ErrorRate != 5.0 {
		t.Errorf("expected 5%% error rate, got %.1f", snapshots[0].ErrorRate)
	}
}

func TestAnalyzeErrorRateCritical(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, Errors: 20, ErrorRate: 20.0},
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Level == AlertCritical && a.Service == "api" {
			found = true
		}
	}
	if !found {
		t.Error("expected critical alert for 20% error rate")
	}
}

func TestAnalyzeErrorRateWarning(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, Errors: 8, ErrorRate: 8.0},
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Level == AlertWarning && a.Service == "api" {
			found = true
		}
	}
	if !found {
		t.Error("expected warning alert for 8% error rate")
	}
}

func TestAnalyzeP99Critical(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, P99: 6000},
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Level == AlertCritical && a.Service == "api" {
			found = true
		}
	}
	if !found {
		t.Error("expected critical alert for P99=6000ms")
	}
}

func TestAnalyzeP99Warning(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, P99: 3000},
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Level == AlertWarning && a.Service == "api" {
			found = true
		}
	}
	if !found {
		t.Error("expected warning alert for P99=3000ms")
	}
}

func TestAnalyzeErrorSpike(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	// Set up baseline
	w.baseline["api"] = &ServiceSnapshot{Name: "api", Errors: 10}

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, Errors: 40, ErrorRate: 1.0}, // 4x spike
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Service == "api" && a.Value >= 3.0 {
			found = true
		}
	}
	if !found {
		t.Error("expected error spike alert for 4x increase")
	}
}

func TestAnalyzeNewErrors(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	// Baseline with 0 errors
	w.baseline["api"] = &ServiceSnapshot{Name: "api", Errors: 0}

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, Errors: 5, ErrorRate: 1.0},
	}

	alerts := w.analyze(snapshots)
	found := false
	for _, a := range alerts {
		if a.Service == "api" && a.Level == AlertWarning {
			found = true
		}
	}
	if !found {
		t.Error("expected new errors alert")
	}
}

func TestAnalyzeAllClear(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 100, Errors: 1, ErrorRate: 1.0},
	}

	alerts := w.analyze(snapshots)
	if len(alerts) != 0 {
		t.Errorf("expected no alerts for healthy service, got %d", len(alerts))
	}
}

func TestAnalyzeSkipsZeroTraffic(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	snapshots := []ServiceSnapshot{
		{Name: "api", Calls: 0, Errors: 0, ErrorRate: 0},
	}

	alerts := w.analyze(snapshots)
	if len(alerts) != 0 {
		t.Errorf("expected no alerts for zero-traffic service, got %d", len(alerts))
	}
}

func TestUpdateBaselineEMA(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	// First update sets baseline directly
	w.updateBaseline([]ServiceSnapshot{
		{Name: "api", Errors: 100, Calls: 1000, ErrorRate: 10.0},
	})

	if w.baseline["api"].Errors != 100 {
		t.Errorf("first update should set baseline directly, got %f", w.baseline["api"].Errors)
	}

	// Second update uses EMA (alpha=0.3)
	w.updateBaseline([]ServiceSnapshot{
		{Name: "api", Errors: 200, Calls: 2000, ErrorRate: 10.0},
	})

	// EMA: 0.3 * 200 + 0.7 * 100 = 60 + 70 = 130
	expected := 130.0
	if w.baseline["api"].Errors != expected {
		t.Errorf("expected EMA baseline %f, got %f", expected, w.baseline["api"].Errors)
	}
}

func TestEma(t *testing.T) {
	// ema(old=100, new=200, alpha=0.3) = 0.3*200 + 0.7*100 = 130
	got := ema(100, 200, 0.3)
	if got != 130 {
		t.Errorf("ema(100, 200, 0.3) = %f, want 130", got)
	}

	// ema(old=50, new=50, alpha=0.3) = 50 (stable)
	got = ema(50, 50, 0.3)
	if got != 50 {
		t.Errorf("ema(50, 50, 0.3) = %f, want 50", got)
	}
}

func TestSummaryNoAlerts(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})

	summary := w.Summary()
	if summary != "No alerts during watch session." {
		t.Errorf("unexpected summary: %s", summary)
	}
}

func TestSummaryWithAlerts(t *testing.T) {
	mock := &mockSignozClient{}
	w := New(mock, "test", 30*time.Second, DefaultThresholds(), &bytes.Buffer{})
	w.alerts = []Alert{
		{Level: AlertCritical, Service: "api", Message: "high error rate"},
		{Level: AlertWarning, Service: "api", Message: "spike"},
	}

	summary := w.Summary()
	if summary == "No alerts during watch session." {
		t.Error("expected alerts in summary")
	}
}

func TestRunCancellation(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
	}

	var buf bytes.Buffer
	w := New(mock, "test", 100*time.Millisecond, DefaultThresholds(), &buf)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	err := w.Run(ctx)
	if err != nil {
		t.Errorf("expected nil error on context cancellation, got %v", err)
	}
}

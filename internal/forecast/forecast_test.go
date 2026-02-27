package forecast

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services  []types.Service
	logs      map[string]*types.QueryResult
	traces    *types.QueryResult
	metrics   *types.QueryResult
	healthErr error
}

func (m *mockQuerier) Health(ctx context.Context) (bool, time.Duration, error) {
	return true, 10 * time.Millisecond, m.healthErr
}

func (m *mockQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if r, ok := m.logs[severityFilter]; ok {
		if service != "" {
			filtered := &types.QueryResult{}
			for _, l := range r.Logs {
				if l.ServiceName == service {
					filtered.Logs = append(filtered.Logs, l)
				}
			}
			return filtered, nil
		}
		return r, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	if m.traces != nil {
		return m.traces, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	if m.metrics != nil {
		return m.metrics, nil
	}
	return &types.QueryResult{}, nil
}

func assertInDelta(t *testing.T, expected, actual, delta float64, msg string) {
	t.Helper()
	diff := expected - actual
	if diff < 0 {
		diff = -diff
	}
	if diff > delta {
		t.Errorf("%s: expected %f ± %f, got %f", msg, expected, delta, actual)
	}
}

func TestLinearRegression_Rising(t *testing.T) {
	start := time.Now().Add(-60 * time.Minute)
	points := []DataPoint{
		{Timestamp: start, Value: 1},
		{Timestamp: start.Add(10 * time.Minute), Value: 3},
		{Timestamp: start.Add(20 * time.Minute), Value: 5},
		{Timestamp: start.Add(30 * time.Minute), Value: 7},
		{Timestamp: start.Add(40 * time.Minute), Value: 9},
	}

	trend := linearRegression(points, start)
	if trend.Direction != "rising" {
		t.Errorf("expected rising, got %s", trend.Direction)
	}
	assertInDelta(t, 0.2, trend.Slope, 0.01, "slope")
	assertInDelta(t, 1.0, trend.Intercept, 0.01, "intercept")
	assertInDelta(t, 1.0, trend.R2, 0.01, "R²")
}

func TestLinearRegression_Falling(t *testing.T) {
	start := time.Now().Add(-60 * time.Minute)
	points := []DataPoint{
		{Timestamp: start, Value: 10},
		{Timestamp: start.Add(10 * time.Minute), Value: 8},
		{Timestamp: start.Add(20 * time.Minute), Value: 6},
		{Timestamp: start.Add(30 * time.Minute), Value: 4},
	}

	trend := linearRegression(points, start)
	if trend.Direction != "falling" {
		t.Errorf("expected falling, got %s", trend.Direction)
	}
	if trend.Slope >= 0 {
		t.Errorf("expected negative slope, got %f", trend.Slope)
	}
	assertInDelta(t, 1.0, trend.R2, 0.01, "R²")
}

func TestLinearRegression_Stable(t *testing.T) {
	start := time.Now().Add(-60 * time.Minute)
	points := []DataPoint{
		{Timestamp: start, Value: 5},
		{Timestamp: start.Add(10 * time.Minute), Value: 5},
		{Timestamp: start.Add(20 * time.Minute), Value: 5},
	}

	trend := linearRegression(points, start)
	if trend.Direction != "stable" {
		t.Errorf("expected stable, got %s", trend.Direction)
	}
	assertInDelta(t, 0, trend.Slope, 0.01, "slope")
}

func TestLinearRegression_InsufficientPoints(t *testing.T) {
	start := time.Now()
	points := []DataPoint{
		{Timestamp: start, Value: 5},
	}

	trend := linearRegression(points, start)
	if trend.Direction != "stable" {
		t.Errorf("expected stable, got %s", trend.Direction)
	}
}

func TestBucketLogs(t *testing.T) {
	now := time.Now()
	start := now.Add(-60 * time.Minute)
	bucketSize := 10 * time.Minute

	logs := []types.LogEntry{
		{ServiceName: "api", Timestamp: start.Add(5 * time.Minute)},
		{ServiceName: "api", Timestamp: start.Add(7 * time.Minute)},
		{ServiceName: "api", Timestamp: start.Add(15 * time.Minute)},
		{ServiceName: "web", Timestamp: start.Add(5 * time.Minute)},
	}

	points := bucketLogs(logs, "api", start, now, bucketSize)
	if len(points) != 6 {
		t.Fatalf("expected 6 buckets, got %d", len(points))
	}
	if points[0].Value != 2.0 {
		t.Errorf("expected 2 logs in first bucket, got %f", points[0].Value)
	}
	if points[1].Value != 1.0 {
		t.Errorf("expected 1 log in second bucket, got %f", points[1].Value)
	}
	if points[2].Value != 0.0 {
		t.Errorf("expected 0 logs in third bucket, got %f", points[2].Value)
	}
}

func TestComputeRateBuckets(t *testing.T) {
	now := time.Now()
	errors := []DataPoint{
		{Timestamp: now, Value: 5},
		{Timestamp: now.Add(10 * time.Minute), Value: 10},
		{Timestamp: now.Add(20 * time.Minute), Value: 0},
	}
	calls := []DataPoint{
		{Timestamp: now, Value: 100},
		{Timestamp: now.Add(10 * time.Minute), Value: 200},
		{Timestamp: now.Add(20 * time.Minute), Value: 0},
	}

	rates := computeRateBuckets(errors, calls)
	if len(rates) != 2 {
		t.Fatalf("expected 2 rate buckets (zero-call bucket skipped), got %d", len(rates))
	}
	assertInDelta(t, 5.0, rates[0].Value, 0.01, "rate[0]")
	assertInDelta(t, 5.0, rates[1].Value, 0.01, "rate[1]")
}

func TestAssessRisk_Stable(t *testing.T) {
	sf := ServiceForecast{
		CurrentRate:   0.5,
		ErrorTrend:    Trend{Direction: "stable", R2: 0.1},
		RateTrend:     Trend{Direction: "stable", R2: 0.1},
		PredictedRate: 0.5,
	}

	score, level, warnings := assessRisk(sf)
	if level != "stable" {
		t.Errorf("expected stable, got %s", level)
	}
	if score >= 30 {
		t.Errorf("expected score < 30, got %f", score)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestAssessRisk_Critical(t *testing.T) {
	sf := ServiceForecast{
		CurrentRate:   8.0,
		ErrorTrend:    Trend{Direction: "rising", Slope: 2.0, R2: 0.8},
		RateTrend:     Trend{Direction: "rising", Slope: 1.0, R2: 0.7},
		PredictedRate: 15.0,
	}

	score, level, warnings := assessRisk(sf)
	if level != "critical" {
		t.Errorf("expected critical, got %s", level)
	}
	if score < 60 {
		t.Errorf("expected score >= 60, got %f", score)
	}
	if len(warnings) == 0 {
		t.Error("expected warnings")
	}
}

func TestAssessRisk_Degrading(t *testing.T) {
	sf := ServiceForecast{
		CurrentRate:   4.0,
		ErrorTrend:    Trend{Direction: "rising", Slope: 1.5, R2: 0.5},
		RateTrend:     Trend{Direction: "rising", Slope: 0.5, R2: 0.4},
		PredictedRate: 7.0,
	}

	score, level, _ := assessRisk(sf)
	if level != "degrading" {
		t.Errorf("expected degrading, got %s (score=%f)", level, score)
	}
	if score < 30 || score >= 60 {
		t.Errorf("expected score 30-59, got %f", score)
	}
}

func TestGenerate_Basic(t *testing.T) {
	now := time.Now()
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api-service", NumErrors: 10, NumCalls: 1000, ErrorRate: 1.0},
			{Name: "web-service", NumErrors: 0, NumCalls: 500, ErrorRate: 0},
		},
		logs: map[string]*types.QueryResult{
			"ERROR": {
				Logs: []types.LogEntry{
					{ServiceName: "api-service", Timestamp: now.Add(-50 * time.Minute)},
					{ServiceName: "api-service", Timestamp: now.Add(-40 * time.Minute)},
					{ServiceName: "api-service", Timestamp: now.Add(-30 * time.Minute)},
				},
			},
			"": {Logs: []types.LogEntry{}},
		},
	}

	ctx := context.Background()
	report, err := Generate(ctx, mock, "test-instance", Options{
		Duration: 60,
		Horizon:  30,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalServices != 2 {
		t.Errorf("expected 2 services, got %d", report.TotalServices)
	}
	if report.Instance != "test-instance" {
		t.Errorf("expected test-instance, got %s", report.Instance)
	}
}

func TestGenerate_ServiceFilter(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api-service", NumErrors: 10, NumCalls: 1000, ErrorRate: 1.0},
			{Name: "web-service", NumErrors: 0, NumCalls: 500, ErrorRate: 0},
		},
		logs: map[string]*types.QueryResult{
			"ERROR": {Logs: []types.LogEntry{}},
			"":      {Logs: []types.LogEntry{}},
		},
	}

	ctx := context.Background()
	report, err := Generate(ctx, mock, "test", Options{
		Duration: 60,
		Horizon:  30,
		Service:  "api-service",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.TotalServices != 1 {
		t.Errorf("expected 1 service, got %d", report.TotalServices)
	}
	if report.Services[0].Name != "api-service" {
		t.Errorf("expected api-service, got %s", report.Services[0].Name)
	}
}

func TestGenerate_ServiceNotFound(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{{Name: "api-service"}},
		logs:     map[string]*types.QueryResult{},
	}

	ctx := context.Background()
	_, err := Generate(ctx, mock, "test", Options{Service: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %s", err.Error())
	}
}

func TestRenderTerminal(t *testing.T) {
	report := &Report{
		GeneratedAt:    time.Now(),
		Instance:       "production",
		Duration:       120,
		Horizon:        60,
		TotalServices:  2,
		StableCount:    1,
		DegradingCount: 1,
		Services: []ServiceForecast{
			{
				Name:          "api-service",
				CurrentErrors: 50,
				CurrentCalls:  1000,
				CurrentRate:   5.0,
				ErrorTrend:    Trend{Slope: 0.5, Direction: "rising", R2: 0.8},
				PredictedRate: 8.0,
				RiskScore:     45,
				RiskLevel:     "degrading",
				Warnings:      []string{"Error count rising"},
			},
			{
				Name:          "web-service",
				CurrentErrors: 2,
				CurrentCalls:  500,
				CurrentRate:   0.4,
				ErrorTrend:    Trend{Slope: 0, Direction: "stable", R2: 0.1},
				PredictedRate: 0.4,
				RiskScore:     5,
				RiskLevel:     "stable",
			},
		},
	}

	var buf bytes.Buffer
	report.RenderTerminal(&buf)
	out := buf.String()
	if !strings.Contains(out, "Forecast") {
		t.Error("expected 'Forecast' in output")
	}
	if !strings.Contains(out, "production") {
		t.Error("expected 'production' in output")
	}
	if !strings.Contains(out, "api-service") {
		t.Error("expected 'api-service' in output")
	}
}

func TestRenderMarkdown(t *testing.T) {
	report := &Report{
		GeneratedAt:   time.Now(),
		Instance:      "production",
		Duration:      120,
		Horizon:       60,
		TotalServices: 1,
		StableCount:   1,
		Services: []ServiceForecast{
			{
				Name:          "api-service",
				CurrentErrors: 10,
				CurrentRate:   1.0,
				ErrorTrend:    Trend{Slope: 0.1, Direction: "rising", R2: 0.5},
				PredictedRate: 2.0,
				RiskScore:     15,
				RiskLevel:     "stable",
			},
		},
	}

	var buf bytes.Buffer
	report.RenderMarkdown(&buf)
	out := buf.String()
	if !strings.HasPrefix(out, "# ") {
		t.Error("expected markdown header")
	}
	if !strings.Contains(out, "api-service") {
		t.Error("expected 'api-service' in output")
	}
}

func TestRenderTerminal_WithAISummary(t *testing.T) {
	report := &Report{
		GeneratedAt:   time.Now(),
		Instance:      "test",
		Duration:      60,
		Horizon:       30,
		TotalServices: 0,
		AISummary:     "Everything looks stable.",
	}

	var buf bytes.Buffer
	report.RenderTerminal(&buf)
	out := buf.String()
	if !strings.Contains(out, "AI Analysis") {
		t.Error("expected 'AI Analysis' in output")
	}
	if !strings.Contains(out, "Everything looks stable.") {
		t.Error("expected AI summary text in output")
	}
}

func TestRenderMarkdown_WithWarnings(t *testing.T) {
	report := &Report{
		GeneratedAt:   time.Now(),
		Instance:      "test",
		Duration:      60,
		Horizon:       30,
		TotalServices: 1,
		CriticalCount: 1,
		Services: []ServiceForecast{
			{
				Name:          "failing-service",
				CurrentErrors: 100,
				CurrentRate:   15.0,
				ErrorTrend:    Trend{Slope: 2.0, Direction: "rising", R2: 0.9},
				PredictedRate: 25.0,
				RiskScore:     85,
				RiskLevel:     "critical",
				Warnings:      []string{"Error count rising (2.00/min, R²=0.90)"},
			},
		},
	}

	var buf bytes.Buffer
	report.RenderMarkdown(&buf)
	out := buf.String()
	if !strings.Contains(out, "Warnings") {
		t.Error("expected 'Warnings' in output")
	}
	if !strings.Contains(out, "failing-service") {
		t.Error("expected 'failing-service' in output")
	}
}

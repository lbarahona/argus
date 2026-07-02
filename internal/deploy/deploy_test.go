package deploy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services []types.Service
	logs     *types.QueryResult
	traces   *types.QueryResult
}

func (m *mockQuerier) Health(_ context.Context) (bool, time.Duration, error) {
	return true, time.Millisecond, nil
}

func (m *mockQuerier) ListServices(_ context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(_ context.Context, _ string, _ int, _ int, _ string) (*types.QueryResult, error) {
	if m.logs != nil {
		return m.logs, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryTraces(_ context.Context, _ string, _ int, _ int) (*types.QueryResult, error) {
	if m.traces != nil {
		return m.traces, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryMetrics(_ context.Context, _ string, _ int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func TestDetectChangePoint_NoData(t *testing.T) {
	buckets := []DataPoint{}
	cp := detectChangePoint(buckets, "test-svc", "errors", 30, 0.5)
	if cp != nil {
		t.Error("expected nil for empty data")
	}
}

func TestDetectChangePoint_StableData(t *testing.T) {
	now := time.Now()
	buckets := make([]DataPoint, 12)
	for i := range buckets {
		buckets[i] = DataPoint{
			Timestamp: now.Add(time.Duration(i) * 5 * time.Minute),
			Value:     10, // constant
		}
	}

	cp := detectChangePoint(buckets, "test-svc", "errors", 30, 0.5)
	if cp != nil {
		t.Error("expected nil for stable data, got change point")
	}
}

func TestDetectChangePoint_ClearJump(t *testing.T) {
	now := time.Now()
	buckets := make([]DataPoint, 12)
	for i := range buckets {
		buckets[i] = DataPoint{
			Timestamp: now.Add(time.Duration(i) * 5 * time.Minute),
		}
		if i < 6 {
			buckets[i].Value = 5 // low errors
		} else {
			buckets[i].Value = 50 // high errors (10x jump)
		}
	}

	cp := detectChangePoint(buckets, "api-svc", "error_count", 30, 0.3)
	if cp == nil {
		t.Fatal("expected change point for clear jump")
	}
	if cp.Service != "api-svc" {
		t.Errorf("expected service 'api-svc', got %q", cp.Service)
	}
	if cp.Magnitude <= 0 {
		t.Errorf("expected positive magnitude for increase, got %.2f", cp.Magnitude)
	}
	if cp.Confidence < 0.3 {
		t.Errorf("expected confidence >= 0.3, got %.2f", cp.Confidence)
	}
}

func TestDetectChangePoint_ClearDrop(t *testing.T) {
	now := time.Now()
	buckets := make([]DataPoint, 12)
	for i := range buckets {
		buckets[i] = DataPoint{
			Timestamp: now.Add(time.Duration(i) * 5 * time.Minute),
		}
		if i < 6 {
			buckets[i].Value = 50 // high
		} else {
			buckets[i].Value = 5 // low (drop)
		}
	}

	cp := detectChangePoint(buckets, "api-svc", "error_count", 30, 0.3)
	if cp == nil {
		t.Fatal("expected change point for clear drop")
	}
	if cp.Magnitude >= 0 {
		t.Errorf("expected negative magnitude for decrease, got %.2f", cp.Magnitude)
	}
}

func TestDetectNewErrorPatterns(t *testing.T) {
	now := time.Now()
	windowStart := now.Add(-60 * time.Minute)

	logs := []types.LogEntry{
		{ServiceName: "api", Body: "connection timeout to database", Timestamp: windowStart.Add(10 * time.Minute)},
		{ServiceName: "api", Body: "connection timeout to database", Timestamp: windowStart.Add(40 * time.Minute)},
		{ServiceName: "api", Body: "new: OOM killed container", Timestamp: windowStart.Add(40 * time.Minute)},
		{ServiceName: "api", Body: "new: nil pointer dereference in handler", Timestamp: windowStart.Add(45 * time.Minute)},
	}

	patterns := detectNewErrorPatterns(logs, "api", windowStart, now)
	if len(patterns) != 2 {
		t.Errorf("expected 2 new patterns, got %d", len(patterns))
	}
}

func TestBucketLogs(t *testing.T) {
	now := time.Now()
	windowStart := now.Add(-60 * time.Minute)
	bucketSize := 10 * time.Minute

	logs := []types.LogEntry{
		{ServiceName: "api", Timestamp: windowStart.Add(5 * time.Minute)},
		{ServiceName: "api", Timestamp: windowStart.Add(7 * time.Minute)},
		{ServiceName: "api", Timestamp: windowStart.Add(15 * time.Minute)},
		{ServiceName: "web", Timestamp: windowStart.Add(5 * time.Minute)}, // different service
	}

	buckets := bucketLogs(logs, "api", windowStart, bucketSize, 6)
	if len(buckets) != 6 {
		t.Fatalf("expected 6 buckets, got %d", len(buckets))
	}
	if buckets[0].Value != 2 {
		t.Errorf("expected 2 logs in first bucket, got %.0f", buckets[0].Value)
	}
	if buckets[1].Value != 1 {
		t.Errorf("expected 1 log in second bucket, got %.0f", buckets[1].Value)
	}
	if buckets[2].Value != 0 {
		t.Errorf("expected 0 logs in third bucket, got %.0f", buckets[2].Value)
	}
}

func TestCalculateImpactScore(t *testing.T) {
	tests := []struct {
		name     string
		changes  []ChangePoint
		wantSign int // -1 negative, 0 neutral, 1 positive
	}{
		{
			name:     "no changes",
			changes:  nil,
			wantSign: 0,
		},
		{
			name: "error spike is bad",
			changes: []ChangePoint{
				{Type: ChangeErrorSpike, Magnitude: 100, Confidence: 0.8},
			},
			wantSign: -1,
		},
		{
			name: "error drop is good",
			changes: []ChangePoint{
				{Type: ChangeErrorDrop, Magnitude: -50, Confidence: 0.8},
			},
			wantSign: 1,
		},
		{
			name: "latency spike is bad",
			changes: []ChangePoint{
				{Type: ChangeLatencySpike, Magnitude: 80, Confidence: 0.9},
			},
			wantSign: -1,
		},
		{
			name: "new errors are bad",
			changes: []ChangePoint{
				{Type: ChangeNewErrors, Magnitude: 100, Confidence: 0.7},
			},
			wantSign: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateImpactScore(tt.changes)
			switch tt.wantSign {
			case -1:
				if score >= 0 {
					t.Errorf("expected negative score, got %.2f", score)
				}
			case 1:
				if score <= 0 {
					t.Errorf("expected positive score, got %.2f", score)
				}
			case 0:
				if score != 0 {
					t.Errorf("expected zero score, got %.2f", score)
				}
			}
		})
	}
}

func TestClassifyImpact(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{50, ImpactPositive},
		{-30, ImpactNegative},
		{0, ImpactNeutral},
		{5, ImpactNeutral},
		{-5, ImpactNeutral},
	}
	for _, tt := range tests {
		got := classifyImpact(tt.score)
		if got != tt.want {
			t.Errorf("classifyImpact(%.1f) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestDetect_NoServices(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{},
		logs:     &types.QueryResult{},
	}

	result, err := Detect(context.Background(), mock, "test", Options{Duration: 60, Buckets: 6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Services) != 0 {
		t.Errorf("expected 0 services, got %d", len(result.Services))
	}
	if result.Summary.TotalChanges != 0 {
		t.Errorf("expected 0 changes, got %d", result.Summary.TotalChanges)
	}
}

func TestDetect_WithErrorSpike(t *testing.T) {
	now := time.Now()
	windowStart := now.Add(-60 * time.Minute)

	// Create logs with a clear spike pattern: few errors early, many errors later
	var logs []types.LogEntry
	for i := 0; i < 3; i++ {
		logs = append(logs, types.LogEntry{
			ServiceName: "payment-api",
			Body:        "connection reset",
			Timestamp:   windowStart.Add(time.Duration(i*5) * time.Minute),
		})
	}
	for i := 0; i < 30; i++ {
		logs = append(logs, types.LogEntry{
			ServiceName: "payment-api",
			Body:        "connection reset",
			Timestamp:   windowStart.Add(35*time.Minute + time.Duration(i)*time.Minute),
		})
	}

	mock := &mockQuerier{
		services: []types.Service{
			{Name: "payment-api", NumCalls: 1000, ErrorRate: 3.0},
		},
		logs: &types.QueryResult{Logs: logs},
	}

	result, err := Detect(context.Background(), mock, "prod", Options{
		Duration:    60,
		Buckets:     6,
		Sensitivity: SensitivityMedium,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Services) == 0 {
		t.Fatal("expected at least one service with changes")
	}

	found := false
	for _, svc := range result.Services {
		if svc.Name == "payment-api" {
			found = true
			if svc.Impact != ImpactNegative {
				t.Errorf("expected negative impact, got %s", svc.Impact)
			}
			if svc.ImpactScore >= 0 {
				t.Errorf("expected negative score for error spike, got %.2f", svc.ImpactScore)
			}
		}
	}
	if !found {
		t.Error("payment-api not found in results")
	}
}

func TestDetect_ServiceFilter(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api-a"},
			{Name: "api-b"},
		},
		logs: &types.QueryResult{},
	}

	_, err := Detect(context.Background(), mock, "test", Options{
		Duration: 60,
		Buckets:  6,
		Service:  "api-nonexistent",
	})
	if err == nil {
		t.Error("expected error for nonexistent service")
	}
}

func TestLatencyChangeDetectionIgnoresEmptyBuckets(t *testing.T) {
	// 12 buckets: first 9 empty (no traces — older than the newest-N fetch),
	// last 3 steady at ~200ms. No real change occurred.
	buckets := make([]DataPoint, 12)
	base := time.Now().Add(-6 * time.Hour)
	for i := range buckets {
		buckets[i].Timestamp = base.Add(time.Duration(i) * 30 * time.Minute)
	}
	buckets[9].Value, buckets[10].Value, buckets[11].Value = 200, 205, 198

	cp := detectLatencyChangePoint(buckets, "api", 50, 0.5)
	if cp != nil {
		t.Errorf("steady latency with empty older buckets must not yield a change point, got %+v", cp)
	}
}

func TestLatencyChangeDetectionStillFindsRealShifts(t *testing.T) {
	buckets := make([]DataPoint, 12)
	base := time.Now().Add(-6 * time.Hour)
	for i := range buckets {
		buckets[i].Timestamp = base.Add(time.Duration(i) * 30 * time.Minute)
		if i < 6 {
			buckets[i].Value = 100
		} else {
			buckets[i].Value = 400 // real 4x latency shift
		}
	}

	cp := detectLatencyChangePoint(buckets, "api", 50, 0.5)
	if cp == nil {
		t.Fatal("real 4x latency shift must be detected")
	}
}

func TestDetect_TraceTruncationCaveat(t *testing.T) {
	now := time.Now()
	traces := make([]types.TraceEntry, deployTraceFetchLimit) // == the fetch limit
	for i := range traces {
		traces[i] = types.TraceEntry{ServiceName: "api", Timestamp: now.Add(-time.Duration(i) * time.Second)}
	}
	mock := &mockQuerier{
		services: []types.Service{{Name: "api"}},
		logs:     &types.QueryResult{},
		traces:   &types.QueryResult{Traces: traces},
	}

	result, err := Detect(context.Background(), mock, "test", Options{Duration: 60, Buckets: 6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated || result.DataCaveat == "" {
		t.Errorf("trace fetch at limit must set Truncated + DataCaveat, got %+v / %q", result.Truncated, result.DataCaveat)
	}
}

func TestDetect_TruncationCaveat(t *testing.T) {
	now := time.Now()
	logs := make([]types.LogEntry, deployFetchLimit) // == the fetch limit
	for i := range logs {
		logs[i] = types.LogEntry{ServiceName: "api", Timestamp: now.Add(-time.Duration(i) * time.Second)}
	}
	mock := &mockQuerier{
		services: []types.Service{{Name: "api"}},
		logs:     &types.QueryResult{Logs: logs},
	}

	result, err := Detect(context.Background(), mock, "test", Options{Duration: 60, Buckets: 6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated || result.DataCaveat == "" {
		t.Errorf("fetch at limit must set Truncated + DataCaveat, got %+v / %q", result.Truncated, result.DataCaveat)
	}
}

func TestDetect_NoTruncationBelowLimit(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{{Name: "api"}},
		logs:     &types.QueryResult{Logs: []types.LogEntry{{ServiceName: "api", Timestamp: time.Now()}}},
	}

	result, err := Detect(context.Background(), mock, "test", Options{Duration: 60, Buckets: 6})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Truncated || result.DataCaveat != "" {
		t.Errorf("fetch below limit must not set Truncated/DataCaveat, got %+v / %q", result.Truncated, result.DataCaveat)
	}
}

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 100}
	p99 := percentile(data, 99)
	if p99 != 100 {
		t.Errorf("expected p99=100, got %.1f", p99)
	}

	p50 := percentile(data, 50)
	if p50 != 5 {
		t.Errorf("expected p50=5, got %.1f", p50)
	}
}

func TestSumBuckets(t *testing.T) {
	buckets := []DataPoint{
		{Value: 10}, {Value: 20}, {Value: 30},
		{Value: 40}, {Value: 50}, {Value: 60},
	}
	before, after := sumBuckets(buckets, 3)
	if before != 60 {
		t.Errorf("expected before=60, got %.0f", before)
	}
	if after != 150 {
		t.Errorf("expected after=150, got %.0f", after)
	}
}

func TestCalcMean(t *testing.T) {
	got := calcMean([]float64{2, 4, 6, 8})
	if got != 5 {
		t.Errorf("expected mean=5, got %.1f", got)
	}

	got = calcMean([]float64{})
	if got != 0 {
		t.Errorf("expected mean=0 for empty, got %.1f", got)
	}
}

func TestCalcStdDev(t *testing.T) {
	// All same values => stddev should be 0
	got := calcStdDev([]float64{5, 5, 5, 5}, 5)
	if got != 0 {
		t.Errorf("expected stddev=0, got %.4f", got)
	}

	// Single value => 0
	got = calcStdDev([]float64{5}, 5)
	if got != 0 {
		t.Errorf("expected stddev=0 for single value, got %.4f", got)
	}
}

func TestGetThresholds(t *testing.T) {
	high := getThresholds(SensitivityHigh)
	med := getThresholds(SensitivityMedium)
	low := getThresholds(SensitivityLow)

	// High sensitivity should have lower thresholds (more sensitive)
	if high.errorRateChange >= med.errorRateChange {
		t.Error("high sensitivity should have lower error rate threshold than medium")
	}
	if med.errorRateChange >= low.errorRateChange {
		t.Error("medium sensitivity should have lower error rate threshold than low")
	}
}

func TestNormalizePattern(t *testing.T) {
	got := normalizePattern("")
	if got != "" {
		t.Error("expected empty for empty input")
	}

	got = normalizePattern("short error")
	if got != "short error" {
		t.Errorf("expected 'short error', got %q", got)
	}

	long := "this is a very long error message that definitely exceeds the eighty character limit for pattern normalization purposes"
	got = normalizePattern(long)
	// textutil.Truncate caps at 80 runes then appends "..." to signal
	// truncation, so the result is up to 83 chars, not exactly 80.
	if len(got) > 83 {
		t.Errorf("expected truncation to at most 83 chars (80 + ellipsis), got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected truncated pattern to end with an ellipsis, got %q", got)
	}
}

func TestBuildSummary(t *testing.T) {
	services := []ServiceImpact{
		{Name: "api", Impact: ImpactNegative, ImpactScore: -40, Changes: []ChangePoint{{}, {}}},
		{Name: "web", Impact: ImpactPositive, ImpactScore: 20, Changes: []ChangePoint{{}}},
		{Name: "db", Impact: ImpactNeutral, ImpactScore: 0, Changes: []ChangePoint{}},
	}

	summary := buildSummary(services)
	if summary.TotalChanges != 3 {
		t.Errorf("expected 3 total changes, got %d", summary.TotalChanges)
	}
	if summary.Positive != 1 {
		t.Errorf("expected 1 positive, got %d", summary.Positive)
	}
	if summary.Negative != 1 {
		t.Errorf("expected 1 negative, got %d", summary.Negative)
	}
	if summary.Neutral != 1 {
		t.Errorf("expected 1 neutral, got %d", summary.Neutral)
	}
	if summary.MostImpacted != "api" {
		t.Errorf("expected most impacted 'api', got %q", summary.MostImpacted)
	}
}

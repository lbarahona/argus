package scorecard

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services []types.Service
	logs     map[string]*types.QueryResult
	traces   map[string]*types.QueryResult
	healthy  bool
}

var _ signoz.SignozQuerier = (*mockQuerier)(nil)

func (m *mockQuerier) Health(_ context.Context) (bool, time.Duration, error) {
	return m.healthy, 10 * time.Millisecond, nil
}

func (m *mockQuerier) ListServices(_ context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(_ context.Context, service string, _, _ int, _ string) (*types.QueryResult, error) {
	if m.logs != nil {
		if r, ok := m.logs[service]; ok {
			return r, nil
		}
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryTraces(_ context.Context, service string, _, _ int) (*types.QueryResult, error) {
	if m.traces != nil {
		if r, ok := m.traces[service]; ok {
			return r, nil
		}
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryMetrics(_ context.Context, _ string, _ int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func TestScoreToGrade(t *testing.T) {
	tests := []struct {
		score float64
		grade Grade
	}{
		{100, GradeA}, {95, GradeA}, {90, GradeA},
		{89, GradeB}, {75, GradeB},
		{74, GradeC}, {60, GradeC},
		{59, GradeD}, {40, GradeD},
		{39, GradeF}, {0, GradeF},
	}
	for _, tt := range tests {
		if got := scoreToGrade(tt.score); got != tt.grade {
			t.Errorf("scoreToGrade(%.1f) = %s, want %s", tt.score, got, tt.grade)
		}
	}
}

func TestComputeScore_PerfectService(t *testing.T) {
	ss := ServiceScore{ErrorRate: 0, TotalCalls: 1000, P99Latency: 100, ErrorTrend: TrendStable}
	if score := computeScore(ss); score != 100.0 {
		t.Errorf("perfect service score = %.1f, want 100.0", score)
	}
}

func TestComputeScore_HighErrorRate(t *testing.T) {
	ss := ServiceScore{ErrorRate: 10, TotalCalls: 1000, P99Latency: 200, ErrorTrend: TrendStable}
	if score := computeScore(ss); score >= 60.0 {
		t.Errorf("high error rate score = %.1f, want < 60", score)
	}
}

func TestComputeScore_HighLatency(t *testing.T) {
	ss := ServiceScore{ErrorRate: 0, TotalCalls: 1000, P99Latency: 5000, ErrorTrend: TrendStable}
	if score := computeScore(ss); score >= 75.0 {
		t.Errorf("high latency score = %.1f, want < 75", score)
	}
}

func TestComputeScore_NoTraffic(t *testing.T) {
	ss := ServiceScore{ErrorRate: 0, TotalCalls: 0, ErrorTrend: TrendStable}
	if score := computeScore(ss); score != 50.0 {
		t.Errorf("no traffic score = %.1f, want 50.0", score)
	}
}

func TestComputeScore_TrendBonus(t *testing.T) {
	base := ServiceScore{ErrorRate: 1, TotalCalls: 1000, P99Latency: 200, ErrorTrend: TrendStable}
	better := base
	better.ErrorTrend = TrendBetter
	worse := base
	worse.ErrorTrend = TrendWorse

	stableScore := computeScore(base)
	betterScore := computeScore(better)
	worseScore := computeScore(worse)

	if betterScore <= stableScore {
		t.Errorf("better trend (%.1f) should be > stable (%.1f)", betterScore, stableScore)
	}
	if worseScore >= stableScore {
		t.Errorf("worse trend (%.1f) should be < stable (%.1f)", worseScore, stableScore)
	}
}

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if p := percentile(data, 50); p != 5.0 {
		t.Errorf("p50 = %.1f, want 5.0", p)
	}
	if p := percentile(data, 99); p != 10.0 {
		t.Errorf("p99 = %.1f, want 10.0", p)
	}
	if p := percentile(nil, 50); p != 0.0 {
		t.Errorf("empty p50 = %.1f, want 0.0", p)
	}
}

func TestGroupErrors(t *testing.T) {
	logs := []types.LogEntry{
		{Body: "connection refused"}, {Body: "connection refused"}, {Body: "connection refused"},
		{Body: "timeout after 30s"}, {Body: "timeout after 30s"},
		{Body: "null pointer"},
	}
	groups := groupErrors(logs, 2)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	if groups[0].Message != "connection refused" || groups[0].Count != 3 {
		t.Errorf("top group: %s x%d, want connection refused x3", groups[0].Message, groups[0].Count)
	}
}

func TestGroupErrors_LongMessages(t *testing.T) {
	longMsg := strings.Repeat("x", 200)
	logs := []types.LogEntry{{Body: longMsg}}
	groups := groupErrors(logs, 5)
	if len(groups) != 1 || len(groups[0].Message) > 100 {
		t.Errorf("long message not truncated: len=%d", len(groups[0].Message))
	}
}

func TestComputeOverall_Empty(t *testing.T) {
	score, grade := computeOverall(nil)
	if score != 0 || grade != GradeF {
		t.Errorf("empty overall = %.1f %s, want 0.0 F", score, grade)
	}
}

func TestComputeOverall_WeightedByTraffic(t *testing.T) {
	services := []ServiceScore{
		{Score: 95, TotalCalls: 10000},
		{Score: 30, TotalCalls: 10},
	}
	score, grade := computeOverall(services)
	if score <= 90.0 {
		t.Errorf("weighted score = %.1f, want > 90 (high-traffic service dominates)", score)
	}
	if grade != GradeA {
		t.Errorf("grade = %s, want A", grade)
	}
}

func TestGenerate_BasicFlow(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 10},
			{Name: "worker", NumCalls: 500, NumErrors: 50},
		},
		logs: map[string]*types.QueryResult{
			"api":    {Logs: []types.LogEntry{{Body: "timeout"}}},
			"worker": {Logs: []types.LogEntry{{Body: "crash"}, {Body: "crash"}}},
		},
		traces: map[string]*types.QueryResult{
			"api": {Traces: []types.TraceEntry{
				{DurationNano: 50_000_000},
				{DurationNano: 200_000_000},
			}},
		},
		healthy: true,
	}

	sc, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Services) != 2 {
		t.Fatalf("got %d services, want 2", len(sc.Services))
	}
	// Sorted worst first
	if sc.Services[0].Name != "worker" {
		t.Errorf("worst service = %s, want worker", sc.Services[0].Name)
	}
	if sc.Services[0].Score >= sc.Services[1].Score {
		t.Error("worker score should be less than api score")
	}
}

func TestGenerate_ServiceFilter(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 10},
			{Name: "worker", NumCalls: 500, NumErrors: 50},
		},
		healthy: true,
	}
	sc, err := Generate(context.Background(), mock, "test", Options{Duration: 60, Service: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Services) != 1 || sc.Services[0].Name != "api" {
		t.Errorf("filter failed: got %d services", len(sc.Services))
	}
}

func TestGenerate_NoServices(t *testing.T) {
	mock := &mockQuerier{services: nil, healthy: true}
	sc, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Services) != 0 {
		t.Errorf("expected no services, got %d", len(sc.Services))
	}
}

func TestRenderTerminal(t *testing.T) {
	sc := &Scorecard{
		GeneratedAt: time.Date(2026, 2, 25, 23, 0, 0, 0, time.UTC), Duration: 60,
		Instance: "prod", OverallGrade: GradeB, OverallScore: 82.5,
		Services: []ServiceScore{
			{Name: "api", Grade: GradeA, Score: 95, ErrorRate: 0.5, TotalCalls: 10000, P50Latency: 45, P99Latency: 200, ErrorTrend: TrendStable},
			{Name: "worker", Grade: GradeD, Score: 45, ErrorRate: 8.5, TotalCalls: 500, P50Latency: 100, P99Latency: 3000, ErrorTrend: TrendWorse,
				TopErrors: []ErrorGroup{{Message: "connection refused", Count: 42}}},
		},
	}
	var buf bytes.Buffer
	RenderTerminal(&buf, sc)
	out := buf.String()
	for _, want := range []string{"Reliability Scorecard", "prod", "api", "worker", "connection refused"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal output missing %q", want)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	sc := &Scorecard{
		GeneratedAt: time.Date(2026, 2, 25, 23, 0, 0, 0, time.UTC), Duration: 60,
		Instance: "prod", OverallGrade: GradeA, OverallScore: 95,
		Services: []ServiceScore{
			{Name: "api", Grade: GradeA, Score: 95, ErrorRate: 0.1, TotalCalls: 5000},
		},
	}
	var buf bytes.Buffer
	RenderMarkdown(&buf, sc)
	out := buf.String()
	for _, want := range []string{"# Reliability Scorecard", "| Service |", "api"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q", want)
		}
	}
}

func TestGradeEmoji(t *testing.T) {
	if e := gradeEmoji(GradeA); e != "🟢" {
		t.Errorf("A emoji = %s", e)
	}
	if e := gradeEmoji(GradeF); e != "🔴" {
		t.Errorf("F emoji = %s", e)
	}
}

func TestTruncate(t *testing.T) {
	if s := truncate("hello", 10); s != "hello" {
		t.Errorf("short truncate = %s", s)
	}
	if s := truncate("hello world", 5); s != "hell…" {
		t.Errorf("long truncate = %s", s)
	}
}

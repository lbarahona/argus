package guard

import (
	"context"
	"strings"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock client
// ──────────────────────────────────────────────

type mockQuerier struct {
	services []types.Service
	logs     *types.QueryResult
	traces   *types.QueryResult
	svcErr   error
	logErr   error
}

func (m *mockQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, m.svcErr
}

func (m *mockQuerier) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if m.logErr != nil {
		return nil, m.logErr
	}
	if m.logs != nil {
		return m.logs, nil
	}
	return &types.QueryResult{Logs: nil}, nil
}

func (m *mockQuerier) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	if m.traces != nil {
		return m.traces, nil
	}
	return &types.QueryResult{Traces: nil}, nil
}

// ──────────────────────────────────────────────
// Tests: calculateScore
// ──────────────────────────────────────────────

func TestCalculateScore(t *testing.T) {
	tests := []struct {
		name   string
		checks []CheckResult
		want   int
	}{
		{"empty", nil, 50},
		{"all pass", []CheckResult{
			{Status: "pass", Severity: 0},
			{Status: "pass", Severity: 0},
		}, 100},
		{"one warning", []CheckResult{
			{Status: "pass", Severity: 0},
			{Status: "warn", Severity: 1},
		}, 85},
		{"one fail", []CheckResult{
			{Status: "pass", Severity: 0},
			{Status: "fail", Severity: 2},
		}, 65},
		{"multiple warnings", []CheckResult{
			{Status: "warn", Severity: 1},
			{Status: "warn", Severity: 1},
			{Status: "warn", Severity: 1},
		}, 55},
		{"catastrophe", []CheckResult{
			{Status: "fail", Severity: 2},
			{Status: "fail", Severity: 2},
			{Status: "fail", Severity: 2},
		}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateScore(tt.checks)
			if got != tt.want {
				t.Errorf("calculateScore() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Tests: determineVerdict
// ──────────────────────────────────────────────

func TestDetermineVerdict(t *testing.T) {
	tests := []struct {
		score  int
		strict bool
		want   Verdict
	}{
		{100, false, VerdictShip},
		{85, false, VerdictShip},
		{70, false, VerdictShip},
		{50, false, VerdictCaution},
		{40, false, VerdictCaution},
		{30, false, VerdictHold},
		{0, false, VerdictHold},
		// Strict mode
		{100, true, VerdictShip},
		{90, true, VerdictShip},
		{80, true, VerdictCaution},
		{70, true, VerdictCaution},
		{60, true, VerdictHold},
		{0, true, VerdictHold},
	}

	for _, tt := range tests {
		got := determineVerdict(tt.score, tt.strict)
		if got != tt.want {
			t.Errorf("determineVerdict(%d, %v) = %q, want %q", tt.score, tt.strict, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: checkSystemHealth
// ──────────────────────────────────────────────

func TestCheckSystemHealth(t *testing.T) {
	a := &Analyzer{}

	t.Run("no services", func(t *testing.T) {
		result := a.checkSystemHealth(nil, 10)
		if result.Status != "warn" {
			t.Errorf("no services should be warn, got %q", result.Status)
		}
	})

	t.Run("all healthy", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 5},
			{Name: "web", NumCalls: 500, NumErrors: 2},
		}
		result := a.checkSystemHealth(services, 10)
		if result.Status != "pass" {
			t.Errorf("healthy services should pass, got %q", result.Status)
		}
	})

	t.Run("service down", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 950}, // 95% error rate
			{Name: "web", NumCalls: 500, NumErrors: 2},
		}
		result := a.checkSystemHealth(services, 10)
		if result.Status != "fail" {
			t.Errorf("service down should fail, got %q", result.Status)
		}
	})

	t.Run("below min calls ignored", func(t *testing.T) {
		services := []types.Service{
			{Name: "cron", NumCalls: 5, NumErrors: 5}, // 100% but low volume
		}
		result := a.checkSystemHealth(services, 10)
		if result.Status != "warn" {
			t.Errorf("below min calls should be warn (no active), got %q", result.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: checkErrorRates
// ──────────────────────────────────────────────

func TestCheckErrorRates(t *testing.T) {
	a := &Analyzer{}

	t.Run("all clean", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 10000, NumErrors: 10}, // 0.1%
		}
		result := a.checkErrorRates(services, 5.0, 1.0, 10)
		if result.Status != "pass" {
			t.Errorf("clean services should pass, got %q", result.Status)
		}
	})

	t.Run("warning level", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 10000, NumErrors: 200}, // 2%
		}
		result := a.checkErrorRates(services, 5.0, 1.0, 10)
		if result.Status != "warn" {
			t.Errorf("2%% error rate should warn, got %q", result.Status)
		}
	})

	t.Run("critical level", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 10000, NumErrors: 600}, // 6%
		}
		result := a.checkErrorRates(services, 5.0, 1.0, 10)
		if result.Status != "fail" {
			t.Errorf("6%% error rate should fail, got %q", result.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: checkLatency
// ──────────────────────────────────────────────

func TestCheckLatency(t *testing.T) {
	t.Run("normal latency", func(t *testing.T) {
		traces := makeTraces(100, 500_000_000) // 500ms in nanos
		a := &Analyzer{client: &mockQuerier{
			traces: &types.QueryResult{Traces: traces},
		}}
		services := []types.Service{
			{Name: "api", NumCalls: 1000},
		}
		result, _ := a.checkLatency(context.Background(), services, 5000, 2000, 10)
		if result.Status != "pass" {
			t.Errorf("normal latency should pass, got %q", result.Status)
		}
	})

	t.Run("high latency warning", func(t *testing.T) {
		traces := makeTraces(100, 3_000_000_000) // 3s in nanos
		a := &Analyzer{client: &mockQuerier{
			traces: &types.QueryResult{Traces: traces},
		}}
		services := []types.Service{
			{Name: "api", NumCalls: 1000},
		}
		result, _ := a.checkLatency(context.Background(), services, 5000, 2000, 10)
		if result.Status != "warn" {
			t.Errorf("3s P99 should warn, got %q", result.Status)
		}
	})

	t.Run("critical latency", func(t *testing.T) {
		traces := makeTraces(100, 6_000_000_000) // 6s in nanos
		a := &Analyzer{client: &mockQuerier{
			traces: &types.QueryResult{Traces: traces},
		}}
		services := []types.Service{
			{Name: "api", NumCalls: 1000},
		}
		result, _ := a.checkLatency(context.Background(), services, 5000, 2000, 10)
		if result.Status != "fail" {
			t.Errorf("6s P99 should fail, got %q", result.Status)
		}
	})

	t.Run("no traces", func(t *testing.T) {
		a := &Analyzer{client: &mockQuerier{}}
		services := []types.Service{
			{Name: "api", NumCalls: 1000},
		}
		result, _ := a.checkLatency(context.Background(), services, 5000, 2000, 10)
		if result.Status != "pass" {
			t.Errorf("no traces should pass, got %q", result.Status)
		}
	})
}

func TestComputeP99(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		p := computeP99(nil)
		if p != 0 {
			t.Errorf("empty should be 0, got %f", p)
		}
	})

	t.Run("single", func(t *testing.T) {
		traces := makeTraces(1, 100_000_000) // 100ms
		p := computeP99(traces)
		if p != 100 {
			t.Errorf("single trace P99 should be 100ms, got %f", p)
		}
	})

	t.Run("varied", func(t *testing.T) {
		// 100 traces, 98 at 100ms, 2 at 1000ms — P99 should pick the high ones
		traces := makeTraces(98, 100_000_000)
		traces = append(traces, types.TraceEntry{DurationNano: 1_000_000_000})
		traces = append(traces, types.TraceEntry{DurationNano: 1_000_000_000})
		p := computeP99(traces)
		if p != 1000 {
			t.Errorf("P99 should be 1000ms, got %f", p)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: checkErrorSpikes
// ──────────────────────────────────────────────

func TestCheckErrorSpikes(t *testing.T) {
	a := &Analyzer{}

	t.Run("no errors", func(t *testing.T) {
		a.client = &mockQuerier{
			logs: &types.QueryResult{Logs: nil},
		}
		result, err := a.checkErrorSpikes(context.Background(), nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "pass" {
			t.Errorf("no errors should pass, got %q", result.Status)
		}
	})

	t.Run("moderate errors", func(t *testing.T) {
		logs := make([]types.LogEntry, 25)
		for i := range logs {
			logs[i] = types.LogEntry{Body: "test error"}
		}
		a.client = &mockQuerier{
			logs: &types.QueryResult{Logs: logs},
		}
		result, err := a.checkErrorSpikes(context.Background(), nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "warn" {
			t.Errorf("25 errors should warn, got %q", result.Status)
		}
	})

	t.Run("error storm", func(t *testing.T) {
		logs := make([]types.LogEntry, 55)
		for i := range logs {
			logs[i] = types.LogEntry{Body: "critical failure"}
		}
		a.client = &mockQuerier{
			logs: &types.QueryResult{Logs: logs},
		}
		result, err := a.checkErrorSpikes(context.Background(), nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "fail" {
			t.Errorf("55 errors should fail, got %q", result.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: checkSaturation
// ──────────────────────────────────────────────

func TestCheckSaturation(t *testing.T) {
	a := &Analyzer{}

	t.Run("normal distribution", func(t *testing.T) {
		services := []types.Service{
			{Name: "api", NumCalls: 1000},
			{Name: "web", NumCalls: 800},
			{Name: "auth", NumCalls: 900},
		}
		result := a.checkSaturation(services, 10)
		if result.Status != "pass" {
			t.Errorf("normal distribution should pass, got %q", result.Status)
		}
	})

	t.Run("one outlier", func(t *testing.T) {
		// Many similar services + one wildly higher. The outlier must be
		// >3 stddevs above mean of the rest to trigger.
		services := []types.Service{
			{Name: "web1", NumCalls: 100},
			{Name: "web2", NumCalls: 105},
			{Name: "web3", NumCalls: 98},
			{Name: "web4", NumCalls: 102},
			{Name: "web5", NumCalls: 97},
			{Name: "web6", NumCalls: 103},
			{Name: "web7", NumCalls: 99},
			{Name: "web8", NumCalls: 101},
			{Name: "api", NumCalls: 100000}, // massive outlier vs tight cluster
		}
		result := a.checkSaturation(services, 10)
		if result.Status != "warn" {
			t.Errorf("outlier traffic should warn, got %q", result.Status)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: resolveThresholds
// ──────────────────────────────────────────────

func TestResolveThresholds(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		maxErr, warnErr, maxP99, warnP99, minCalls := resolveThresholds(Options{})
		if maxErr != defaultMaxErrorRate {
			t.Errorf("default maxErr = %f, want %f", maxErr, defaultMaxErrorRate)
		}
		if warnErr != defaultWarnErrorRate {
			t.Errorf("default warnErr = %f, want %f", warnErr, defaultWarnErrorRate)
		}
		if maxP99 != defaultMaxP99Latency {
			t.Errorf("default maxP99 = %f, want %v", maxP99, defaultMaxP99Latency)
		}
		if warnP99 != defaultWarnP99Latency {
			t.Errorf("default warnP99 = %f, want %v", warnP99, defaultWarnP99Latency)
		}
		if minCalls != defaultMinCallVolume {
			t.Errorf("default minCalls = %d, want %d", minCalls, defaultMinCallVolume)
		}
	})

	t.Run("strict", func(t *testing.T) {
		maxErr, warnErr, _, _, _ := resolveThresholds(Options{Strict: true})
		if maxErr != strictMaxErrorRate {
			t.Errorf("strict maxErr = %f, want %f", maxErr, strictMaxErrorRate)
		}
		if warnErr != strictWarnErrorRate {
			t.Errorf("strict warnErr = %f, want %f", warnErr, strictWarnErrorRate)
		}
	})

	t.Run("custom overrides", func(t *testing.T) {
		maxErr, _, maxP99, _, minCalls := resolveThresholds(Options{
			MaxErrorRate:  10.0,
			MaxP99Latency: 8000,
			MinCallVolume: 100,
		})
		if maxErr != 10.0 {
			t.Errorf("custom maxErr = %f, want 10.0", maxErr)
		}
		if maxP99 != 8000 {
			t.Errorf("custom maxP99 = %f, want 8000", maxP99)
		}
		if minCalls != 100 {
			t.Errorf("custom minCalls = %d, want 100", minCalls)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: meanStdDev
// ──────────────────────────────────────────────

func TestMeanStdDev(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		mean, std := meanStdDev(nil)
		if mean != 0 || std != 0 {
			t.Errorf("empty should be (0,0), got (%f, %f)", mean, std)
		}
	})

	t.Run("uniform", func(t *testing.T) {
		mean, std := meanStdDev([]float64{5, 5, 5, 5})
		if mean != 5 || std != 0 {
			t.Errorf("uniform should be (5,0), got (%f, %f)", mean, std)
		}
	})

	t.Run("varied", func(t *testing.T) {
		mean, std := meanStdDev([]float64{2, 4, 4, 4, 5, 5, 7, 9})
		if mean != 5 {
			t.Errorf("mean should be 5, got %f", mean)
		}
		if std < 1.9 || std > 2.1 {
			t.Errorf("std should be ~2.0, got %f", std)
		}
	})
}

// ──────────────────────────────────────────────
// Tests: ExitCode
// ──────────────────────────────────────────────

func TestExitCode(t *testing.T) {
	tests := []struct {
		verdict Verdict
		want    int
	}{
		{VerdictShip, 0},
		{VerdictCaution, 1},
		{VerdictHold, 2},
	}

	for _, tt := range tests {
		r := &GuardReport{Verdict: tt.verdict}
		got := r.ExitCode()
		if got != tt.want {
			t.Errorf("ExitCode() for %s = %d, want %d", tt.verdict, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: formatNumber
// ──────────────────────────────────────────────

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{2500000, "2.5M"},
	}
	for _, tt := range tests {
		got := formatNumber(tt.n)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: topNPatterns
// ──────────────────────────────────────────────

func TestTopNPatterns(t *testing.T) {
	patterns := map[string]int{
		"connection refused": 10,
		"timeout":            5,
		"null pointer":       2,
		"disk full":          1,
	}

	result := topNPatterns(patterns, 2)
	if !strings.Contains(result, "connection refused") {
		t.Error("should contain top pattern")
	}
	if !strings.Contains(result, "timeout") {
		t.Error("should contain second pattern")
	}
	if strings.Contains(result, "disk full") {
		t.Error("should not contain fourth pattern")
	}
}

// ──────────────────────────────────────────────
// Tests: buildSummary
// ──────────────────────────────────────────────

func TestBuildSummary(t *testing.T) {
	t.Run("ship", func(t *testing.T) {
		r := &GuardReport{
			Verdict: VerdictShip,
			Checks:  []CheckResult{{Status: "pass"}, {Status: "pass"}},
		}
		s := buildSummary(r)
		if !strings.Contains(s, "✅") {
			t.Error("SHIP summary should have checkmark")
		}
	})

	t.Run("hold", func(t *testing.T) {
		r := &GuardReport{
			Verdict: VerdictHold,
			Checks:  []CheckResult{{Status: "fail"}, {Status: "fail"}},
		}
		s := buildSummary(r)
		if !strings.Contains(s, "🛑") {
			t.Error("HOLD summary should have stop sign")
		}
	})
}

// ──────────────────────────────────────────────
// Tests: Full integration
// ──────────────────────────────────────────────

func TestCheckIntegration(t *testing.T) {
	t.Run("healthy system", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 5},
				{Name: "web", NumCalls: 8000, NumErrors: 3},
			},
			logs:   &types.QueryResult{Logs: nil},
			traces: &types.QueryResult{Traces: makeTraces(50, 200_000_000)},
		}

		analyzer := NewAnalyzer(mock, "prod")
		report, err := analyzer.Check(context.Background(), Options{})
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}

		if report.Verdict != VerdictShip {
			t.Errorf("healthy system should be SHIP, got %s", report.Verdict)
		}
		if report.Score < 70 {
			t.Errorf("healthy system score should be >=70, got %d", report.Score)
		}
		if len(report.Services) != 2 {
			t.Errorf("expected 2 services, got %d", len(report.Services))
		}
	})

	t.Run("degraded system", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 300}, // 3% error
				{Name: "web", NumCalls: 8000, NumErrors: 10},
			},
			logs:   &types.QueryResult{Logs: makeLogs(25)},
			traces: &types.QueryResult{Traces: makeTraces(50, 3_000_000_000)}, // 3s P99
		}

		analyzer := NewAnalyzer(mock, "prod")
		report, err := analyzer.Check(context.Background(), Options{})
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}

		if report.Verdict == VerdictShip {
			t.Error("degraded system should not be SHIP")
		}
	})

	t.Run("system on fire", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 9500}, // 95% errors
				{Name: "web", NumCalls: 8000, NumErrors: 7000},
			},
			logs:   &types.QueryResult{Logs: makeLogs(60)},
			traces: &types.QueryResult{Traces: makeTraces(50, 8_000_000_000)}, // 8s P99
		}

		analyzer := NewAnalyzer(mock, "prod")
		report, err := analyzer.Check(context.Background(), Options{})
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}

		if report.Verdict != VerdictHold {
			t.Errorf("burning system should be HOLD, got %s", report.Verdict)
		}
		if report.Score > 30 {
			t.Errorf("burning system score should be low, got %d", report.Score)
		}
	})

	t.Run("strict mode blocks warnings", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 80}, // 0.8% error
			},
			logs:   &types.QueryResult{Logs: nil},
			traces: &types.QueryResult{Traces: makeTraces(50, 1_500_000_000)}, // 1.5s P99
		}

		// Normal mode: should be OK
		analyzer := NewAnalyzer(mock, "prod")
		normal, _ := analyzer.Check(context.Background(), Options{})
		if normal.Verdict == VerdictHold {
			t.Error("normal mode should not HOLD for mild issues")
		}

		// Strict mode: same data should be more cautious
		strict, _ := analyzer.Check(context.Background(), Options{Strict: true})
		if strict.Score >= normal.Score {
			t.Error("strict mode should have lower or equal score")
		}
	})

	t.Run("service filter", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 10000, NumErrors: 5},
				{Name: "web", NumCalls: 8000, NumErrors: 3},
			},
			logs:   &types.QueryResult{Logs: nil},
			traces: &types.QueryResult{Traces: makeTraces(50, 200_000_000)},
		}

		analyzer := NewAnalyzer(mock, "prod")
		report, err := analyzer.Check(context.Background(), Options{Service: "api"})
		if err != nil {
			t.Fatalf("Check() error: %v", err)
		}
		if len(report.Services) != 1 {
			t.Errorf("filtered report should have 1 service, got %d", len(report.Services))
		}
	})

	t.Run("service not found", func(t *testing.T) {
		mock := &mockQuerier{
			services: []types.Service{
				{Name: "api", NumCalls: 100},
			},
		}

		analyzer := NewAnalyzer(mock, "prod")
		_, err := analyzer.Check(context.Background(), Options{Service: "nonexistent"})
		if err == nil {
			t.Error("should error on nonexistent service")
		}
	})
}

// ──────────────────────────────────────────────
// Tests: Output formats
// ──────────────────────────────────────────────

func TestFormatTerminal(t *testing.T) {
	report := &GuardReport{
		Timestamp:  "2026-03-06T05:00:00Z",
		Instance:   "prod",
		Verdict:    VerdictShip,
		Score:      95,
		StrictMode: false,
		Summary:    "✅ All clear",
		Checks: []CheckResult{
			{Name: "System Health", Status: "pass", Detail: "All good"},
			{Name: "Error Rates", Status: "pass", Detail: "Clean"},
		},
		Services: []ServiceGuardResult{
			{Service: "api", NumCalls: 10000, NumErrors: 5, ErrorRate: 0.05, P99Latency: 200.0, Status: "healthy"},
		},
	}

	out := FormatTerminal(report)
	if out == "" {
		t.Error("FormatTerminal() returned empty")
	}
	if !strings.Contains(out, "SHIP") {
		t.Error("should contain SHIP verdict")
	}
	if !strings.Contains(out, "System Health") {
		t.Error("should contain check names")
	}
}

func TestFormatMarkdown(t *testing.T) {
	report := &GuardReport{
		Timestamp:  "2026-03-06T05:00:00Z",
		Instance:   "prod",
		Verdict:    VerdictHold,
		Score:      20,
		StrictMode: true,
		Summary:    "🛑 Hold",
		Checks: []CheckResult{
			{Name: "Error Rates", Status: "fail", Detail: "Critical"},
		},
		Services: []ServiceGuardResult{
			{Service: "api", NumCalls: 10000, NumErrors: 5000, ErrorRate: 50, P99Latency: 8000, Status: "unhealthy"},
		},
		AIAdvisory: "Do not deploy.",
	}

	out := FormatMarkdown(report)
	if !strings.Contains(out, "# 🛡️ Deployment Guard") {
		t.Error("markdown should have header")
	}
	if !strings.Contains(out, "HOLD") {
		t.Error("markdown should contain HOLD verdict")
	}
	if !strings.Contains(out, "AI Deployment Advisory") {
		t.Error("markdown should have AI section")
	}
}

func TestFormatJSON(t *testing.T) {
	report := &GuardReport{
		Timestamp: "2026-03-06T05:00:00Z",
		Instance:  "prod",
		Verdict:   VerdictShip,
		Score:     100,
	}

	out, err := FormatJSON(report)
	if err != nil {
		t.Fatalf("FormatJSON() error: %v", err)
	}
	if !strings.Contains(out, `"verdict": "SHIP"`) {
		t.Error("JSON should contain verdict")
	}
	if !strings.Contains(out, `"score": 100`) {
		t.Error("JSON should contain score")
	}
}

// ──────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────

func makeLogs(n int) []types.LogEntry {
	logs := make([]types.LogEntry, n)
	for i := range logs {
		logs[i] = types.LogEntry{Body: "test error message"}
	}
	return logs
}

func makeTraces(n int, durationNano int64) []types.TraceEntry {
	traces := make([]types.TraceEntry, n)
	for i := range traces {
		traces[i] = types.TraceEntry{
			ServiceName:  "test-svc",
			DurationNano: durationNano,
		}
	}
	return traces
}

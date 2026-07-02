package budget

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/internal/slo"
	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock client
// ──────────────────────────────────────────────

type mockLister struct {
	services []types.Service
	err      error
}

func (m *mockLister) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, m.err
}

// ──────────────────────────────────────────────
// Tests: classifyBudgetStatus
// ──────────────────────────────────────────────

func TestClassifyBudgetStatus(t *testing.T) {
	tests := []struct {
		consumed  float64
		remaining float64
		want      string
	}{
		{0, 100, "healthy"},
		{30, 70, "healthy"},
		{49, 51, "healthy"},
		{50, 50, "burning"},
		{79, 21, "burning"},
		{80, 20, "critical"},
		{95, 5, "critical"},
		{100, 0, "exhausted"},
		{120, 0, "exhausted"},
	}

	for _, tt := range tests {
		got := classifyBudgetStatus(tt.consumed, tt.remaining)
		if got != tt.want {
			t.Errorf("classifyBudgetStatus(%v, %v) = %q, want %q", tt.consumed, tt.remaining, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: classifyAlert (multi-window burn rate)
// ──────────────────────────────────────────────

func TestClassifyAlert(t *testing.T) {
	tests := []struct {
		name   string
		burn1h float64
		burn6h float64
		want   string
	}{
		{"no burn", 0, 0, "none"},
		{"low burn", 1.0, 1.0, "none"},
		{"elevated short-term", 8.0, 3.0, "watch"},
		{"slow burn", 3.0, 7.0, "ticket"},
		{"fast burn page", 15.0, 7.0, "page"},
		{"critical both windows", 20.0, 10.0, "page"},
		{"high 1h only", 15.0, 3.0, "watch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyAlert(tt.burn1h, tt.burn6h)
			if got != tt.want {
				t.Errorf("classifyAlert(%.1f, %.1f) = %q, want %q", tt.burn1h, tt.burn6h, got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Tests: determineTrend
// ──────────────────────────────────────────────

func TestDetermineTrend(t *testing.T) {
	tests := []struct {
		name   string
		points []BurnPoint
		want   string
	}{
		{"empty", nil, "stable"},
		{"too few", []BurnPoint{{BudgetRemaining: 80}, {BudgetRemaining: 70}}, "stable"},
		{"stable", []BurnPoint{
			{BudgetRemaining: 80}, {BudgetRemaining: 79}, {BudgetRemaining: 78},
			{BudgetRemaining: 77}, {BudgetRemaining: 76}, {BudgetRemaining: 75},
		}, "stable"},
		{"degrading", []BurnPoint{
			{BudgetRemaining: 90}, {BudgetRemaining: 85}, {BudgetRemaining: 80},
			{BudgetRemaining: 70}, {BudgetRemaining: 60}, {BudgetRemaining: 50},
			{BudgetRemaining: 40}, {BudgetRemaining: 30}, {BudgetRemaining: 20},
		}, "degrading"},
		{"improving", []BurnPoint{
			{BudgetRemaining: 50}, {BudgetRemaining: 55}, {BudgetRemaining: 60},
			{BudgetRemaining: 65}, {BudgetRemaining: 70}, {BudgetRemaining: 80},
			{BudgetRemaining: 85}, {BudgetRemaining: 90}, {BudgetRemaining: 95},
		}, "improving"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineTrend(tt.points)
			if got != tt.want {
				t.Errorf("determineTrend() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Tests: recommendPolicy
// ──────────────────────────────────────────────

func TestRecommendPolicy(t *testing.T) {
	tests := []struct {
		name   string
		report BudgetReport
		substr string
	}{
		{"exhausted", BudgetReport{Status: "exhausted", Alert: "none"}, "FREEZE"},
		{"page alert", BudgetReport{Status: "critical", Alert: "page"}, "PAGE"},
		{"ticket alert", BudgetReport{Status: "burning", Alert: "ticket"}, "TICKET"},
		{"critical status", BudgetReport{Status: "critical", Alert: "none"}, "CAUTION"},
		{"burning status", BudgetReport{Status: "burning", Alert: "none"}, "MONITOR"},
		{"healthy", BudgetReport{Status: "healthy", Alert: "none"}, "ALL CLEAR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recommendPolicy(tt.report)
			if !strContains(got, tt.substr) {
				t.Errorf("recommendPolicy() = %q, want to contain %q", got, tt.substr)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Tests: parseWindow
// ──────────────────────────────────────────────

func TestParseWindow(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 1440},
		{"1h", 60},
		{"6h", 360},
		{"24h", 1440},
		{"7d", 10080},
		{"30d", 43200},
		{"30m", 30},
	}

	for _, tt := range tests {
		got := parseWindow(tt.input)
		if got != tt.want {
			t.Errorf("parseWindow(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: formatDuration
// ──────────────────────────────────────────────

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		hours float64
		want  string
	}{
		{0.5, "30 minutes"},
		{2.5, "2.5 hours"},
		{48, "2.0 days"},
		{168, "7 days"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.hours)
		if got != tt.want {
			t.Errorf("formatDuration(%.1f) = %q, want %q", tt.hours, got, tt.want)
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
		{1500, "1.5K"},
		{1000000, "1.0M"},
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
// Tests: calculateOverallBudget
// ──────────────────────────────────────────────

func TestCalculateOverallBudget(t *testing.T) {
	a := &Analyzer{}

	t.Run("no traffic", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 0.1}
		s := slo.SLO{Service: "api", Target: 99.9}
		a.calculateOverallBudget(&br, s, []types.Service{})
		if br.Status != "healthy" {
			t.Errorf("no traffic should be healthy, got %q", br.Status)
		}
		if br.CurrentAvail != 100.0 {
			t.Errorf("no traffic availability should be 100, got %f", br.CurrentAvail)
		}
	})

	t.Run("perfect service", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 0.1}
		s := slo.SLO{Service: "api", Target: 99.9}
		services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 0}}
		a.calculateOverallBudget(&br, s, services)
		if br.BudgetRemaining != 100.0 {
			t.Errorf("perfect service budget remaining should be 100, got %f", br.BudgetRemaining)
		}
		if br.Status != "healthy" {
			t.Errorf("perfect service should be healthy, got %q", br.Status)
		}
	})

	t.Run("budget burning", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 0.1}
		s := slo.SLO{Service: "api", Target: 99.9, Window: "6h"}
		// 0.06% error rate = 60% of 0.1% budget, observed over the full 6h window
		services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 6}}
		a.calculateOverallBudget(&br, s, services)
		if br.Status != "burning" {
			t.Errorf("60%% budget consumed should be burning, got %q", br.Status)
		}
		if br.BudgetConsumed < 55 || br.BudgetConsumed > 65 {
			t.Errorf("expected ~60%% consumed, got %.1f%%", br.BudgetConsumed)
		}
	})

	t.Run("budget critical", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 0.1}
		s := slo.SLO{Service: "api", Target: 99.9, Window: "6h"}
		// 0.09% error rate = 90% of 0.1% budget, observed over the full 6h window
		services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 9}}
		a.calculateOverallBudget(&br, s, services)
		if br.Status != "critical" {
			t.Errorf("90%% budget consumed should be critical, got %q", br.Status)
		}
	})

	t.Run("budget exhausted", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 0.1}
		s := slo.SLO{Service: "api", Target: 99.9, Window: "6h"}
		// 0.15% error rate = 150% of 0.1% budget, observed over the full 6h window
		services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 15}}
		a.calculateOverallBudget(&br, s, services)
		if br.Status != "exhausted" {
			t.Errorf("150%% budget consumed should be exhausted, got %q", br.Status)
		}
		if br.BudgetRemaining != 0 {
			t.Errorf("exhausted budget remaining should be 0, got %f", br.BudgetRemaining)
		}
	})

	t.Run("all services combined", func(t *testing.T) {
		br := BudgetReport{TotalBudget: 1.0}
		s := slo.SLO{Service: "", Target: 99.0}
		services := []types.Service{
			{Name: "api", NumCalls: 5000, NumErrors: 10},
			{Name: "web", NumCalls: 5000, NumErrors: 20},
		}
		a.calculateOverallBudget(&br, s, services)
		if br.TotalRequests != 10000 {
			t.Errorf("total requests should be 10000, got %d", br.TotalRequests)
		}
		if br.FailedRequests != 30 {
			t.Errorf("failed requests should be 30, got %d", br.FailedRequests)
		}
	})
}

func TestCalculateOverallBudgetSubUnitBurnIsHealthy(t *testing.T) {
	a := &Analyzer{}
	br := BudgetReport{TotalBudget: 0.1}
	s := slo.SLO{Service: "api", Target: 99.9, Window: "30d"}
	// 0.09% error rate over the observed 6h = 0.9x burn: sustainable forever.
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}

	a.calculateOverallBudget(&br, s, services)

	if br.Status != "healthy" {
		t.Errorf("0.9x burn on a 30d window should be healthy, got %q (consumed %.2f%%)",
			br.Status, br.BudgetConsumed)
	}
	if br.BudgetConsumed > 1.0 {
		t.Errorf("6h at 0.9x burn should consume <1%% of a 30d budget, got %.2f%%", br.BudgetConsumed)
	}
}

// ──────────────────────────────────────────────
// Tests: computeBurnWindow
// ──────────────────────────────────────────────

func TestComputeBurnWindow(t *testing.T) {
	t.Run("normal burn", func(t *testing.T) {
		s := slo.SLO{Service: "api", Target: 99.9, Window: "30d"}
		services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 5}}
		bw := computeBurnWindow(s, services, "1h", 60)
		if bw.Label != "1h" {
			t.Errorf("label should be '1h', got %q", bw.Label)
		}
		if bw.TotalRequests != 10000 {
			t.Errorf("total requests should be 10000, got %d", bw.TotalRequests)
		}
		if bw.BurnRate <= 0 {
			t.Errorf("burn rate should be positive, got %f", bw.BurnRate)
		}
	})

	t.Run("zero traffic", func(t *testing.T) {
		s := slo.SLO{Service: "api", Target: 99.9, Window: "30d"}
		services := []types.Service{{Name: "api", NumCalls: 0, NumErrors: 0}}
		bw := computeBurnWindow(s, services, "1h", 60)
		if bw.BurnRate != 0 {
			t.Errorf("zero traffic burn rate should be 0, got %f", bw.BurnRate)
		}
	})
}

func TestComputeBurnWindowFractionCapped(t *testing.T) {
	// SLO window (1h) shorter than the 6h burn window: fraction must cap at 1.
	s := slo.SLO{Service: "api", Target: 99.9, Window: "1h"}
	services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 5}} // 0.05% = 0.5x burn
	bw := computeBurnWindow(s, services, "6h", 360)
	if bw.BudgetUsed > 100 {
		t.Errorf("BudgetUsed must be capped at 100, got %.1f", bw.BudgetUsed)
	}
	if bw.BudgetUsed < 45 || bw.BudgetUsed > 55 {
		t.Errorf("0.5x burn with capped fraction should use ~50%%, got %.1f", bw.BudgetUsed)
	}
}

// ──────────────────────────────────────────────
// Tests: predictExhaustion
// ──────────────────────────────────────────────

func TestPredictExhaustion(t *testing.T) {
	t.Run("healthy - no prediction", func(t *testing.T) {
		br := BudgetReport{
			BudgetConsumed:  0,
			BudgetRemaining: 100,
			SLO:             slo.SLO{Window: "30d"},
		}
		predictExhaustion(&br)
		if br.ExhaustionETA != nil {
			t.Error("healthy budget should not have exhaustion ETA")
		}
	})

	t.Run("already exhausted", func(t *testing.T) {
		br := BudgetReport{
			BudgetConsumed:  100,
			BudgetRemaining: 0,
			SLO:             slo.SLO{Window: "30d"},
		}
		predictExhaustion(&br)
		if br.ExhaustionIn != "already exhausted" {
			t.Errorf("expected 'already exhausted', got %q", br.ExhaustionIn)
		}
	})

	t.Run("burning - has ETA", func(t *testing.T) {
		br := BudgetReport{
			BudgetConsumed:  50,
			BudgetRemaining: 50,
			BurnRate6h:      2.0,
			SLO:             slo.SLO{Window: "30d", Target: 99.9},
		}
		predictExhaustion(&br)
		if br.ExhaustionETA == nil {
			t.Error("burning budget should have exhaustion ETA")
		}
		if br.ExhaustionIn == "" {
			t.Error("burning budget should have exhaustion description")
		}
	})
}

func TestPredictExhaustionSubUnitBurnNeverExhausts(t *testing.T) {
	br := BudgetReport{
		SLO:             slo.SLO{Target: 99.9, Window: "30d"},
		TotalBudget:     0.1,
		BudgetConsumed:  50,
		BudgetRemaining: 50,
		BurnRate6h:      0.9,
	}
	predictExhaustion(&br)
	if br.ExhaustionIn != "" {
		t.Errorf("burn rate 0.9 must never predict exhaustion, got %q", br.ExhaustionIn)
	}
	if br.ExhaustionETA != nil {
		t.Errorf("burn rate 0.9 must not set an ETA, got %v", br.ExhaustionETA)
	}
}

// ──────────────────────────────────────────────
// Tests: ExitCode
// ──────────────────────────────────────────────

func TestExitCode(t *testing.T) {
	tests := []struct {
		name    string
		reports []BudgetReport
		want    int
	}{
		{"healthy", []BudgetReport{{Status: "healthy", Alert: "none"}}, 0},
		{"burning", []BudgetReport{{Status: "burning", Alert: "none"}}, 0},
		{"critical", []BudgetReport{{Status: "critical", Alert: "none"}}, 1},
		{"exhausted", []BudgetReport{{Status: "exhausted", Alert: "none"}}, 2},
		{"page alert", []BudgetReport{{Status: "burning", Alert: "page"}}, 2},
		{"ticket alert", []BudgetReport{{Status: "burning", Alert: "ticket"}}, 1},
		{"worst wins", []BudgetReport{
			{Status: "healthy", Alert: "none"},
			{Status: "exhausted", Alert: "none"},
		}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &FullReport{Reports: tt.reports}
			got := r.ExitCode()
			if got != tt.want {
				t.Errorf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ──────────────────────────────────────────────
// Tests: SortByUrgency
// ──────────────────────────────────────────────

func TestSortByUrgency(t *testing.T) {
	reports := []BudgetReport{
		{SLO: slo.SLO{Name: "healthy"}, Status: "healthy", BudgetRemaining: 90},
		{SLO: slo.SLO{Name: "exhausted"}, Status: "exhausted", BudgetRemaining: 0},
		{SLO: slo.SLO{Name: "critical"}, Status: "critical", BudgetRemaining: 15},
		{SLO: slo.SLO{Name: "burning"}, Status: "burning", BudgetRemaining: 40},
	}

	SortByUrgency(reports)

	if reports[0].SLO.Name != "exhausted" {
		t.Errorf("first should be exhausted, got %q", reports[0].SLO.Name)
	}
	if reports[1].SLO.Name != "critical" {
		t.Errorf("second should be critical, got %q", reports[1].SLO.Name)
	}
	if reports[len(reports)-1].SLO.Name != "healthy" {
		t.Errorf("last should be healthy, got %q", reports[len(reports)-1].SLO.Name)
	}
}

// ──────────────────────────────────────────────
// Tests: Full analyzer with mock
// ──────────────────────────────────────────────

func TestAnalyzeIntegration(t *testing.T) {
	mock := &mockLister{
		services: []types.Service{
			{Name: "api-gateway", NumCalls: 100000, NumErrors: 50},
			{Name: "auth-service", NumCalls: 50000, NumErrors: 10},
		},
	}

	analyzer := NewAnalyzer(mock, "test-instance")
	cfg := &slo.SLOConfig{
		SLOs: []slo.SLO{
			{Name: "API Availability", Service: "api-gateway", Type: "availability", Target: 99.9, Window: "30d"},
			{Name: "Auth Availability", Service: "auth-service", Type: "availability", Target: 99.95, Window: "30d"},
		},
	}

	report, err := analyzer.Analyze(context.Background(), cfg, Options{Window: "6h"})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if len(report.Reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(report.Reports))
	}

	// API gateway: 0.05% error rate over the observed 6h = 0.5x burn on a
	// 30d SLO window → well under 1% consumed, not "burning".
	apiReport := report.Reports[0]
	if apiReport.TotalRequests != 100000 {
		t.Errorf("API total requests = %d, want 100000", apiReport.TotalRequests)
	}
	if apiReport.FailedRequests != 50 {
		t.Errorf("API failed requests = %d, want 50", apiReport.FailedRequests)
	}
	if apiReport.Status != "healthy" {
		t.Errorf("API status = %q, want healthy (0.5x burn consumes <1%% of a 30d budget)", apiReport.Status)
	}

	// Auth service: 0.02% error rate over the observed 6h = 0.4x burn on a
	// 30d SLO window → healthy.
	authReport := report.Reports[1]
	if authReport.TotalRequests != 50000 {
		t.Errorf("Auth total requests = %d, want 50000", authReport.TotalRequests)
	}
	if authReport.Status != "healthy" {
		t.Errorf("Auth status = %q, want healthy (0.4x burn over 6h of a 30d window)", authReport.Status)
	}
}

func TestAnalyzeServiceFilter(t *testing.T) {
	mock := &mockLister{
		services: []types.Service{
			{Name: "api-gateway", NumCalls: 100000, NumErrors: 50},
			{Name: "auth-service", NumCalls: 50000, NumErrors: 10},
		},
	}

	analyzer := NewAnalyzer(mock, "test")
	cfg := &slo.SLOConfig{
		SLOs: []slo.SLO{
			{Name: "API", Service: "api-gateway", Type: "availability", Target: 99.9, Window: "30d"},
			{Name: "Auth", Service: "auth-service", Type: "availability", Target: 99.95, Window: "30d"},
		},
	}

	report, err := analyzer.Analyze(context.Background(), cfg, Options{
		Window:  "6h",
		Service: "api-gateway",
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if len(report.Reports) != 1 {
		t.Fatalf("expected 1 filtered report, got %d", len(report.Reports))
	}
	if report.Reports[0].SLO.Name != "API" {
		t.Errorf("filtered report should be API, got %q", report.Reports[0].SLO.Name)
	}
}

func TestAnalyzeDisabledSLO(t *testing.T) {
	mock := &mockLister{
		services: []types.Service{
			{Name: "api", NumCalls: 10000, NumErrors: 5},
		},
	}

	disabled := false
	analyzer := NewAnalyzer(mock, "test")
	cfg := &slo.SLOConfig{
		SLOs: []slo.SLO{
			{Name: "Active", Service: "api", Type: "availability", Target: 99.9, Window: "30d"},
			{Name: "Disabled", Service: "api", Type: "availability", Target: 99.9, Window: "30d", Enabled: &disabled},
		},
	}

	report, _ := analyzer.Analyze(context.Background(), cfg, Options{Window: "6h"})
	if len(report.Reports) != 1 {
		t.Fatalf("expected 1 report (disabled skipped), got %d", len(report.Reports))
	}
}

// ──────────────────────────────────────────────
// Tests: Format output
// ──────────────────────────────────────────────

func TestFormatTerminal(t *testing.T) {
	now := time.Now()
	report := &FullReport{
		Timestamp: now.Format(time.RFC3339),
		Instance:  "prod",
		Reports: []BudgetReport{
			{
				SLO:             slo.SLO{Name: "API Availability", Service: "api", Target: 99.9, Type: "availability"},
				WindowLabel:     "6h",
				TotalBudget:     0.1,
				BudgetConsumed:  60,
				BudgetRemaining: 40,
				CurrentAvail:    99.94,
				Status:          "burning",
				Alert:           "none",
				Trend:           "stable",
				Policy:          "⚠️ MONITOR",
				TotalRequests:   100000,
				FailedRequests:  60,
				BurnWindows: []BurnWindow{
					{Label: "6h", BurnRate: 2.0, ErrorRate: 0.02, TotalRequests: 5000},
				},
			},
		},
	}

	out := FormatTerminal(report)
	if out == "" {
		t.Error("FormatTerminal() returned empty string")
	}
	if !strContains(out, "API Availability") {
		t.Error("output should contain SLO name")
	}
	if !strContains(out, "40.0% remaining") {
		t.Error("output should contain budget remaining")
	}
	if !strContains(out, "Burn Rates") {
		t.Error("output should contain burn rates section")
	}
}

func TestFormatMarkdown(t *testing.T) {
	report := &FullReport{
		Timestamp: "2026-03-05T03:00:00Z",
		Instance:  "prod",
		Reports: []BudgetReport{
			{
				SLO:             slo.SLO{Name: "Test SLO", Service: "api", Target: 99.9, Type: "availability"},
				WindowLabel:     "6h",
				TotalBudget:     0.1,
				BudgetConsumed:  25,
				BudgetRemaining: 75,
				CurrentAvail:    99.975,
				Status:          "healthy",
				Alert:           "none",
				Trend:           "stable",
				TotalRequests:   50000,
				FailedRequests:  12,
				BurnWindows: []BurnWindow{
					{Label: "6h", BurnRate: 0.5, ErrorRate: 0.005, TotalRequests: 2000},
				},
			},
		},
		AIInsight: "Everything looks good.",
	}

	out := FormatMarkdown(report)
	if !strContains(out, "# ⚡ Error Budget") {
		t.Error("markdown should have header")
	}
	if !strContains(out, "Test SLO") {
		t.Error("markdown should contain SLO name")
	}
	if !strContains(out, "AI Recommendations") {
		t.Error("markdown should contain AI section")
	}
}

// ──────────────────────────────────────────────
// Tests: renderBudgetGauge
// ──────────────────────────────────────────────

func TestRenderBudgetGauge(t *testing.T) {
	tests := []struct {
		remaining float64
	}{
		{100}, {75}, {40}, {15}, {0},
	}

	for _, tt := range tests {
		gauge := renderBudgetGauge(tt.remaining, 20)
		if gauge == "" {
			t.Errorf("renderBudgetGauge(%.0f) returned empty", tt.remaining)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: renderSparkline
// ──────────────────────────────────────────────

func TestRenderSparkline(t *testing.T) {
	points := []BurnPoint{
		{BudgetRemaining: 100},
		{BudgetRemaining: 90},
		{BudgetRemaining: 70},
		{BudgetRemaining: 50},
		{BudgetRemaining: 30},
	}

	spark := renderSparkline(points)
	if len(spark) == 0 {
		t.Error("sparkline should not be empty")
	}

	empty := renderSparkline(nil)
	if empty != "" {
		t.Error("empty points should return empty sparkline")
	}
}

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func strContains(s, sub string) bool {
	return len(s) >= len(sub) && strings.Contains(s, sub)
}

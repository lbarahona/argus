package budget

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/slo"
	"github.com/lbarahona/argus/pkg/types"
)

// observedWindowMins is the window the Signoz ListServices data covers (6h,
// fixed by the client). Budget consumption must be scaled by how much of
// the SLO window this observation actually represents.
const observedWindowMins = 360.0

// ──────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────

// ServiceLister can list services with their metrics.
type ServiceLister interface {
	ListServices(ctx context.Context) ([]types.Service, error)
}

// Options configures budget analysis.
type Options struct {
	Window     string // analysis window label: 1h, 6h, 24h, 7d, 30d
	Service    string // optional: filter to specific service
	Format     string // "terminal" or "markdown" or "json"
	WithAI     bool   // include AI-powered recommendations
	AIProvider ai.Provider
}

// BurnWindow represents error budget consumption in a specific time window.
type BurnWindow struct {
	Label         string  `json:"label"`
	DurationMins  int     `json:"duration_mins"`
	ErrorRate     float64 `json:"error_rate"`      // actual error rate in this window
	BurnRate      float64 `json:"burn_rate"`       // burn rate multiplier (1.0 = normal)
	BudgetUsed    float64 `json:"budget_used_pct"` // % of total budget consumed
	TotalRequests int     `json:"total_requests"`
	FailedReqs    int     `json:"failed_requests"`
}

// BudgetReport is the full error budget analysis for one SLO.
type BudgetReport struct {
	SLO             slo.SLO      `json:"slo"`
	WindowLabel     string       `json:"window"`
	TotalBudget     float64      `json:"total_budget_pct"`     // e.g. 0.1 for 99.9% SLO
	BudgetConsumed  float64      `json:"budget_consumed_pct"`  // % of budget used
	BudgetRemaining float64      `json:"budget_remaining_pct"` // % remaining
	CurrentAvail    float64      `json:"current_availability"` // e.g. 99.95%
	BurnRate1h      float64      `json:"burn_rate_1h"`         // short-window burn rate
	BurnRate6h      float64      `json:"burn_rate_6h"`         // long-window burn rate
	BurnWindows     []BurnWindow `json:"burn_windows"`
	ExhaustionETA   *time.Time   `json:"exhaustion_eta,omitempty"`
	ExhaustionIn    string       `json:"exhaustion_in,omitempty"`
	Status          string       `json:"status"`           // healthy, burning, critical, exhausted
	Alert           string       `json:"alert,omitempty"`  // multi-window alert status
	Policy          string       `json:"policy,omitempty"` // recommended action
	Trend           string       `json:"trend"`            // improving, stable, degrading
	TotalRequests   int          `json:"total_requests"`
	FailedRequests  int          `json:"failed_requests"`
	Datapoints      []BurnPoint  `json:"datapoints,omitempty"` // for burndown chart
}

// BurnPoint represents a point in the burndown chart.
type BurnPoint struct {
	Time             time.Time `json:"time"`
	BudgetRemaining  float64   `json:"budget_remaining_pct"`
	CumulativeErrors int       `json:"cumulative_errors"`
	CumulativeCalls  int       `json:"cumulative_calls"`
}

// FullReport holds all budget reports.
type FullReport struct {
	Timestamp string         `json:"timestamp"`
	Instance  string         `json:"instance"`
	Reports   []BudgetReport `json:"reports"`
	AIInsight string         `json:"ai_insight,omitempty"`
}

// ──────────────────────────────────────────────
// Analyzer
// ──────────────────────────────────────────────

// Analyzer calculates error budget burndown.
type Analyzer struct {
	client  ServiceLister
	instKey string
}

// NewAnalyzer creates a budget analyzer.
func NewAnalyzer(client ServiceLister, instKey string) *Analyzer {
	return &Analyzer{client: client, instKey: instKey}
}

// Analyze runs error budget analysis for all configured SLOs.
func (a *Analyzer) Analyze(ctx context.Context, cfg *slo.SLOConfig, opts Options) (*FullReport, error) {
	// Fetch services once (Signoz returns data for the last 6h)
	services, err := a.client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching services: %w", err)
	}

	report := &FullReport{
		Timestamp: time.Now().Format(time.RFC3339),
		Instance:  a.instKey,
	}

	windowLabel := opts.Window
	if windowLabel == "" {
		windowLabel = "6h"
	}

	for _, s := range cfg.SLOs {
		if !s.IsEnabled() {
			continue
		}
		if opts.Service != "" && !strings.EqualFold(s.Service, opts.Service) {
			continue
		}

		br := a.analyzeSLO(s, services, windowLabel)
		report.Reports = append(report.Reports, br)
	}

	// AI insight if requested
	if opts.WithAI && opts.AIProvider != nil && len(report.Reports) > 0 {
		insight, err := generateAIInsight(ctx, report, opts.AIProvider)
		if err == nil {
			report.AIInsight = insight
		}
	}

	return report, nil
}

// analyzeSLO evaluates error budget for a single SLO using available service data.
func (a *Analyzer) analyzeSLO(s slo.SLO, services []types.Service, windowLabel string) BudgetReport {
	br := BudgetReport{
		SLO:         s,
		WindowLabel: windowLabel,
		TotalBudget: 100.0 - s.Target,
	}

	// Calculate overall budget from service data
	a.calculateOverallBudget(&br, s, services)

	// Build a single burn window from the available data (Signoz gives us 6h)
	bw := computeBurnWindow(s, services, "6h", 360)
	br.BurnWindows = []BurnWindow{bw}
	br.BurnRate1h = bw.BurnRate // use same data as approximation
	br.BurnRate6h = bw.BurnRate

	// Determine alert status using multi-window burn rate
	br.Alert = classifyAlert(br.BurnRate1h, br.BurnRate6h)

	// Predict exhaustion based on SLO window
	predictExhaustion(&br)

	// Trend (with single snapshot, default to stable)
	br.Trend = "stable"

	// Set policy recommendation
	br.Policy = recommendPolicy(br)

	return br
}

// calculateOverallBudget computes budget consumption from service data.
func (a *Analyzer) calculateOverallBudget(br *BudgetReport, s slo.SLO, services []types.Service) {
	var totalCalls, totalErrors int

	if s.Service == "" {
		for _, svc := range services {
			totalCalls += svc.NumCalls
			totalErrors += svc.NumErrors
		}
	} else {
		for _, svc := range services {
			if strings.EqualFold(svc.Name, s.Service) {
				totalCalls = svc.NumCalls
				totalErrors = svc.NumErrors
				break
			}
		}
	}

	br.TotalRequests = totalCalls
	br.FailedRequests = totalErrors

	if totalCalls == 0 {
		br.CurrentAvail = 100.0
		br.BudgetConsumed = 0
		br.BudgetRemaining = 100.0
		br.Status = "healthy"
		return
	}

	errorRate := float64(totalErrors) / float64(totalCalls) * 100
	br.CurrentAvail = 100.0 - errorRate

	if br.TotalBudget > 0 {
		burnRate := errorRate / br.TotalBudget
		windowMins := float64(s.WindowMinutes())
		observed := math.Min(observedWindowMins, windowMins)
		// Budget consumed during the observed window, as a share of the
		// full-window budget. A 1.0 burn rate consumes budget exactly at
		// window pace; only the observed fraction of the window is known.
		br.BudgetConsumed = math.Min(100.0, burnRate*(observed/windowMins)*100)
		br.BudgetRemaining = math.Max(0, 100.0-br.BudgetConsumed)
	}

	br.Status = classifyBudgetStatus(br.BudgetConsumed, br.BudgetRemaining)
}

// computeBurnWindow calculates burn rate for a specific time window.
func computeBurnWindow(s slo.SLO, services []types.Service, label string, mins int) BurnWindow {
	bw := BurnWindow{
		Label:        label,
		DurationMins: mins,
	}

	var totalCalls, totalErrors int
	if s.Service == "" {
		for _, svc := range services {
			totalCalls += svc.NumCalls
			totalErrors += svc.NumErrors
		}
	} else {
		for _, svc := range services {
			if strings.EqualFold(svc.Name, s.Service) {
				totalCalls = svc.NumCalls
				totalErrors = svc.NumErrors
				break
			}
		}
	}

	bw.TotalRequests = totalCalls
	bw.FailedReqs = totalErrors

	if totalCalls == 0 {
		return bw
	}

	bw.ErrorRate = float64(totalErrors) / float64(totalCalls) * 100
	allowedRate := 100.0 - s.Target
	if allowedRate > 0 {
		bw.BurnRate = bw.ErrorRate / allowedRate
		windowMins := float64(s.WindowMinutes())
		observed := math.Min(float64(mins), windowMins)
		bw.BudgetUsed = math.Min(100, bw.BurnRate*(observed/windowMins)*100)
	}

	return bw
}

// predictExhaustion estimates when the error budget will be fully consumed.
func predictExhaustion(br *BudgetReport) {
	if br.BudgetConsumed <= 0 || br.BudgetRemaining >= 100 {
		return
	}
	if br.BudgetConsumed >= 100 {
		br.ExhaustionIn = "already exhausted"
		return
	}

	// Use burn rate to project forward
	// If we consumed X% in 6h, project when we'll hit 100%
	burnRate := br.BurnRate6h
	if burnRate <= 1.0 {
		// At or below 1.0x the budget is consumed no faster than the rolling
		// SLO window replenishes it — it never exhausts.
		return
	}

	// Budget allowed per SLO window
	windowMins := float64(br.SLO.WindowMinutes())
	// At current burn rate, we consume the full budget in (windowMins / burnRate) minutes
	timeToExhaustMins := (br.BudgetRemaining / 100.0) * (windowMins / burnRate)

	if timeToExhaustMins > 0 && timeToExhaustMins < 43200 { // cap at 30 days
		hours := timeToExhaustMins / 60.0
		eta := time.Now().Add(time.Duration(timeToExhaustMins) * time.Minute)
		br.ExhaustionETA = &eta
		br.ExhaustionIn = formatDuration(hours)
	}
}

// ──────────────────────────────────────────────
// Classification Helpers
// ──────────────────────────────────────────────

// classifyBudgetStatus returns the overall budget health.
func classifyBudgetStatus(consumed, remaining float64) string {
	switch {
	case consumed >= 100:
		return "exhausted"
	case consumed >= 80:
		return "critical"
	case consumed >= 50:
		return "burning"
	default:
		return "healthy"
	}
}

// classifyAlert implements Google SRE multi-window burn rate alerting.
// Fast burn: 1h > 14x AND 6h > 6x → page
// Slow burn: 6h > 6x → ticket
func classifyAlert(burnRate1h, burnRate6h float64) string {
	if burnRate1h >= 14.4 && burnRate6h >= 6 {
		return "page" // page immediately — fast burn detected
	}
	if burnRate6h >= 6 {
		return "ticket" // create ticket — slow burn
	}
	if burnRate1h >= 6 {
		return "watch" // watch closely — elevated short-term burn
	}
	return "none"
}

// determineTrend analyzes the burndown datapoints for trend.
func determineTrend(points []BurnPoint) string {
	if len(points) < 3 {
		return "stable"
	}

	// Compare first third vs last third
	third := len(points) / 3
	if third < 1 {
		return "stable"
	}

	var earlyAvg, lateAvg float64
	for i := 0; i < third; i++ {
		earlyAvg += points[i].BudgetRemaining
	}
	earlyAvg /= float64(third)

	for i := len(points) - third; i < len(points); i++ {
		lateAvg += points[i].BudgetRemaining
	}
	lateAvg /= float64(third)

	diff := lateAvg - earlyAvg
	switch {
	case diff > 5:
		return "improving"
	case diff < -10:
		return "degrading"
	default:
		return "stable"
	}
}

// recommendPolicy suggests action based on budget status and alert level.
func recommendPolicy(br BudgetReport) string {
	switch {
	case br.Status == "exhausted":
		return "🚨 FREEZE DEPLOYMENTS — Error budget exhausted. Focus on reliability."
	case br.Alert == "page":
		return "🔴 PAGE ON-CALL — Fast burn detected. Investigate immediately."
	case br.Alert == "ticket":
		return "🟠 CREATE TICKET — Slow burn in progress. Plan remediation."
	case br.Status == "critical":
		return "🟡 CAUTION — Budget nearly exhausted. Limit risky changes."
	case br.Status == "burning":
		return "⚠️ MONITOR — Budget consumption elevated. Review recent changes."
	default:
		return "✅ ALL CLEAR — Budget healthy. Ship with confidence."
	}
}

// ──────────────────────────────────────────────
// Formatting
// ──────────────────────────────────────────────

// FormatTerminal renders the budget report for terminal display.
func FormatTerminal(report *FullReport) string {
	var b strings.Builder

	b.WriteString(output.TitleStyle.Render("⚡ Error Budget Burndown"))
	b.WriteString("\n")
	b.WriteString(output.MutedStyle.Render(fmt.Sprintf("Instance: %s | %s", report.Instance, report.Timestamp)))
	b.WriteString("\n\n")

	for i, br := range report.Reports {
		if i > 0 {
			b.WriteString("\n" + strings.Repeat("─", 60) + "\n\n")
		}
		formatSingleReport(&b, br)
	}

	if report.AIInsight != "" {
		b.WriteString("\n" + strings.Repeat("─", 60) + "\n")
		b.WriteString(output.TitleStyle.Render("🤖 AI Recommendations"))
		b.WriteString("\n\n")
		b.WriteString(report.AIInsight)
		b.WriteString("\n")
	}

	return b.String()
}

func formatSingleReport(b *strings.Builder, br BudgetReport) {
	// Header
	sloName := br.SLO.Name
	if sloName == "" {
		sloName = fmt.Sprintf("%s (%s)", br.SLO.Service, br.SLO.Type)
	}
	b.WriteString(output.AccentStyle.Render(fmt.Sprintf("📊 %s", sloName)))
	b.WriteString("\n")
	b.WriteString(output.MutedStyle.Render(fmt.Sprintf("   Target: %.2f%% | Window: %s | Type: %s",
		br.SLO.Target, br.WindowLabel, br.SLO.Type)))
	b.WriteString("\n\n")

	// Budget gauge
	b.WriteString("   Budget: ")
	b.WriteString(renderBudgetGauge(br.BudgetRemaining, 30))
	b.WriteString(fmt.Sprintf(" %.1f%% remaining\n", br.BudgetRemaining))

	// Key metrics
	b.WriteString(fmt.Sprintf("   Availability: %s | Requests: %s | Errors: %s\n",
		formatAvailability(br.CurrentAvail, br.Status),
		formatNumber(br.TotalRequests),
		formatNumber(br.FailedRequests)))

	// Burn rate windows
	if len(br.BurnWindows) > 0 {
		b.WriteString("\n   Burn Rates:\n")
		for _, bw := range br.BurnWindows {
			icon := burnRateIcon(bw.BurnRate)
			b.WriteString(fmt.Sprintf("     %s %-4s  %sx burn  (%.3f%% error rate, %s reqs)\n",
				icon, bw.Label, formatBurnRate(bw.BurnRate), bw.ErrorRate, formatNumber(bw.TotalRequests)))
		}
	}

	// Exhaustion prediction
	if br.ExhaustionETA != nil {
		b.WriteString(fmt.Sprintf("\n   ⏳ Budget exhausts in %s (%s)\n",
			br.ExhaustionIn, br.ExhaustionETA.Format("Jan 02 15:04")))
	} else if br.Status == "exhausted" {
		b.WriteString("\n   💀 Budget EXHAUSTED\n")
	}

	// Alert status
	if br.Alert != "none" {
		b.WriteString(fmt.Sprintf("   🚨 Alert: %s\n", strings.ToUpper(br.Alert)))
	}

	// Trend
	trendIcon := trendEmoji(br.Trend)
	b.WriteString(fmt.Sprintf("   %s Trend: %s\n", trendIcon, br.Trend))

	// Policy
	b.WriteString(fmt.Sprintf("\n   %s\n", br.Policy))

	// Sparkline burndown
	if len(br.Datapoints) >= 3 {
		b.WriteString("\n   Burndown:\n")
		b.WriteString("   " + renderSparkline(br.Datapoints) + "\n")
	}
}

// FormatMarkdown renders the budget report as markdown.
func FormatMarkdown(report *FullReport) string {
	var b strings.Builder

	b.WriteString("# ⚡ Error Budget Burndown Report\n\n")
	b.WriteString(fmt.Sprintf("**Instance:** %s | **Generated:** %s\n\n", report.Instance, report.Timestamp))

	for _, br := range report.Reports {
		sloName := br.SLO.Name
		if sloName == "" {
			sloName = fmt.Sprintf("%s (%s)", br.SLO.Service, br.SLO.Type)
		}

		b.WriteString(fmt.Sprintf("## %s\n\n", sloName))
		b.WriteString("| Metric | Value |\n|--------|-------|\n")
		b.WriteString(fmt.Sprintf("| Target | %.2f%% |\n", br.SLO.Target))
		b.WriteString(fmt.Sprintf("| Current | %.3f%% |\n", br.CurrentAvail))
		b.WriteString(fmt.Sprintf("| Budget Remaining | %.1f%% |\n", br.BudgetRemaining))
		b.WriteString(fmt.Sprintf("| Status | %s |\n", br.Status))
		b.WriteString(fmt.Sprintf("| Trend | %s |\n", br.Trend))
		b.WriteString(fmt.Sprintf("| Total Requests | %s |\n", formatNumber(br.TotalRequests)))
		b.WriteString(fmt.Sprintf("| Failed Requests | %s |\n", formatNumber(br.FailedRequests)))

		if br.ExhaustionIn != "" {
			b.WriteString(fmt.Sprintf("| Exhaustion ETA | %s |\n", br.ExhaustionIn))
		}

		b.WriteString("\n### Burn Rates\n\n")
		b.WriteString("| Window | Burn Rate | Error Rate | Requests |\n|--------|-----------|------------|----------|\n")
		for _, bw := range br.BurnWindows {
			b.WriteString(fmt.Sprintf("| %s | %sx | %.3f%% | %s |\n",
				bw.Label, formatBurnRate(bw.BurnRate), bw.ErrorRate, formatNumber(bw.TotalRequests)))
		}

		if br.Alert != "none" {
			b.WriteString(fmt.Sprintf("\n> ⚠️ **Alert Level: %s**\n", strings.ToUpper(br.Alert)))
		}

		b.WriteString(fmt.Sprintf("\n**Policy:** %s\n\n", br.Policy))
		b.WriteString("---\n\n")
	}

	if report.AIInsight != "" {
		b.WriteString("## 🤖 AI Recommendations\n\n")
		b.WriteString(report.AIInsight + "\n")
	}

	return b.String()
}

// ──────────────────────────────────────────────
// Rendering Helpers
// ──────────────────────────────────────────────

func renderBudgetGauge(remaining float64, width int) string {
	filled := int(remaining / 100.0 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	switch {
	case remaining <= 0:
		return output.ErrorStyle.Render("[" + bar + "]")
	case remaining < 20:
		return output.ErrorStyle.Render("[" + bar + "]")
	case remaining < 50:
		return output.WarningStyle.Render("[" + bar + "]")
	default:
		return output.SuccessStyle.Render("[" + bar + "]")
	}
}

func renderSparkline(points []BurnPoint) string {
	if len(points) == 0 {
		return ""
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var spark strings.Builder

	for _, p := range points {
		idx := int(p.BudgetRemaining / 100.0 * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		spark.WriteRune(blocks[idx])
	}

	return spark.String()
}

func formatAvailability(avail float64, status string) string {
	s := fmt.Sprintf("%.3f%%", avail)
	switch status {
	case "exhausted", "critical":
		return output.ErrorStyle.Render(s)
	case "burning":
		return output.WarningStyle.Render(s)
	default:
		return output.SuccessStyle.Render(s)
	}
}

func formatBurnRate(rate float64) string {
	if rate == 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", rate)
}

func burnRateIcon(rate float64) string {
	switch {
	case rate >= 14:
		return "🔥"
	case rate >= 6:
		return "🟠"
	case rate >= 2:
		return "🟡"
	case rate > 0:
		return "🟢"
	default:
		return "⚪"
	}
}

func trendEmoji(trend string) string {
	switch trend {
	case "improving":
		return "📈"
	case "degrading":
		return "📉"
	default:
		return "➡️"
	}
}

func formatNumber(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func formatDuration(hours float64) string {
	if hours < 1 {
		return fmt.Sprintf("%.0f minutes", hours*60)
	}
	if hours < 24 {
		return fmt.Sprintf("%.1f hours", hours)
	}
	days := hours / 24
	if days < 7 {
		return fmt.Sprintf("%.1f days", days)
	}
	return fmt.Sprintf("%.0f days", days)
}

func parseWindow(w string) int {
	if w == "" {
		return 1440 // default 24h
	}
	w = strings.TrimSpace(strings.ToLower(w))
	var val int
	var unit string
	fmt.Sscanf(w, "%d%s", &val, &unit)
	if val == 0 {
		return 1440
	}
	switch unit {
	case "m", "min":
		return val
	case "h", "hr":
		return val * 60
	case "d", "day":
		return val * 1440
	default:
		return 1440
	}
}

// ──────────────────────────────────────────────
// AI Integration
// ──────────────────────────────────────────────

func generateAIInsight(_ context.Context, report *FullReport, provider ai.Provider) (string, error) {
	var summary strings.Builder

	summary.WriteString("Error Budget Burndown Analysis:\n\n")
	for _, br := range report.Reports {
		sloName := br.SLO.Name
		if sloName == "" {
			sloName = br.SLO.Service
		}
		summary.WriteString(fmt.Sprintf("SLO: %s (target: %.2f%%)\n", sloName, br.SLO.Target))
		summary.WriteString(fmt.Sprintf("  Current: %.3f%% | Budget remaining: %.1f%%\n", br.CurrentAvail, br.BudgetRemaining))
		summary.WriteString(fmt.Sprintf("  Status: %s | Alert: %s | Trend: %s\n", br.Status, br.Alert, br.Trend))
		summary.WriteString(fmt.Sprintf("  Requests: %d total, %d failed\n", br.TotalRequests, br.FailedRequests))
		for _, bw := range br.BurnWindows {
			summary.WriteString(fmt.Sprintf("  Burn rate %s: %.1fx (%.3f%% errors)\n", bw.Label, bw.BurnRate, bw.ErrorRate))
		}
		if br.ExhaustionIn != "" {
			summary.WriteString(fmt.Sprintf("  Exhaustion ETA: %s\n", br.ExhaustionIn))
		}
		summary.WriteString("\n")
	}

	prompt := fmt.Sprintf(`You are a senior SRE analyzing error budget burndown data. Based on this data, provide:
1. Key observations (what stands out)
2. Risk assessment (what could go wrong)
3. Specific action items (what to do NOW)
4. Deployment recommendation (ship or hold?)

Be concise, opinionated, and actionable. No fluff.

Data:
%s`, summary.String())

	analyzer := ai.NewFromProvider(provider)
	var buf bytes.Buffer
	if err := analyzer.Analyze(prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ExitCode returns non-zero if any SLO is in a bad state.
func (r *FullReport) ExitCode() int {
	for _, br := range r.Reports {
		switch br.Status {
		case "exhausted":
			return 2
		case "critical":
			return 1
		}
	}
	for _, br := range r.Reports {
		if br.Alert == "page" {
			return 2
		}
		if br.Alert == "ticket" {
			return 1
		}
	}
	return 0
}

// ──────────────────────────────────────────────
// Sorting helpers
// ──────────────────────────────────────────────

// SortByUrgency sorts reports with most critical first.
func SortByUrgency(reports []BudgetReport) {
	sort.Slice(reports, func(i, j int) bool {
		pi := statusPriority(reports[i].Status)
		pj := statusPriority(reports[j].Status)
		if pi != pj {
			return pi > pj
		}
		return reports[i].BudgetRemaining < reports[j].BudgetRemaining
	})
}

func statusPriority(status string) int {
	switch status {
	case "exhausted":
		return 4
	case "critical":
		return 3
	case "burning":
		return 2
	case "healthy":
		return 1
	default:
		return 0
	}
}

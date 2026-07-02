package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/textutil"
	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────

// ServiceQuerier is the interface for querying Signoz data.
type ServiceQuerier interface {
	ListServices(ctx context.Context) ([]types.Service, error)
	QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
	QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error)
}

// Options configures the deployment guard check.
type Options struct {
	Service    string // optional: check specific service only
	Strict     bool   // strict mode: lower thresholds, block on warnings
	Format     string // "terminal", "markdown", or "json"
	WithAI     bool   // include AI deployment advisory
	AIProvider ai.Provider
	// Thresholds (overridable, zero = defaults)
	MaxErrorRate  float64 // max acceptable error rate %
	MaxP99Latency float64 // max acceptable P99 ms
	MinCallVolume int     // minimum calls to consider service active
}

// Verdict is the final deploy/no-deploy decision.
type Verdict string

const (
	VerdictShip    Verdict = "SHIP"    // safe to deploy
	VerdictCaution Verdict = "CAUTION" // deploy with care
	VerdictHold    Verdict = "HOLD"    // do not deploy
)

// CheckResult is the result of a single guard check.
type CheckResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // pass, warn, fail
	Detail   string `json:"detail"`
	Impact   string `json:"impact,omitempty"` // what happens if ignored
	Severity int    `json:"severity"`         // 0=info, 1=warn, 2=critical
}

// ServiceGuardResult is per-service guard analysis.
type ServiceGuardResult struct {
	Service    string  `json:"service"`
	ErrorRate  float64 `json:"error_rate_pct"`
	NumCalls   int     `json:"num_calls"`
	NumErrors  int     `json:"num_errors"`
	P99Latency float64 `json:"p99_latency_ms"`
	Status     string  `json:"status"` // healthy, degraded, unhealthy
}

// GuardReport is the full deployment gate report.
type GuardReport struct {
	Timestamp  string               `json:"timestamp"`
	Instance   string               `json:"instance"`
	Verdict    Verdict              `json:"verdict"`
	Score      int                  `json:"score"` // 0-100 deployment confidence
	Checks     []CheckResult        `json:"checks"`
	Services   []ServiceGuardResult `json:"services"`
	Summary    string               `json:"summary"`
	AIAdvisory string               `json:"ai_advisory,omitempty"`
	StrictMode bool                 `json:"strict_mode"`
}

// ──────────────────────────────────────────────
// Default Thresholds
// ──────────────────────────────────────────────

const (
	defaultMaxErrorRate   = 5.0  // 5% error rate → block
	defaultWarnErrorRate  = 1.0  // 1% error rate → caution
	defaultMaxP99Latency  = 5000 // 5s P99 → block
	defaultWarnP99Latency = 2000 // 2s P99 → caution
	defaultMinCallVolume  = 10   // ignore services with < 10 calls
	strictMaxErrorRate    = 1.0  // strict: 1% → block
	strictWarnErrorRate   = 0.5  // strict: 0.5% → caution
	strictMaxP99Latency   = 2000 // strict: 2s → block
	strictWarnP99Latency  = 1000 // strict: 1s → caution
)

// ──────────────────────────────────────────────
// Guard Analyzer
// ──────────────────────────────────────────────

// Analyzer runs deployment guard checks.
type Analyzer struct {
	client  ServiceQuerier
	instKey string
}

// NewAnalyzer creates a guard analyzer.
func NewAnalyzer(client ServiceQuerier, instKey string) *Analyzer {
	return &Analyzer{client: client, instKey: instKey}
}

// Check runs all deployment guard checks and returns a verdict.
func (a *Analyzer) Check(ctx context.Context, opts Options) (*GuardReport, error) {
	services, err := a.client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching services: %w", err)
	}

	report := &GuardReport{
		Timestamp:  time.Now().Format(time.RFC3339),
		Instance:   a.instKey,
		StrictMode: opts.Strict,
	}

	// Resolve thresholds
	maxErr, warnErr, maxP99, warnP99, minCalls := resolveThresholds(opts)

	// Filter services if specified
	if opts.Service != "" {
		var filtered []types.Service
		for _, svc := range services {
			if strings.EqualFold(svc.Name, opts.Service) {
				filtered = append(filtered, svc)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("service %q not found", opts.Service)
		}
		services = filtered
	}

	// Check 1: Overall system health
	report.Checks = append(report.Checks, a.checkSystemHealth(services, minCalls))

	// Check 2: Error rate analysis
	report.Checks = append(report.Checks, a.checkErrorRates(services, maxErr, warnErr, minCalls))

	// Check 3: Latency analysis (from trace data)
	latencyCheck, p99Map := a.checkLatency(ctx, services, maxP99, warnP99, minCalls)
	report.Checks = append(report.Checks, latencyCheck)

	// Check 4: Error spikes (recent log analysis)
	spikeCheck, err := a.checkErrorSpikes(ctx, services, minCalls)
	if err == nil {
		report.Checks = append(report.Checks, spikeCheck)
	}

	// Check 5: Service saturation
	report.Checks = append(report.Checks, a.checkSaturation(services, minCalls))

	// Build per-service results
	for _, svc := range services {
		if svc.NumCalls < minCalls {
			continue
		}
		errRate := 0.0
		if svc.NumCalls > 0 {
			errRate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}
		status := "healthy"
		if errRate >= maxErr {
			status = "unhealthy"
		} else if errRate >= warnErr {
			status = "degraded"
		}
		p99 := p99Map[svc.Name]
		if p99 >= maxP99 && status == "healthy" {
			status = "degraded"
		}
		report.Services = append(report.Services, ServiceGuardResult{
			Service:    svc.Name,
			ErrorRate:  errRate,
			NumCalls:   svc.NumCalls,
			NumErrors:  svc.NumErrors,
			P99Latency: p99,
			Status:     status,
		})
	}

	// Calculate score and verdict
	report.Score = calculateScore(report.Checks)
	report.Verdict = determineVerdict(report.Score, opts.Strict)
	report.Summary = buildSummary(report)

	// AI advisory if requested
	if opts.WithAI && opts.AIProvider != nil {
		advisory, err := generateAdvisory(ctx, report, opts.AIProvider)
		if err == nil {
			report.AIAdvisory = advisory
		}
	}

	return report, nil
}

// ──────────────────────────────────────────────
// Individual Checks
// ──────────────────────────────────────────────

func (a *Analyzer) checkSystemHealth(services []types.Service, minCalls int) CheckResult {
	active := 0
	down := 0
	for _, svc := range services {
		if svc.NumCalls >= minCalls {
			active++
			if svc.NumErrors > 0 && float64(svc.NumErrors)/float64(svc.NumCalls) > 0.9 {
				down++
			}
		}
	}

	if active == 0 {
		return CheckResult{
			Name:     "System Health",
			Status:   "warn",
			Detail:   "No active services detected (no traffic)",
			Impact:   "Cannot assess system stability",
			Severity: 1,
		}
	}

	if down > 0 {
		return CheckResult{
			Name:     "System Health",
			Status:   "fail",
			Detail:   fmt.Sprintf("%d of %d services appear down (>90%% error rate)", down, active),
			Impact:   "Deploying during an outage risks making things worse",
			Severity: 2,
		}
	}

	return CheckResult{
		Name:     "System Health",
		Status:   "pass",
		Detail:   fmt.Sprintf("%d services active, all responding", active),
		Severity: 0,
	}
}

func (a *Analyzer) checkErrorRates(services []types.Service, maxErr, warnErr float64, minCalls int) CheckResult {
	var highErrorServices []string
	var warnErrorServices []string
	maxObservedRate := 0.0

	for _, svc := range services {
		if svc.NumCalls < minCalls {
			continue
		}
		errRate := float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		if errRate > maxObservedRate {
			maxObservedRate = errRate
		}
		if errRate >= maxErr {
			highErrorServices = append(highErrorServices, fmt.Sprintf("%s (%.1f%%)", svc.Name, errRate))
		} else if errRate >= warnErr {
			warnErrorServices = append(warnErrorServices, fmt.Sprintf("%s (%.1f%%)", svc.Name, errRate))
		}
	}

	if len(highErrorServices) > 0 {
		return CheckResult{
			Name:     "Error Rates",
			Status:   "fail",
			Detail:   fmt.Sprintf("Critical error rates: %s", strings.Join(highErrorServices, ", ")),
			Impact:   "Deploying with high error rates may trigger cascading failures",
			Severity: 2,
		}
	}

	if len(warnErrorServices) > 0 {
		return CheckResult{
			Name:     "Error Rates",
			Status:   "warn",
			Detail:   fmt.Sprintf("Elevated error rates: %s", strings.Join(warnErrorServices, ", ")),
			Impact:   "Existing errors may mask new deployment issues",
			Severity: 1,
		}
	}

	return CheckResult{
		Name:     "Error Rates",
		Status:   "pass",
		Detail:   fmt.Sprintf("All services below %.1f%% error rate (max observed: %.2f%%)", warnErr, maxObservedRate),
		Severity: 0,
	}
}

func (a *Analyzer) checkLatency(ctx context.Context, services []types.Service, maxP99, warnP99 float64, minCalls int) (CheckResult, map[string]float64) {
	p99Map := make(map[string]float64)
	var highLatencyServices []string
	var warnLatencyServices []string
	maxObservedP99 := 0.0

	for _, svc := range services {
		if svc.NumCalls < minCalls {
			continue
		}
		// Sample traces to compute P99
		result, err := a.client.QueryTraces(ctx, svc.Name, 30, 100)
		if err != nil || result == nil || len(result.Traces) == 0 {
			continue
		}
		p99 := computeP99(result.Traces)
		p99Map[svc.Name] = p99

		if p99 > maxObservedP99 {
			maxObservedP99 = p99
		}
		if p99 >= maxP99 {
			highLatencyServices = append(highLatencyServices, fmt.Sprintf("%s (%.0fms)", svc.Name, p99))
		} else if p99 >= warnP99 {
			warnLatencyServices = append(warnLatencyServices, fmt.Sprintf("%s (%.0fms)", svc.Name, p99))
		}
	}

	if len(highLatencyServices) > 0 {
		return CheckResult{
			Name:     "Latency (P99)",
			Status:   "fail",
			Detail:   fmt.Sprintf("Critical P99 latency: %s", strings.Join(highLatencyServices, ", ")),
			Impact:   "High latency indicates saturation; deployment adds risk",
			Severity: 2,
		}, p99Map
	}

	if len(warnLatencyServices) > 0 {
		return CheckResult{
			Name:     "Latency (P99)",
			Status:   "warn",
			Detail:   fmt.Sprintf("Elevated P99 latency: %s", strings.Join(warnLatencyServices, ", ")),
			Impact:   "Latency spikes may worsen with deployment overhead",
			Severity: 1,
		}, p99Map
	}

	detail := "All services within acceptable P99 latency"
	if maxObservedP99 > 0 {
		detail = fmt.Sprintf("All services within acceptable P99 latency (max: %.0fms)", maxObservedP99)
	}
	return CheckResult{
		Name:     "Latency (P99)",
		Status:   "pass",
		Detail:   detail,
		Severity: 0,
	}, p99Map
}

// computeP99 calculates the 99th percentile latency from trace entries.
func computeP99(traces []types.TraceEntry) float64 {
	if len(traces) == 0 {
		return 0
	}
	durations := make([]float64, len(traces))
	for i, t := range traces {
		durations[i] = t.DurationMs()
	}
	// Sort for percentile calculation
	sortFloats(durations)
	idx := int(math.Ceil(float64(len(durations))*0.99)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	return durations[idx]
}

// sortFloats sorts a float64 slice in ascending order.
func sortFloats(data []float64) {
	for i := 1; i < len(data); i++ {
		for j := i; j > 0 && data[j] < data[j-1]; j-- {
			data[j], data[j-1] = data[j-1], data[j]
		}
	}
}

func (a *Analyzer) checkErrorSpikes(ctx context.Context, services []types.Service, minCalls int) (CheckResult, error) {
	// Query recent error logs (last 30 min) across all services
	result, err := a.client.QueryLogs(ctx, "", 30, 100, "error")
	if err != nil {
		return CheckResult{}, err
	}

	errorCount := len(result.Logs)

	// Count unique error patterns
	patterns := make(map[string]int)
	for _, log := range result.Logs {
		patterns[textutil.Truncate(log.Body, 80)]++
	}

	if errorCount > 50 {
		topPatterns := topNPatterns(patterns, 3)
		return CheckResult{
			Name:     "Error Spikes",
			Status:   "fail",
			Detail:   fmt.Sprintf("%d errors in last 30 min across %d patterns. Top: %s", errorCount, len(patterns), topPatterns),
			Impact:   "Active error storm — deploying now will be impossible to debug",
			Severity: 2,
		}, nil
	}

	if errorCount > 20 {
		return CheckResult{
			Name:     "Error Spikes",
			Status:   "warn",
			Detail:   fmt.Sprintf("%d errors in last 30 min across %d patterns", errorCount, len(patterns)),
			Impact:   "Moderate error activity may complicate deployment diagnosis",
			Severity: 1,
		}, nil
	}

	return CheckResult{
		Name:     "Error Spikes",
		Status:   "pass",
		Detail:   fmt.Sprintf("%d errors in last 30 min — baseline normal", errorCount),
		Severity: 0,
	}, nil
}

func (a *Analyzer) checkSaturation(services []types.Service, minCalls int) CheckResult {
	// Check if any service has unusually high call volume (potential saturation)
	if len(services) == 0 {
		return CheckResult{
			Name:     "Saturation",
			Status:   "pass",
			Detail:   "No services to check",
			Severity: 0,
		}
	}

	// Calculate mean and stddev of call volumes
	var callVolumes []float64
	for _, svc := range services {
		if svc.NumCalls >= minCalls {
			callVolumes = append(callVolumes, float64(svc.NumCalls))
		}
	}

	if len(callVolumes) < 2 {
		return CheckResult{
			Name:     "Saturation",
			Status:   "pass",
			Detail:   "Insufficient services for saturation analysis",
			Severity: 0,
		}
	}

	// Use median-based detection: flag services >10x median call volume
	sortFloats(callVolumes)
	median := callVolumes[len(callVolumes)/2]
	threshold := median * 10 // 10x the median is suspicious
	if threshold < 100 {
		threshold = 100 // minimum threshold
	}

	var saturated []string
	for _, svc := range services {
		if svc.NumCalls < minCalls {
			continue
		}
		if float64(svc.NumCalls) > threshold && float64(svc.NumCalls) > median*10 {
			saturated = append(saturated, fmt.Sprintf("%s (%s calls)", svc.Name, formatNumber(svc.NumCalls)))
		}
	}

	if len(saturated) > 0 {
		return CheckResult{
			Name:     "Saturation",
			Status:   "warn",
			Detail:   fmt.Sprintf("Unusually high traffic on: %s", strings.Join(saturated, ", ")),
			Impact:   "High-traffic services are more sensitive to deployment issues",
			Severity: 1,
		}
	}

	return CheckResult{
		Name:     "Saturation",
		Status:   "pass",
		Detail:   fmt.Sprintf("Traffic distribution normal across %d services", len(callVolumes)),
		Severity: 0,
	}
}

// ──────────────────────────────────────────────
// Scoring & Verdict
// ──────────────────────────────────────────────

func calculateScore(checks []CheckResult) int {
	if len(checks) == 0 {
		return 50
	}

	score := 100
	for _, c := range checks {
		switch c.Severity {
		case 2: // critical
			score -= 35
		case 1: // warning
			score -= 15
		}
	}

	if score < 0 {
		score = 0
	}
	return score
}

func determineVerdict(score int, strict bool) Verdict {
	if strict {
		if score >= 90 {
			return VerdictShip
		}
		if score >= 70 {
			return VerdictCaution
		}
		return VerdictHold
	}

	if score >= 70 {
		return VerdictShip
	}
	if score >= 40 {
		return VerdictCaution
	}
	return VerdictHold
}

func buildSummary(report *GuardReport) string {
	fails := 0
	warns := 0
	passes := 0
	for _, c := range report.Checks {
		switch c.Status {
		case "fail":
			fails++
		case "warn":
			warns++
		case "pass":
			passes++
		}
	}

	switch report.Verdict {
	case VerdictShip:
		return fmt.Sprintf("✅ All clear — %d checks passed, %d warnings. Safe to deploy.", passes, warns)
	case VerdictCaution:
		return fmt.Sprintf("⚠️ Proceed with caution — %d warnings, %d failures. Monitor closely after deploy.", warns, fails)
	case VerdictHold:
		return fmt.Sprintf("🛑 Hold deployment — %d checks failed, %d warnings. Fix issues first.", fails, warns)
	default:
		return "Unknown verdict"
	}
}

// ──────────────────────────────────────────────
// Output Formatting
// ──────────────────────────────────────────────

// FormatTerminal renders the guard report for terminal display.
func FormatTerminal(report *GuardReport) string {
	var b strings.Builder

	b.WriteString(output.TitleStyle.Render("🛡️  Deployment Guard"))
	b.WriteString("\n")
	b.WriteString(output.MutedStyle.Render(fmt.Sprintf("Instance: %s | %s", report.Instance, report.Timestamp)))
	if report.StrictMode {
		b.WriteString(output.WarningStyle.Render("  [STRICT MODE]"))
	}
	b.WriteString("\n\n")

	// Verdict banner
	verdictBanner := renderVerdict(report.Verdict, report.Score)
	b.WriteString(verdictBanner)
	b.WriteString("\n\n")

	// Checks
	b.WriteString(output.AccentStyle.Render("Checks"))
	b.WriteString("\n")
	for _, c := range report.Checks {
		icon := checkIcon(c.Status)
		b.WriteString(fmt.Sprintf("  %s %s\n", icon, c.Name))
		b.WriteString(fmt.Sprintf("    %s\n", c.Detail))
		if c.Impact != "" {
			b.WriteString(fmt.Sprintf("    %s\n", output.MutedStyle.Render("→ "+c.Impact)))
		}
		b.WriteString("\n")
	}

	// Service health table
	if len(report.Services) > 0 {
		b.WriteString(output.AccentStyle.Render("Service Health"))
		b.WriteString("\n")
		// Header
		b.WriteString(fmt.Sprintf("  %-25s %10s %10s %10s %10s\n",
			"Service", "Calls", "Errors", "Error%", "P99(ms)"))
		b.WriteString("  " + strings.Repeat("─", 67) + "\n")
		for _, svc := range report.Services {
			statusIcon := serviceStatusIcon(svc.Status)
			errRateStr := fmt.Sprintf("%.2f%%", svc.ErrorRate)
			p99Str := fmt.Sprintf("%.0f", svc.P99Latency)
			b.WriteString(fmt.Sprintf("  %s %-23s %10s %10s %10s %10s\n",
				statusIcon,
				truncate(svc.Service, 23),
				formatNumber(svc.NumCalls),
				formatNumber(svc.NumErrors),
				errRateStr,
				p99Str))
		}
		b.WriteString("\n")
	}

	// Summary
	b.WriteString(report.Summary)
	b.WriteString("\n")

	// AI Advisory
	if report.AIAdvisory != "" {
		b.WriteString("\n" + strings.Repeat("─", 60) + "\n")
		b.WriteString(output.TitleStyle.Render("🤖 AI Deployment Advisory"))
		b.WriteString("\n\n")
		b.WriteString(report.AIAdvisory)
		b.WriteString("\n")
	}

	return b.String()
}

// FormatMarkdown renders the guard report as markdown.
func FormatMarkdown(report *GuardReport) string {
	var b strings.Builder

	b.WriteString("# 🛡️ Deployment Guard Report\n\n")
	b.WriteString(fmt.Sprintf("**Instance:** %s | **Time:** %s", report.Instance, report.Timestamp))
	if report.StrictMode {
		b.WriteString(" | **Mode:** STRICT")
	}
	b.WriteString("\n\n")

	// Verdict
	emoji := verdictEmoji(report.Verdict)
	b.WriteString(fmt.Sprintf("## %s Verdict: %s (Score: %d/100)\n\n", emoji, report.Verdict, report.Score))
	b.WriteString(fmt.Sprintf("%s\n\n", report.Summary))

	// Checks table
	b.WriteString("## Checks\n\n")
	b.WriteString("| Status | Check | Detail |\n|--------|-------|--------|\n")
	for _, c := range report.Checks {
		icon := checkEmoji(c.Status)
		b.WriteString(fmt.Sprintf("| %s | %s | %s |\n", icon, c.Name, c.Detail))
	}
	b.WriteString("\n")

	// Services table
	if len(report.Services) > 0 {
		b.WriteString("## Service Health\n\n")
		b.WriteString("| Service | Calls | Errors | Error Rate | P99 (ms) | Status |\n")
		b.WriteString("|---------|-------|--------|------------|----------|--------|\n")
		for _, svc := range report.Services {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %.2f%% | %.0f | %s |\n",
				svc.Service, formatNumber(svc.NumCalls), formatNumber(svc.NumErrors),
				svc.ErrorRate, svc.P99Latency, svc.Status))
		}
		b.WriteString("\n")
	}

	if report.AIAdvisory != "" {
		b.WriteString("## 🤖 AI Deployment Advisory\n\n")
		b.WriteString(report.AIAdvisory + "\n")
	}

	return b.String()
}

// FormatJSON returns the report as formatted JSON.
func FormatJSON(report *GuardReport) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ──────────────────────────────────────────────
// Rendering Helpers
// ──────────────────────────────────────────────

func renderVerdict(v Verdict, score int) string {
	gauge := renderConfidenceGauge(score, 30)
	switch v {
	case VerdictShip:
		return output.SuccessStyle.Render(fmt.Sprintf("  🚀 SHIP IT  ")) +
			fmt.Sprintf("  Confidence: %s %d/100", gauge, score)
	case VerdictCaution:
		return output.WarningStyle.Render(fmt.Sprintf("  ⚠️  CAUTION  ")) +
			fmt.Sprintf("  Confidence: %s %d/100", gauge, score)
	case VerdictHold:
		return output.ErrorStyle.Render(fmt.Sprintf("  🛑 HOLD     ")) +
			fmt.Sprintf("  Confidence: %s %d/100", gauge, score)
	default:
		return "Unknown"
	}
}

func renderConfidenceGauge(score, width int) string {
	filled := score * width / 100
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	switch {
	case score >= 70:
		return output.SuccessStyle.Render("[" + bar + "]")
	case score >= 40:
		return output.WarningStyle.Render("[" + bar + "]")
	default:
		return output.ErrorStyle.Render("[" + bar + "]")
	}
}

func checkIcon(status string) string {
	switch status {
	case "pass":
		return "✅"
	case "warn":
		return "⚠️"
	case "fail":
		return "❌"
	default:
		return "❓"
	}
}

func checkEmoji(status string) string {
	switch status {
	case "pass":
		return "✅"
	case "warn":
		return "⚠️"
	case "fail":
		return "❌"
	default:
		return "❓"
	}
}

func verdictEmoji(v Verdict) string {
	switch v {
	case VerdictShip:
		return "🟢"
	case VerdictCaution:
		return "🟡"
	case VerdictHold:
		return "🔴"
	default:
		return "❓"
	}
}

func serviceStatusIcon(status string) string {
	switch status {
	case "healthy":
		return "🟢"
	case "degraded":
		return "🟡"
	case "unhealthy":
		return "🔴"
	default:
		return "⚪"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
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

func topNPatterns(patterns map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range patterns {
		sorted = append(sorted, kv{k, v})
	}
	// Simple sort by count descending
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].v > sorted[i].v {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	var parts []string
	for i, kv := range sorted {
		if i >= n {
			break
		}
		label := kv.k
		if len(label) > 40 {
			label = label[:40] + "…"
		}
		parts = append(parts, fmt.Sprintf("%q (%dx)", label, kv.v))
	}
	return strings.Join(parts, ", ")
}

func meanStdDev(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))

	return mean, math.Sqrt(variance)
}

func resolveThresholds(opts Options) (maxErr, warnErr, maxP99, warnP99 float64, minCalls int) {
	if opts.Strict {
		maxErr = strictMaxErrorRate
		warnErr = strictWarnErrorRate
		maxP99 = strictMaxP99Latency
		warnP99 = strictWarnP99Latency
	} else {
		maxErr = defaultMaxErrorRate
		warnErr = defaultWarnErrorRate
		maxP99 = defaultMaxP99Latency
		warnP99 = defaultWarnP99Latency
	}

	// Allow overrides
	if opts.MaxErrorRate > 0 {
		maxErr = opts.MaxErrorRate
	}
	if opts.MaxP99Latency > 0 {
		maxP99 = opts.MaxP99Latency
	}

	minCalls = defaultMinCallVolume
	if opts.MinCallVolume > 0 {
		minCalls = opts.MinCallVolume
	}

	return
}

// ──────────────────────────────────────────────
// AI Advisory
// ──────────────────────────────────────────────

func generateAdvisory(_ context.Context, report *GuardReport, provider ai.Provider) (string, error) {
	var summary strings.Builder

	summary.WriteString(fmt.Sprintf("Deployment Guard Report (verdict: %s, score: %d/100, strict: %v):\n\n",
		report.Verdict, report.Score, report.StrictMode))

	summary.WriteString("Checks:\n")
	for _, c := range report.Checks {
		summary.WriteString(fmt.Sprintf("  [%s] %s: %s\n", c.Status, c.Name, c.Detail))
		if c.Impact != "" {
			summary.WriteString(fmt.Sprintf("    Impact: %s\n", c.Impact))
		}
	}

	summary.WriteString("\nServices:\n")
	for _, svc := range report.Services {
		summary.WriteString(fmt.Sprintf("  %s: %d calls, %d errors (%.2f%%), P99: %.0fms [%s]\n",
			svc.Service, svc.NumCalls, svc.NumErrors, svc.ErrorRate, svc.P99Latency, svc.Status))
	}

	prompt := fmt.Sprintf(`You are a senior SRE reviewing deployment readiness. Based on this data, provide:
1. Go/no-go recommendation with reasoning (1-2 sentences)
2. If deploying: what to watch for in the first 15 minutes
3. If holding: what needs to happen before deploying
4. Risk factors specific to these services

Be direct, opinionated, and concise. No fluff. Think like an SRE who's been paged at 3am before.

Data:
%s`, summary.String())

	analyzer := ai.NewFromProvider(provider)
	var buf bytes.Buffer
	if err := analyzer.Analyze(prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ExitCode returns non-zero based on verdict.
// 0 = SHIP, 1 = CAUTION, 2 = HOLD
func (r *GuardReport) ExitCode() int {
	switch r.Verdict {
	case VerdictHold:
		return 2
	case VerdictCaution:
		return 1
	default:
		return 0
	}
}

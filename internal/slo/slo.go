package slo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// Config Types
// ──────────────────────────────────────────────

// SLOConfig holds all SLO definitions.
type SLOConfig struct {
	SLOs []SLO `yaml:"slos" json:"slos"`
}

// SLO defines a single Service Level Objective.
type SLO struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Service     string            `yaml:"service" json:"service"`
	Type        string            `yaml:"type" json:"type"` // availability, latency
	Target      float64           `yaml:"target" json:"target"` // e.g. 99.9 for 99.9%
	Window      string            `yaml:"window" json:"window"` // 1h, 24h, 7d, 30d
	Threshold   float64           `yaml:"threshold,omitempty" json:"threshold,omitempty"` // for latency: max ms
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Enabled     *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// IsEnabled returns whether the SLO is active.
func (s SLO) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// WindowMinutes parses the window string to minutes.
func (s SLO) WindowMinutes() int {
	w := strings.TrimSpace(s.Window)
	if w == "" {
		return 1440 // default 24h
	}
	var val int
	var unit string
	fmt.Sscanf(w, "%d%s", &val, &unit)
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
// SLO Result Types
// ──────────────────────────────────────────────

// Result holds the evaluation of a single SLO.
type Result struct {
	SLO            SLO     `json:"slo"`
	Current        float64 `json:"current"`         // current value (e.g. 99.85%)
	Target         float64 `json:"target"`           // target (e.g. 99.9%)
	ErrorBudget    float64 `json:"error_budget"`     // total error budget (%)
	BudgetConsumed float64 `json:"budget_consumed"`  // how much budget used (%)
	BudgetRemain   float64 `json:"budget_remaining"` // remaining budget (%)
	BurnRate       float64 `json:"burn_rate"`        // current burn rate multiplier
	Status         string  `json:"status"`           // ok, warning, critical, exhausted
	TotalRequests  int     `json:"total_requests"`
	FailedRequests int     `json:"failed_requests"`
	WindowMinutes  int     `json:"window_minutes"`
}

// Report holds results for all SLOs.
type Report struct {
	Timestamp string   `json:"timestamp"`
	Instance  string   `json:"instance"`
	Results   []Result `json:"results"`
}

// ──────────────────────────────────────────────
// Config Management
// ──────────────────────────────────────────────

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus")
}

func configPath() string {
	return filepath.Join(configDir(), "slos.yaml")
}

// InitSLOs creates a sample SLO config.
func InitSLOs() error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	sample := `# Argus SLO Definitions
# Define your Service Level Objectives here.
#
# Types:
#   availability - Tracks error rate (1 - error_rate = availability)
#   latency      - Tracks P99 latency against a threshold
#
# Windows: 1h, 6h, 24h, 7d, 30d
#
# Target: percentage (e.g. 99.9 = 99.9% availability)

slos:
  - name: "API Availability"
    description: "API service should be 99.9% available over 24h"
    service: ""  # empty = all services combined
    type: availability
    target: 99.9
    window: 24h
    labels:
      team: platform
      tier: "1"

  - name: "API Latency P99"
    description: "P99 latency should be under 500ms for 99% of requests"
    service: ""
    type: latency
    target: 99.0
    threshold: 500  # milliseconds
    window: 24h
    labels:
      team: platform

  - name: "Auth Service Availability"
    description: "Auth service 99.95% available over 7 days"
    service: "auth-service"
    type: availability
    target: 99.95
    window: 7d
    labels:
      team: identity
      tier: "0"
`
	return os.WriteFile(configPath(), []byte(sample), 0644)
}

// LoadSLOs loads SLO config from disk.
func LoadSLOs() (*SLOConfig, error) {
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no SLO config found. Run: argus slo init")
		}
		return nil, err
	}
	var cfg SLOConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing SLO config: %w", err)
	}
	return &cfg, nil
}

// ──────────────────────────────────────────────
// Checker
// ──────────────────────────────────────────────

// Checker evaluates SLOs against Signoz data.
type Checker struct {
	client   signoz.SignozQuerier
	instance string
}

// NewChecker creates an SLO checker.
func NewChecker(client signoz.SignozQuerier, instKey string) *Checker {
	return &Checker{
		client:   client,
		instance: instKey,
	}
}

// CheckAll evaluates all enabled SLOs and returns a report.
func (c *Checker) CheckAll(ctx context.Context, cfg *SLOConfig) (*Report, error) {
	report := &Report{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Instance:  c.instance,
	}

	// Fetch services once for availability SLOs
	services, err := c.client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching services: %w", err)
	}

	for _, slo := range cfg.SLOs {
		if !slo.IsEnabled() {
			continue
		}

		var result Result
		switch slo.Type {
		case "availability":
			result = c.checkAvailability(slo, services)
		case "latency":
			result = c.checkLatency(ctx, slo, services)
		default:
			continue
		}

		report.Results = append(report.Results, result)
	}

	return report, nil
}

// checkAvailability evaluates an availability SLO using service error rates.
func (c *Checker) checkAvailability(slo SLO, services []types.Service) Result {
	result := Result{
		SLO:           slo,
		Target:        slo.Target,
		WindowMinutes: slo.WindowMinutes(),
	}

	var totalCalls, totalErrors int

	if slo.Service == "" {
		// All services combined
		for _, svc := range services {
			totalCalls += svc.NumCalls
			totalErrors += svc.NumErrors
		}
	} else {
		for _, svc := range services {
			if strings.EqualFold(svc.Name, slo.Service) {
				totalCalls = svc.NumCalls
				totalErrors = svc.NumErrors
				break
			}
		}
	}

	result.TotalRequests = totalCalls
	result.FailedRequests = totalErrors

	if totalCalls == 0 {
		result.Current = 100.0
		result.Status = "ok"
		result.ErrorBudget = 100.0 - slo.Target
		result.BudgetRemain = result.ErrorBudget
		result.BurnRate = 0
		return result
	}

	errorRate := float64(totalErrors) / float64(totalCalls) * 100
	result.Current = 100.0 - errorRate

	// Error budget calculation
	result.ErrorBudget = 100.0 - slo.Target // e.g. 0.1% for 99.9%
	if result.ErrorBudget > 0 {
		result.BudgetConsumed = (errorRate / result.ErrorBudget) * 100
		result.BudgetRemain = math.Max(0, 100-result.BudgetConsumed)
		result.BurnRate = errorRate / result.ErrorBudget
	}

	// Status based on budget consumption
	result.Status = classifyStatus(result.BudgetConsumed)

	return result
}

// checkLatency evaluates a latency SLO using trace data.
func (c *Checker) checkLatency(ctx context.Context, slo SLO, services []types.Service) Result {
	result := Result{
		SLO:           slo,
		Target:        slo.Target,
		WindowMinutes: slo.WindowMinutes(),
	}

	// Query traces for the service
	service := slo.Service
	dur := slo.WindowMinutes()
	if dur > 1440 {
		dur = 1440 // cap trace queries at 24h for perf
	}

	traceResult, err := c.client.QueryTraces(ctx, service, dur, 1000)
	if err != nil || len(traceResult.Traces) == 0 {
		result.Current = 100.0
		result.Status = "ok"
		result.ErrorBudget = 100.0 - slo.Target
		result.BudgetRemain = result.ErrorBudget
		return result
	}

	// Count how many traces are under threshold
	total := len(traceResult.Traces)
	underThreshold := 0
	for _, t := range traceResult.Traces {
		if t.DurationMs() <= slo.Threshold {
			underThreshold++
		}
	}

	result.TotalRequests = total
	result.FailedRequests = total - underThreshold
	result.Current = float64(underThreshold) / float64(total) * 100

	// Error budget
	result.ErrorBudget = 100.0 - slo.Target
	violationRate := 100.0 - result.Current
	if result.ErrorBudget > 0 {
		result.BudgetConsumed = (violationRate / result.ErrorBudget) * 100
		result.BudgetRemain = math.Max(0, 100-result.BudgetConsumed)
		result.BurnRate = violationRate / result.ErrorBudget
	}

	result.Status = classifyStatus(result.BudgetConsumed)

	return result
}

func classifyStatus(budgetConsumed float64) string {
	switch {
	case budgetConsumed >= 100:
		return "exhausted"
	case budgetConsumed >= 80:
		return "critical"
	case budgetConsumed >= 50:
		return "warning"
	default:
		return "ok"
	}
}

// ──────────────────────────────────────────────
// Output Formatting
// ──────────────────────────────────────────────

// FormatJSON returns the report as JSON.
func FormatJSON(r *Report) (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FormatText returns a terminal-formatted report.
func FormatText(r *Report) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("\n%s SLO Report — %s\n", output.AccentStyle.Render("📊"), r.Instance))
	sb.WriteString(fmt.Sprintf("   %s\n\n", output.MutedStyle.Render(r.Timestamp)))

	// Sort: exhausted first, then critical, warning, ok
	sort.Slice(r.Results, func(i, j int) bool {
		return statusPriority(r.Results[i].Status) > statusPriority(r.Results[j].Status)
	})

	for _, res := range r.Results {
		icon := statusIcon(res.Status)
		nameStr := output.AccentStyle.Render(res.SLO.Name)

		sb.WriteString(fmt.Sprintf("  %s %s\n", icon, nameStr))

		if res.SLO.Description != "" {
			sb.WriteString(fmt.Sprintf("     %s\n", output.MutedStyle.Render(res.SLO.Description)))
		}

		// Current vs Target
		currentStr := formatValue(res.Current, res.Status)
		sb.WriteString(fmt.Sprintf("     Current: %s  Target: %.2f%%  Window: %s\n",
			currentStr, res.Target, res.SLO.Window))

		// Error Budget bar
		budgetBar := renderBudgetBar(res.BudgetRemain, 30)
		sb.WriteString(fmt.Sprintf("     Budget:  %s %.1f%% remaining\n", budgetBar, res.BudgetRemain))

		// Burn rate
		burnStr := fmt.Sprintf("%.2fx", res.BurnRate)
		if res.BurnRate > 1 {
			burnStr = output.ErrorStyle.Render(burnStr)
		} else if res.BurnRate > 0.5 {
			burnStr = output.WarningStyle.Render(burnStr)
		} else {
			burnStr = output.SuccessStyle.Render(burnStr)
		}
		sb.WriteString(fmt.Sprintf("     Burn:    %s  Requests: %d total, %d failed\n\n",
			burnStr, res.TotalRequests, res.FailedRequests))
	}

	// Summary
	var ok, warn, crit, exhausted int
	for _, res := range r.Results {
		switch res.Status {
		case "ok":
			ok++
		case "warning":
			warn++
		case "critical":
			crit++
		case "exhausted":
			exhausted++
		}
	}

	sb.WriteString(fmt.Sprintf("  %s Summary: %s ok  %s warning  %s critical  %s exhausted\n\n",
		output.MutedStyle.Render("─────"),
		output.SuccessStyle.Render(fmt.Sprintf("%d", ok)),
		output.WarningStyle.Render(fmt.Sprintf("%d", warn)),
		output.ErrorStyle.Render(fmt.Sprintf("%d", crit)),
		output.ErrorStyle.Render(fmt.Sprintf("%d", exhausted)),
	))

	return sb.String()
}

func statusIcon(status string) string {
	switch status {
	case "ok":
		return "✅"
	case "warning":
		return "⚠️ "
	case "critical":
		return "🔴"
	case "exhausted":
		return "💀"
	default:
		return "❓"
	}
}

func statusPriority(status string) int {
	switch status {
	case "exhausted":
		return 4
	case "critical":
		return 3
	case "warning":
		return 2
	case "ok":
		return 1
	default:
		return 0
	}
}

func formatValue(val float64, status string) string {
	s := fmt.Sprintf("%.3f%%", val)
	switch status {
	case "ok":
		return output.SuccessStyle.Render(s)
	case "warning":
		return output.WarningStyle.Render(s)
	case "critical", "exhausted":
		return output.ErrorStyle.Render(s)
	default:
		return s
	}
}

func renderBudgetBar(remaining float64, width int) string {
	filled := int(remaining / 100.0 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	empty := width - filled

	var color string
	if remaining > 50 {
		color = "\033[32m" // green
	} else if remaining > 20 {
		color = "\033[33m" // yellow
	} else {
		color = "\033[31m" // red
	}
	reset := "\033[0m"

	bar := color + strings.Repeat("█", filled) + reset + strings.Repeat("░", empty)
	return "[" + bar + "]"
}

// ExitCode returns an exit code based on worst SLO status.
func (r *Report) ExitCode() int {
	worst := 0
	for _, res := range r.Results {
		p := statusPriority(res.Status)
		if p > worst {
			worst = p
		}
	}
	switch worst {
	case 4, 3:
		return 2 // critical/exhausted
	case 2:
		return 1 // warning
	default:
		return 0
	}
}

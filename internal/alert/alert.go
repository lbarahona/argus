package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// Config Types
// ──────────────────────────────────────────────

// AlertConfig holds all alert rules.
type AlertConfig struct {
	Rules []Rule `yaml:"rules" json:"rules"`
}

// Rule defines a single alert rule.
type Rule struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Service     string            `yaml:"service,omitempty" json:"service,omitempty"` // empty = all services
	Type        string            `yaml:"type" json:"type"`                          // error_rate, latency, log_errors, service_down
	Operator    string            `yaml:"operator" json:"operator"`                  // gt, lt, gte, lte, eq
	Warning     float64           `yaml:"warning" json:"warning"`
	Critical    float64           `yaml:"critical" json:"critical"`
	Duration    string            `yaml:"duration,omitempty" json:"duration,omitempty"` // e.g. "5m", "1h"
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Enabled     *bool             `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// IsEnabled returns whether the rule is active.
func (r Rule) IsEnabled() bool {
	if r.Enabled == nil {
		return true
	}
	return *r.Enabled
}

// DurationMinutes parses the duration string to minutes.
func (r Rule) DurationMinutes() int {
	d := r.Duration
	if d == "" {
		d = "5m"
	}
	if strings.HasSuffix(d, "h") {
		var h int
		fmt.Sscanf(d, "%dh", &h)
		return h * 60
	}
	var m int
	fmt.Sscanf(d, "%dm", &m)
	if m == 0 {
		return 5
	}
	return m
}

// ──────────────────────────────────────────────
// Check Results
// ──────────────────────────────────────────────

// Severity levels for results.
type Severity int

const (
	SeverityOK Severity = iota
	SeverityWarning
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return "ok"
	}
}

func (s Severity) Icon() string {
	switch s {
	case SeverityWarning:
		return "⚠️"
	case SeverityCritical:
		return "🔴"
	default:
		return "✅"
	}
}

// CheckResult holds the outcome of evaluating one rule.
type CheckResult struct {
	Rule     string   `json:"rule"`
	Service  string   `json:"service"`
	Type     string   `json:"type"`
	Severity Severity `json:"severity"`
	Status   string   `json:"status"` // "ok", "warning", "critical"
	Value    float64  `json:"value"`
	Message  string   `json:"message"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// Report holds all check results.
type Report struct {
	Instance   string        `json:"instance"`
	Timestamp  time.Time     `json:"timestamp"`
	Results    []CheckResult `json:"results"`
	Summary    Summary       `json:"summary"`
	DurationMs int64         `json:"duration_ms"`
}

// Summary counts results by severity.
type Summary struct {
	Total    int `json:"total"`
	OK       int `json:"ok"`
	Warnings int `json:"warnings"`
	Critical int `json:"critical"`
}

// ExitCode returns the appropriate process exit code.
func (r Report) ExitCode() int {
	if r.Summary.Critical > 0 {
		return 2
	}
	if r.Summary.Warnings > 0 {
		return 1
	}
	return 0
}

// ──────────────────────────────────────────────
// File Management
// ──────────────────────────────────────────────

func alertsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus", "alerts.yaml")
}

// LoadAlerts reads the alerts config.
func LoadAlerts() (*AlertConfig, error) {
	data, err := os.ReadFile(alertsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no alerts configured — run 'argus alert init' to create sample rules")
		}
		return nil, fmt.Errorf("reading alerts: %w", err)
	}
	var cfg AlertConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing alerts: %w", err)
	}
	return &cfg, nil
}

// SaveAlerts writes the alerts config.
func SaveAlerts(cfg *AlertConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling alerts: %w", err)
	}
	return os.WriteFile(alertsPath(), data, 0600)
}

// InitAlerts creates a sample alerts config.
func InitAlerts() error {
	if _, err := os.Stat(alertsPath()); err == nil {
		return fmt.Errorf("alerts file already exists at %s", alertsPath())
	}

	sample := &AlertConfig{
		Rules: []Rule{
			{
				Name:        "high-error-rate",
				Description: "Alert when any service error rate exceeds threshold",
				Type:        "error_rate",
				Operator:    "gt",
				Warning:     5.0,
				Critical:    15.0,
				Duration:    "5m",
				Labels:      map[string]string{"team": "platform"},
			},
			{
				Name:        "api-errors",
				Description: "Alert when API service has errors",
				Service:     "api-service",
				Type:        "error_rate",
				Operator:    "gt",
				Warning:     2.0,
				Critical:    10.0,
				Duration:    "5m",
			},
			{
				Name:        "log-errors",
				Description: "Alert on error logs in any service",
				Type:        "log_errors",
				Operator:    "gt",
				Warning:     10,
				Critical:    50,
				Duration:    "15m",
			},
			{
				Name:        "service-health",
				Description: "Alert when services stop reporting",
				Type:        "service_down",
				Operator:    "lt",
				Warning:     1,
				Critical:    1,
				Duration:    "10m",
			},
		},
	}
	return SaveAlerts(sample)
}

// ──────────────────────────────────────────────
// Checker
// ──────────────────────────────────────────────

// Checker evaluates alert rules against a Signoz instance.
type Checker struct {
	client       signoz.SignozQuerier
	instanceName string
}

// NewChecker creates a new alert checker.
func NewChecker(client signoz.SignozQuerier, instanceName string) *Checker {
	return &Checker{
		client:       client,
		instanceName: instanceName,
	}
}

// CheckAll evaluates all enabled rules and returns a report.
func (ch *Checker) CheckAll(ctx context.Context, cfg *AlertConfig) (*Report, error) {
	start := time.Now()

	// Fetch services once
	services, err := ch.client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	serviceMap := make(map[string]types.Service)
	for _, s := range services {
		serviceMap[s.Name] = s
	}

	var results []CheckResult

	for _, rule := range cfg.Rules {
		if !rule.IsEnabled() {
			continue
		}

		var ruleResults []CheckResult

		switch rule.Type {
		case "error_rate":
			ruleResults = ch.checkErrorRate(rule, services, serviceMap)
		case "log_errors":
			ruleResults = ch.checkLogErrors(ctx, rule, services)
		case "service_down":
			ruleResults = ch.checkServiceDown(rule, services)
		default:
			ruleResults = []CheckResult{{
				Rule:     rule.Name,
				Type:     rule.Type,
				Severity: SeverityWarning,
				Status:   "warning",
				Message:  fmt.Sprintf("Unknown rule type: %s", rule.Type),
			}}
		}

		results = append(results, ruleResults...)
	}

	// Sort: critical first, then warning, then ok
	sort.Slice(results, func(i, j int) bool {
		return results[i].Severity > results[j].Severity
	})

	report := &Report{
		Instance:   ch.instanceName,
		Timestamp:  time.Now().UTC(),
		Results:    results,
		DurationMs: time.Since(start).Milliseconds(),
	}

	// Build summary
	for _, r := range results {
		report.Summary.Total++
		switch r.Severity {
		case SeverityOK:
			report.Summary.OK++
		case SeverityWarning:
			report.Summary.Warnings++
		case SeverityCritical:
			report.Summary.Critical++
		}
	}

	return report, nil
}

func (ch *Checker) checkErrorRate(rule Rule, services []types.Service, serviceMap map[string]types.Service) []CheckResult {
	var results []CheckResult

	checkService := func(svc types.Service) {
		rate := svc.ErrorRate * 100 // Convert to percentage
		if svc.NumCalls > 0 && svc.ErrorRate == 0 {
			rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}

		severity := SeverityOK
		status := "ok"
		msg := fmt.Sprintf("Error rate: %.2f%%", rate)

		if evaluate(rate, rule.Operator, rule.Critical) {
			severity = SeverityCritical
			status = "critical"
			msg = fmt.Sprintf("Error rate %.2f%% exceeds critical threshold (%.1f%%)", rate, rule.Critical)
		} else if evaluate(rate, rule.Operator, rule.Warning) {
			severity = SeverityWarning
			status = "warning"
			msg = fmt.Sprintf("Error rate %.2f%% exceeds warning threshold (%.1f%%)", rate, rule.Warning)
		}

		results = append(results, CheckResult{
			Rule:     rule.Name,
			Service:  svc.Name,
			Type:     rule.Type,
			Severity: severity,
			Status:   status,
			Value:    rate,
			Message:  msg,
			Labels:   rule.Labels,
		})
	}

	if rule.Service != "" {
		if svc, ok := serviceMap[rule.Service]; ok {
			checkService(svc)
		} else {
			results = append(results, CheckResult{
				Rule:     rule.Name,
				Service:  rule.Service,
				Type:     rule.Type,
				Severity: SeverityWarning,
				Status:   "warning",
				Message:  fmt.Sprintf("Service %q not found", rule.Service),
			})
		}
	} else {
		for _, svc := range services {
			checkService(svc)
		}
	}

	return results
}

func (ch *Checker) checkLogErrors(ctx context.Context, rule Rule, services []types.Service) []CheckResult {
	var results []CheckResult
	duration := rule.DurationMinutes()

	checkService := func(svc types.Service) {
		result, err := ch.client.QueryLogs(ctx, svc.Name, duration, 1, "error")
		if err != nil {
			results = append(results, CheckResult{
				Rule:     rule.Name,
				Service:  svc.Name,
				Type:     rule.Type,
				Severity: SeverityWarning,
				Status:   "warning",
				Message:  fmt.Sprintf("Failed to query logs: %v", err),
			})
			return
		}

		count := float64(len(result.Logs))
		severity := SeverityOK
		status := "ok"
		msg := fmt.Sprintf("%d error logs in last %dm", int(count), duration)

		if evaluate(count, rule.Operator, rule.Critical) {
			severity = SeverityCritical
			status = "critical"
			msg = fmt.Sprintf("%d error logs in last %dm (critical threshold: %.0f)", int(count), duration, rule.Critical)
		} else if evaluate(count, rule.Operator, rule.Warning) {
			severity = SeverityWarning
			status = "warning"
			msg = fmt.Sprintf("%d error logs in last %dm (warning threshold: %.0f)", int(count), duration, rule.Warning)
		}

		results = append(results, CheckResult{
			Rule:     rule.Name,
			Service:  svc.Name,
			Type:     rule.Type,
			Severity: severity,
			Status:   status,
			Value:    count,
			Message:  msg,
			Labels:   rule.Labels,
		})
	}

	if rule.Service != "" {
		// Find the specific service
		for _, svc := range services {
			if svc.Name == rule.Service {
				checkService(svc)
				return results
			}
		}
		results = append(results, CheckResult{
			Rule:    rule.Name,
			Service: rule.Service,
			Type:    rule.Type,
			Severity: SeverityWarning,
			Status:  "warning",
			Message: fmt.Sprintf("Service %q not found", rule.Service),
		})
	} else {
		for _, svc := range services {
			checkService(svc)
		}
	}

	return results
}

func (ch *Checker) checkServiceDown(rule Rule, services []types.Service) []CheckResult {
	var results []CheckResult

	if len(services) == 0 {
		results = append(results, CheckResult{
			Rule:     rule.Name,
			Type:     rule.Type,
			Severity: SeverityCritical,
			Status:   "critical",
			Message:  "No services reporting to Signoz",
		})
		return results
	}

	for _, svc := range services {
		if svc.NumCalls == 0 {
			results = append(results, CheckResult{
				Rule:     rule.Name,
				Service:  svc.Name,
				Type:     rule.Type,
				Severity: SeverityWarning,
				Status:   "warning",
				Value:    0,
				Message:  fmt.Sprintf("Service %q has 0 calls — may be down", svc.Name),
				Labels:   rule.Labels,
			})
		}
	}

	if len(results) == 0 {
		results = append(results, CheckResult{
			Rule:     rule.Name,
			Type:     rule.Type,
			Severity: SeverityOK,
			Status:   "ok",
			Value:    float64(len(services)),
			Message:  fmt.Sprintf("All %d services reporting", len(services)),
		})
	}

	return results
}

// evaluate compares a value against a threshold using the given operator.
func evaluate(value float64, operator string, threshold float64) bool {
	switch operator {
	case "gt", ">":
		return value > threshold
	case "gte", ">=":
		return value >= threshold
	case "lt", "<":
		return value < threshold
	case "lte", "<=":
		return value <= threshold
	case "eq", "==":
		return value == threshold
	default:
		return value > threshold
	}
}

// ──────────────────────────────────────────────
// Output Formatting
// ──────────────────────────────────────────────

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// FormatText returns a human-readable colored output.
func FormatText(report *Report) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("\n%s🔔 Alert Check — %s%s\n", colorBold, report.Instance, colorReset))
	b.WriteString(fmt.Sprintf("%s%s  •  %d rules evaluated in %dms%s\n\n",
		colorGray, report.Timestamp.Format("2006-01-02 15:04:05 UTC"),
		report.Summary.Total, report.DurationMs, colorReset))

	if len(report.Results) == 0 {
		b.WriteString(fmt.Sprintf("  %s✅ No rules to evaluate%s\n", colorGreen, colorReset))
		return b.String()
	}

	// Group by severity
	for _, result := range report.Results {
		var color string
		switch result.Severity {
		case SeverityCritical:
			color = colorRed
		case SeverityWarning:
			color = colorYellow
		default:
			color = colorGreen
		}

		svc := result.Service
		if svc == "" {
			svc = "*"
		}

		b.WriteString(fmt.Sprintf("  %s%s %s%s", color, result.Severity.Icon(), result.Status, colorReset))
		b.WriteString(fmt.Sprintf("  %s[%s]%s", colorCyan, svc, colorReset))
		b.WriteString(fmt.Sprintf("  %s\n", result.Message))
	}

	// Summary line
	b.WriteString(fmt.Sprintf("\n%s━━━ ", colorGray))
	if report.Summary.Critical > 0 {
		b.WriteString(fmt.Sprintf("%s%d critical%s ", colorRed, report.Summary.Critical, colorGray))
	}
	if report.Summary.Warnings > 0 {
		b.WriteString(fmt.Sprintf("%s%d warnings%s ", colorYellow, report.Summary.Warnings, colorGray))
	}
	b.WriteString(fmt.Sprintf("%s%d ok%s", colorGreen, report.Summary.OK, colorGray))
	b.WriteString(fmt.Sprintf(" ━━━%s\n", colorReset))

	return b.String()
}

// FormatJSON returns a JSON representation.
func FormatJSON(report *Report) (string, error) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

package report

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// Report holds all data for a health report.
type Report struct {
	GeneratedAt time.Time
	Duration    int // minutes
	Instance    string
	Health      []types.HealthStatus
	Services    []types.Service
	ErrorLogs   []types.LogEntry
	AllLogs     []types.LogEntry
	AISummary   string

	// Derived
	TotalErrors   int
	TotalCalls    int
	TopErrors     []ServiceError
	ErrorPatterns []ErrorPattern
}

// ServiceError tracks errors per service.
type ServiceError struct {
	Service   string
	Errors    int
	ErrorRate float64
}

// ErrorPattern groups similar errors.
type ErrorPattern struct {
	Pattern string
	Count   int
	Service string
	Sample  string
}

// Options configures report generation.
type Options struct {
	Duration     int // minutes
	WithAI       bool
	Format       string // "terminal" or "markdown"
	AnthropicKey string
}

// Generate creates a health report from Signoz data.
func Generate(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Report, error) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    opts.Duration,
		Instance:    instKey,
	}

	// Health check
	healthy, latency, healthErr := client.Health(ctx)
	status := types.HealthStatus{
		InstanceName: instKey,
		InstanceKey:  instKey,
		Healthy:      healthy,
		Latency:      latency,
	}
	if healthErr != nil {
		status.Message = healthErr.Error()
	}
	r.Health = []types.HealthStatus{status}

	// Services
	if services, err := client.ListServices(ctx); err == nil {
		r.Services = services
		for _, s := range services {
			r.TotalCalls += s.NumCalls
			r.TotalErrors += s.NumErrors
		}
	}

	// Error logs
	if result, err := client.QueryLogs(ctx, "", opts.Duration, 200, "ERROR"); err == nil {
		r.ErrorLogs = result.Logs
	}

	// All logs (sample for pattern detection)
	if result, err := client.QueryLogs(ctx, "", opts.Duration, 50, ""); err == nil {
		r.AllLogs = result.Logs
	}

	// Derive top errors by service
	r.TopErrors = computeTopErrors(r.Services)
	r.ErrorPatterns = detectPatterns(r.ErrorLogs)

	// AI summary
	if opts.WithAI && opts.AnthropicKey != "" {
		summary, err := generateAISummary(r, opts.AnthropicKey)
		if err == nil {
			r.AISummary = summary
		}
	}

	return r, nil
}

func computeTopErrors(services []types.Service) []ServiceError {
	var top []ServiceError
	for _, s := range services {
		if s.NumErrors > 0 {
			top = append(top, ServiceError{
				Service:   s.Name,
				Errors:    s.NumErrors,
				ErrorRate: s.ErrorRate,
			})
		}
	}
	sort.Slice(top, func(i, j int) bool {
		return top[i].Errors > top[j].Errors
	})
	if len(top) > 10 {
		top = top[:10]
	}
	return top
}

func detectPatterns(logs []types.LogEntry) []ErrorPattern {
	// Group by first 80 chars of body (rough dedup)
	groups := make(map[string]*ErrorPattern)
	for _, log := range logs {
		key := log.Body
		if len(key) > 80 {
			key = key[:80]
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if p, ok := groups[key]; ok {
			p.Count++
		} else {
			groups[key] = &ErrorPattern{
				Pattern: key,
				Count:   1,
				Service: log.ServiceName,
				Sample:  truncate(log.Body, 200),
			}
		}
	}

	var patterns []ErrorPattern
	for _, p := range groups {
		patterns = append(patterns, *p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Count > patterns[j].Count
	})
	if len(patterns) > 10 {
		patterns = patterns[:10]
	}
	return patterns
}

func generateAISummary(r *Report, apiKey string) (string, error) {
	prompt := buildSummaryPrompt(r)
	analyzer := ai.New(apiKey)
	return analyzer.AnalyzeSync(prompt)
}

func buildSummaryPrompt(r *Report) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Generate a concise health report summary for a Signoz instance over the last %d minutes.\n\n", r.Duration))

	// Health
	for _, h := range r.Health {
		status := "healthy"
		if !h.Healthy {
			status = "unhealthy: " + h.Message
		}
		sb.WriteString(fmt.Sprintf("Instance %s (%s): %s, latency %s\n", h.InstanceKey, h.URL, status, h.Latency))
	}

	// Services summary
	sb.WriteString(fmt.Sprintf("\nTotal services: %d, Total calls: %d, Total errors: %d\n", len(r.Services), r.TotalCalls, r.TotalErrors))

	// Top errors
	if len(r.TopErrors) > 0 {
		sb.WriteString("\nTop error services:\n")
		for _, e := range r.TopErrors {
			sb.WriteString(fmt.Sprintf("- %s: %d errors (%.1f%% error rate)\n", e.Service, e.Errors, e.ErrorRate))
		}
	}

	// Error patterns
	if len(r.ErrorPatterns) > 0 {
		sb.WriteString("\nTop error patterns:\n")
		for _, p := range r.ErrorPatterns {
			sb.WriteString(fmt.Sprintf("- [%s] (%dx): %s\n", p.Service, p.Count, p.Sample))
		}
	}

	sb.WriteString("\nProvide:\n1. Overall health assessment (1-2 sentences)\n2. Key issues requiring attention (bullet points)\n3. Recommended actions (bullet points)\n\nKeep it brief and actionable.")
	return sb.String()
}

// RenderTerminal outputs the report to a terminal writer.
func (r *Report) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n🔭 ARGUS HEALTH REPORT\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "  Generated: %s\n", r.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "  Window:    Last %d minutes\n", r.Duration)
	fmt.Fprintf(w, "  Instance:  %s\n\n", r.Instance)

	// Health
	for _, h := range r.Health {
		icon := "🟢"
		if !h.Healthy {
			icon = "🔴"
		}
		fmt.Fprintf(w, "  %s %s — %s (latency: %s)\n", icon, h.InstanceName, h.URL, h.Latency)
	}
	fmt.Fprintln(w)

	// Overview
	fmt.Fprintf(w, "  📊 Overview\n")
	fmt.Fprintf(w, "  ├─ Services:     %d\n", len(r.Services))
	fmt.Fprintf(w, "  ├─ Total Calls:  %d\n", r.TotalCalls)
	fmt.Fprintf(w, "  ├─ Total Errors: %d\n", r.TotalErrors)
	errRate := float64(0)
	if r.TotalCalls > 0 {
		errRate = float64(r.TotalErrors) / float64(r.TotalCalls) * 100
	}
	fmt.Fprintf(w, "  └─ Error Rate:   %.2f%%\n\n", errRate)

	// Top errors
	if len(r.TopErrors) > 0 {
		fmt.Fprintf(w, "  🚨 Top Error Services\n")
		for i, e := range r.TopErrors {
			connector := "├─"
			if i == len(r.TopErrors)-1 {
				connector = "└─"
			}
			fmt.Fprintf(w, "  %s %-30s %5d errors (%.1f%%)\n", connector, e.Service, e.Errors, e.ErrorRate)
		}
		fmt.Fprintln(w)
	}

	// Error patterns
	if len(r.ErrorPatterns) > 0 {
		fmt.Fprintf(w, "  🔍 Error Patterns\n")
		for i, p := range r.ErrorPatterns {
			connector := "├─"
			if i == len(r.ErrorPatterns)-1 {
				connector = "└─"
			}
			fmt.Fprintf(w, "  %s [%s] (%dx) %s\n", connector, p.Service, p.Count, truncate(p.Pattern, 60))
		}
		fmt.Fprintln(w)
	}

	// AI Summary
	if r.AISummary != "" {
		fmt.Fprintf(w, "  🤖 AI Assessment\n")
		fmt.Fprintf(w, "  %s\n\n", strings.ReplaceAll(r.AISummary, "\n", "\n  "))
	}

	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// RenderMarkdown outputs the report as markdown.
func (r *Report) RenderMarkdown(w io.Writer) {
	fmt.Fprintf(w, "# 🔭 Argus Health Report\n\n")
	fmt.Fprintf(w, "**Generated:** %s  \n", r.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(w, "**Window:** Last %d minutes  \n", r.Duration)
	fmt.Fprintf(w, "**Instance:** %s\n\n", r.Instance)

	// Health
	fmt.Fprintf(w, "## Instance Health\n\n")
	for _, h := range r.Health {
		icon := "🟢"
		if !h.Healthy {
			icon = "🔴"
		}
		fmt.Fprintf(w, "- %s **%s** — %s (latency: %s)\n", icon, h.InstanceName, h.URL, h.Latency)
	}
	fmt.Fprintln(w)

	// Overview
	errRate := float64(0)
	if r.TotalCalls > 0 {
		errRate = float64(r.TotalErrors) / float64(r.TotalCalls) * 100
	}
	fmt.Fprintf(w, "## Overview\n\n")
	fmt.Fprintf(w, "| Metric | Value |\n|--------|-------|\n")
	fmt.Fprintf(w, "| Services | %d |\n", len(r.Services))
	fmt.Fprintf(w, "| Total Calls | %d |\n", r.TotalCalls)
	fmt.Fprintf(w, "| Total Errors | %d |\n", r.TotalErrors)
	fmt.Fprintf(w, "| Error Rate | %.2f%% |\n\n", errRate)

	// Top errors
	if len(r.TopErrors) > 0 {
		fmt.Fprintf(w, "## Top Error Services\n\n")
		fmt.Fprintf(w, "| Service | Errors | Error Rate |\n|---------|--------|------------|\n")
		for _, e := range r.TopErrors {
			fmt.Fprintf(w, "| %s | %d | %.1f%% |\n", e.Service, e.Errors, e.ErrorRate)
		}
		fmt.Fprintln(w)
	}

	// Error patterns
	if len(r.ErrorPatterns) > 0 {
		fmt.Fprintf(w, "## Error Patterns\n\n")
		for _, p := range r.ErrorPatterns {
			fmt.Fprintf(w, "- **[%s]** (%dx): `%s`\n", p.Service, p.Count, truncate(p.Pattern, 80))
		}
		fmt.Fprintln(w)
	}

	// AI Summary
	if r.AISummary != "" {
		fmt.Fprintf(w, "## AI Assessment\n\n%s\n", r.AISummary)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}


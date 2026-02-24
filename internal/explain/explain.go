package explain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// Options configures the explain command.
type Options struct {
	Service      string
	Duration     int // minutes
	AnthropicKey string
}

// CorrelatedData holds all collected observability data for a service.
type CorrelatedData struct {
	Service     string
	Instance    string
	Services    []types.Service
	ErrorLogs   []types.LogEntry
	RecentLogs  []types.LogEntry
	Traces      []types.TraceEntry
	CollectedAt time.Time
}

// Collect gathers all relevant data for a service from Signoz.
func Collect(ctx context.Context, client signoz.SignozQuerier, instanceName string, opts Options) (*CorrelatedData, error) {
	data := &CorrelatedData{
		Service:     opts.Service,
		Instance:    instanceName,
		CollectedAt: time.Now().UTC(),
	}

	// Get all services for context
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	data.Services = services

	// Verify target service exists
	found := false
	for _, s := range services {
		if s.Name == opts.Service {
			found = true
			break
		}
	}
	if !found {
		available := make([]string, len(services))
		for i, s := range services {
			available[i] = s.Name
		}
		return nil, fmt.Errorf("service %q not found. Available: %s", opts.Service, strings.Join(available, ", "))
	}

	// Get error logs
	if result, err := client.QueryLogs(ctx, opts.Service, opts.Duration, 50, "error"); err == nil {
		data.ErrorLogs = result.Logs
	}

	// Get recent logs (all levels)
	if result, err := client.QueryLogs(ctx, opts.Service, opts.Duration, 30, ""); err == nil {
		data.RecentLogs = result.Logs
	}

	// Get traces
	if result, err := client.QueryTraces(ctx, opts.Service, opts.Duration, 30); err == nil {
		data.Traces = result.Traces
	}

	return data, nil
}

// BuildPrompt creates the AI analysis prompt from correlated data.
func BuildPrompt(data *CorrelatedData) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("You are an expert SRE analyzing the service '%s' on Signoz instance '%s'.\n", data.Service, data.Instance))
	b.WriteString("Correlate the following observability data and provide a root cause analysis.\n\n")

	// Service context
	b.WriteString("## Service Overview\n")
	for _, s := range data.Services {
		marker := ""
		if s.Name == data.Service {
			marker = " ← TARGET"
		}
		rate := s.ErrorRate * 100
		if s.NumCalls > 0 && s.ErrorRate == 0 {
			rate = float64(s.NumErrors) / float64(s.NumCalls) * 100
		}
		b.WriteString(fmt.Sprintf("- %s: %d calls, %d errors (%.2f%%)%s\n", s.Name, s.NumCalls, s.NumErrors, rate, marker))
	}

	// Error logs
	if len(data.ErrorLogs) > 0 {
		b.WriteString(fmt.Sprintf("\n## Error Logs (%d found)\n", len(data.ErrorLogs)))
		for _, log := range data.ErrorLogs {
			body := log.Body
			if len(body) > 300 {
				body = body[:300] + "..."
			}
			b.WriteString(fmt.Sprintf("[%s] %s\n", log.Timestamp.Format("15:04:05"), body))
		}
	} else {
		b.WriteString("\n## Error Logs\nNo error logs found in the time window.\n")
	}

	// Recent logs for context
	if len(data.RecentLogs) > 0 {
		b.WriteString(fmt.Sprintf("\n## Recent Logs (all levels, %d entries)\n", len(data.RecentLogs)))
		for _, log := range data.RecentLogs {
			body := log.Body
			if len(body) > 200 {
				body = body[:200] + "..."
			}
			b.WriteString(fmt.Sprintf("[%s] [%s] %s\n", log.Timestamp.Format("15:04:05"), log.SeverityText, body))
		}
	}

	// Traces
	if len(data.Traces) > 0 {
		b.WriteString(fmt.Sprintf("\n## Traces (%d spans)\n", len(data.Traces)))

		// Find slow and error traces
		var slowTraces, errorTraces []types.TraceEntry
		for _, t := range data.Traces {
			if t.DurationMs() > 1000 {
				slowTraces = append(slowTraces, t)
			}
			if t.StatusCode != "" && t.StatusCode != "OK" && t.StatusCode != "0" {
				errorTraces = append(errorTraces, t)
			}
		}

		if len(errorTraces) > 0 {
			b.WriteString(fmt.Sprintf("\n### Error Traces (%d)\n", len(errorTraces)))
			for _, t := range errorTraces {
				b.WriteString(fmt.Sprintf("- %s %s → %s (%.1fms, status: %s)\n",
					t.Timestamp.Format("15:04:05"), t.ServiceName, t.OperationName, t.DurationMs(), t.StatusCode))
			}
		}

		if len(slowTraces) > 0 {
			b.WriteString(fmt.Sprintf("\n### Slow Traces >1s (%d)\n", len(slowTraces)))
			limit := 10
			if len(slowTraces) < limit {
				limit = len(slowTraces)
			}
			for _, t := range slowTraces[:limit] {
				b.WriteString(fmt.Sprintf("- %s %s → %s (%.1fms)\n",
					t.Timestamp.Format("15:04:05"), t.ServiceName, t.OperationName, t.DurationMs()))
			}
		}
	}

	b.WriteString(`
## Your Analysis

Provide:
1. **Health Assessment** — Is the service healthy, degraded, or critical?
2. **Root Cause** — What's causing any issues? Correlate across logs, traces, and metrics.
3. **Impact** — What's the blast radius? Are downstream services affected?
4. **Recommended Actions** — Specific steps to resolve or mitigate, ordered by priority.
5. **Prevention** — What would prevent this in the future?

Be specific and actionable. Reference actual log messages and trace data.`)

	return b.String()
}

// Run collects data and streams AI analysis.
func Run(ctx context.Context, client signoz.SignozQuerier, instanceName string, opts Options, writer interface{ Write([]byte) (int, error) }) error {
	data, err := Collect(ctx, client, instanceName, opts)
	if err != nil {
		return err
	}

	prompt := BuildPrompt(data)
	analyzer := ai.New(opts.AnthropicKey)
	return analyzer.Analyze(prompt, writer)
}

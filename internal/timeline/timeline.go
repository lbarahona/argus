package timeline

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

// EventType categorizes timeline events.
type EventType string

const (
	EventErrorSpike   EventType = "error_spike"
	EventNewError     EventType = "new_error"
	EventServiceDown  EventType = "service_down"
	EventLatencySpike EventType = "latency_spike"
	EventRecovery     EventType = "recovery"
	EventLogAnomaly   EventType = "log_anomaly"
)

// Event represents a single event on the timeline.
type Event struct {
	Timestamp   time.Time
	Type        EventType
	Service     string
	Description string
	Severity    string // "critical", "warning", "info"
	Details     map[string]string
}

// Timeline holds the reconstructed incident timeline.
type Timeline struct {
	StartTime   time.Time
	EndTime     time.Time
	Duration    time.Duration
	Instance    string
	Events      []Event
	Services    []string
	AINarrative string
}

// Options configures timeline generation.
type Options struct {
	Duration     int    // minutes to look back
	Service      string // optional: filter to a specific service
	WithAI       bool   // generate AI narrative
	Format       string // "terminal" or "markdown"
	AnthropicKey string
}

// Generate builds an incident timeline from Signoz data.
func Generate(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Timeline, error) {
	dur := opts.Duration
	if dur <= 0 {
		dur = 60
	}

	now := time.Now()
	start := now.Add(-time.Duration(dur) * time.Minute)

	tl := &Timeline{
		StartTime: start,
		EndTime:   now,
		Duration:  time.Duration(dur) * time.Minute,
		Instance:  instKey,
	}

	// 1. Get services
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	// 2. Collect error logs
	serviceFilter := opts.Service
	errorLogs, err := client.QueryLogs(ctx, serviceFilter, dur, 500, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying error logs: %w", err)
	}

	// 3. Collect traces for latency analysis
	traces, err := client.QueryTraces(ctx, serviceFilter, dur, 200)
	if err != nil {
		// Traces might not be available, don't fail
		traces = &types.QueryResult{}
	}

	// 4. Detect error spikes using time bucketing
	errorEvents := detectErrorSpikes(errorLogs.Logs, dur)
	tl.Events = append(tl.Events, errorEvents...)

	// 5. Detect new/unique error messages
	newErrors := detectNewErrors(errorLogs.Logs)
	tl.Events = append(tl.Events, newErrors...)

	// 6. Detect latency spikes from traces
	latencyEvents := detectLatencySpikes(traces.Traces)
	tl.Events = append(tl.Events, latencyEvents...)

	// 7. Detect service health issues
	healthEvents := detectServiceHealth(services)
	tl.Events = append(tl.Events, healthEvents...)

	// Sort events chronologically
	sort.Slice(tl.Events, func(i, j int) bool {
		return tl.Events[i].Timestamp.Before(tl.Events[j].Timestamp)
	})

	// Collect unique services
	svcSet := make(map[string]bool)
	for _, e := range tl.Events {
		if e.Service != "" {
			svcSet[e.Service] = true
		}
	}
	for s := range svcSet {
		tl.Services = append(tl.Services, s)
	}
	sort.Strings(tl.Services)

	// 8. Optional AI narrative
	if opts.WithAI && opts.AnthropicKey != "" && len(tl.Events) > 0 {
		narrative, err := generateNarrative(ctx, tl, opts.AnthropicKey)
		if err == nil {
			tl.AINarrative = narrative
		}
	}

	return tl, nil
}

// detectErrorSpikes finds sudden increases in error rate using time buckets.
func detectErrorSpikes(logs []types.LogEntry, durationMin int) []Event {
	var events []Event
	if len(logs) == 0 {
		return events
	}

	// Bucket size: 5 minutes or duration/10, whichever is larger
	bucketSize := 5 * time.Minute
	if d := time.Duration(durationMin/10) * time.Minute; d > bucketSize {
		bucketSize = d
	}

	// Group errors by service, then by time bucket
	type bucket struct {
		service string
		time    time.Time
		count   int
		samples []string
	}

	serviceBuckets := make(map[string]map[int64]*bucket)
	for _, log := range logs {
		svc := log.ServiceName
		if svc == "" {
			svc = "unknown"
		}
		if _, ok := serviceBuckets[svc]; !ok {
			serviceBuckets[svc] = make(map[int64]*bucket)
		}
		key := log.Timestamp.Truncate(bucketSize).Unix()
		if b, ok := serviceBuckets[svc][key]; ok {
			b.count++
			if len(b.samples) < 3 {
				b.samples = append(b.samples, truncateStr(log.Body, 80))
			}
		} else {
			serviceBuckets[svc][key] = &bucket{
				service: svc,
				time:    log.Timestamp.Truncate(bucketSize),
				count:   1,
				samples: []string{truncateStr(log.Body, 80)},
			}
		}
	}

	// For each service, find buckets with significantly more errors than average
	for svc, buckets := range serviceBuckets {
		if len(buckets) < 2 {
			// Single bucket - just report if there are errors
			for _, b := range buckets {
				if b.count >= 5 {
					events = append(events, Event{
						Timestamp:   b.time,
						Type:        EventErrorSpike,
						Service:     svc,
						Description: fmt.Sprintf("%d errors in %s window", b.count, bucketSize),
						Severity:    severityFromCount(b.count),
						Details: map[string]string{
							"count":  fmt.Sprintf("%d", b.count),
							"sample": strings.Join(b.samples, "; "),
						},
					})
				}
			}
			continue
		}

		// Calculate average
		total := 0
		for _, b := range buckets {
			total += b.count
		}
		avg := float64(total) / float64(len(buckets))

		for _, b := range buckets {
			// Spike = 2x average or at least 5 errors above average
			if float64(b.count) > avg*2 || float64(b.count) > avg+5 {
				events = append(events, Event{
					Timestamp:   b.time,
					Type:        EventErrorSpike,
					Service:     svc,
					Description: fmt.Sprintf("Error spike: %d errors (avg %.0f) in %s window", b.count, avg, bucketSize),
					Severity:    severityFromCount(b.count),
					Details: map[string]string{
						"count":   fmt.Sprintf("%d", b.count),
						"average": fmt.Sprintf("%.1f", avg),
						"sample":  strings.Join(b.samples, "; "),
					},
				})
			}
		}
	}

	return events
}

// detectNewErrors finds unique error messages that appear for the first time.
func detectNewErrors(logs []types.LogEntry) []Event {
	var events []Event
	if len(logs) == 0 {
		return events
	}

	// Sort by time
	sorted := make([]types.LogEntry, len(logs))
	copy(sorted, logs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	// Track first occurrence of each error pattern per service
	seen := make(map[string]time.Time) // "service:pattern" -> first seen
	for _, log := range sorted {
		pattern := normalizeError(log.Body)
		key := log.ServiceName + ":" + pattern
		if _, ok := seen[key]; !ok {
			seen[key] = log.Timestamp
		}
	}

	// Only report errors that first appeared in this window
	// (heuristic: if we have the first occurrence, it's likely new)
	reported := make(map[string]bool)
	for _, log := range sorted {
		pattern := normalizeError(log.Body)
		key := log.ServiceName + ":" + pattern
		if reported[key] {
			continue
		}
		reported[key] = true

		// Count occurrences of this pattern
		count := 0
		for _, l := range sorted {
			if l.ServiceName == log.ServiceName && normalizeError(l.Body) == pattern {
				count++
			}
		}

		if count >= 3 { // Only report patterns with multiple occurrences
			events = append(events, Event{
				Timestamp:   seen[key],
				Type:        EventNewError,
				Service:     log.ServiceName,
				Description: fmt.Sprintf("Error pattern (%dx): %s", count, truncateStr(log.Body, 100)),
				Severity:    severityFromCount(count),
				Details: map[string]string{
					"count":   fmt.Sprintf("%d", count),
					"pattern": pattern,
					"sample":  truncateStr(log.Body, 200),
				},
			})
		}
	}

	return events
}

// detectLatencySpikes finds services with unusually high latency.
func detectLatencySpikes(traces []types.TraceEntry) []Event {
	var events []Event
	if len(traces) == 0 {
		return events
	}

	// Group traces by service
	serviceTraces := make(map[string][]types.TraceEntry)
	for _, t := range traces {
		serviceTraces[t.ServiceName] = append(serviceTraces[t.ServiceName], t)
	}

	for svc, svcTraces := range serviceTraces {
		if len(svcTraces) < 5 {
			continue
		}

		// Sort by duration
		sort.Slice(svcTraces, func(i, j int) bool {
			return svcTraces[i].DurationNano < svcTraces[j].DurationNano
		})

		// Calculate P50 and P99
		p50 := svcTraces[len(svcTraces)/2].DurationMs()
		p99Idx := int(float64(len(svcTraces)) * 0.99)
		if p99Idx >= len(svcTraces) {
			p99Idx = len(svcTraces) - 1
		}
		p99 := svcTraces[p99Idx].DurationMs()

		// If P99 is 10x P50, that's a spike
		if p99 > p50*10 && p99 > 1000 { // >1s P99
			// Find when the slow traces happened
			slowTraces := svcTraces[p99Idx:]
			if len(slowTraces) > 0 {
				events = append(events, Event{
					Timestamp:   slowTraces[0].Timestamp,
					Type:        EventLatencySpike,
					Service:     svc,
					Description: fmt.Sprintf("Latency spike: P99=%.0fms (P50=%.0fms, %dx slower)", p99, p50, int(p99/p50)),
					Severity:    latencySeverity(p99),
					Details: map[string]string{
						"p50_ms":     fmt.Sprintf("%.0f", p50),
						"p99_ms":     fmt.Sprintf("%.0f", p99),
						"trace_id":   slowTraces[0].TraceID,
						"operation":  slowTraces[0].OperationName,
						"multiplier": fmt.Sprintf("%dx", int(p99/p50)),
					},
				})
			}
		}
	}

	return events
}

// detectServiceHealth checks for services with very high error rates.
func detectServiceHealth(services []types.Service) []Event {
	var events []Event
	now := time.Now()

	for _, svc := range services {
		if svc.NumCalls == 0 {
			continue
		}

		errorRate := float64(svc.NumErrors) / float64(svc.NumCalls) * 100

		if errorRate > 50 {
			events = append(events, Event{
				Timestamp:   now,
				Type:        EventServiceDown,
				Service:     svc.Name,
				Description: fmt.Sprintf("Service degraded: %.1f%% error rate (%d/%d calls failing)", errorRate, svc.NumErrors, svc.NumCalls),
				Severity:    "critical",
				Details: map[string]string{
					"error_rate": fmt.Sprintf("%.1f%%", errorRate),
					"errors":     fmt.Sprintf("%d", svc.NumErrors),
					"calls":      fmt.Sprintf("%d", svc.NumCalls),
				},
			})
		} else if errorRate > 10 {
			events = append(events, Event{
				Timestamp:   now,
				Type:        EventServiceDown,
				Service:     svc.Name,
				Description: fmt.Sprintf("Elevated error rate: %.1f%% (%d/%d calls)", errorRate, svc.NumErrors, svc.NumCalls),
				Severity:    "warning",
				Details: map[string]string{
					"error_rate": fmt.Sprintf("%.1f%%", errorRate),
					"errors":     fmt.Sprintf("%d", svc.NumErrors),
					"calls":      fmt.Sprintf("%d", svc.NumCalls),
				},
			})
		}
	}

	return events
}

// generateNarrative uses AI to create a human-readable incident narrative.
func generateNarrative(_ context.Context, tl *Timeline, apiKey string) (string, error) {
	analyzer := ai.New(apiKey)

	var sb strings.Builder
	sb.WriteString("You are an expert SRE analyzing an incident timeline. ")
	sb.WriteString("Create a concise incident narrative from these events. ")
	sb.WriteString("Focus on: root cause hypothesis, blast radius, and recommended actions.\n\n")
	sb.WriteString(fmt.Sprintf("Time window: %s to %s (%s)\n", tl.StartTime.Format(time.RFC3339), tl.EndTime.Format(time.RFC3339), tl.Duration))
	sb.WriteString(fmt.Sprintf("Affected services: %s\n\n", strings.Join(tl.Services, ", ")))
	sb.WriteString("Events (chronological):\n")

	for _, e := range tl.Events {
		sb.WriteString(fmt.Sprintf("- [%s] %s | %s | %s: %s\n",
			e.Timestamp.Format("15:04:05"), e.Severity, e.Type, e.Service, e.Description))
		if sample, ok := e.Details["sample"]; ok && sample != "" {
			sb.WriteString(fmt.Sprintf("  Sample: %s\n", truncateStr(sample, 150)))
		}
	}

	result, err := analyzer.AnalyzeSync(sb.String())
	if err != nil {
		return "", err
	}

	return result, nil
}

// RenderTerminal displays the timeline in a terminal.
func (tl *Timeline) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n⏱️  ARGUS INCIDENT TIMELINE\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "  Instance: %s\n", tl.Instance)
	fmt.Fprintf(w, "  Window:   %s → %s (%s)\n",
		tl.StartTime.Format("15:04:05"), tl.EndTime.Format("15:04:05"), tl.Duration)

	if len(tl.Services) > 0 {
		fmt.Fprintf(w, "  Services: %s\n", strings.Join(tl.Services, ", "))
	}
	fmt.Fprintf(w, "  Events:   %d\n", len(tl.Events))
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	if len(tl.Events) == 0 {
		fmt.Fprintf(w, "  ✅ No incidents detected in this time window.\n\n")
		return
	}

	// Group events by time proximity (within 2 minutes = same cluster)
	var lastTime time.Time
	for i, e := range tl.Events {
		// Show time marker if >2 min gap
		if i == 0 || e.Timestamp.Sub(lastTime) > 2*time.Minute {
			if i > 0 {
				fmt.Fprintf(w, "  │\n")
				gap := e.Timestamp.Sub(lastTime)
				if gap > 5*time.Minute {
					fmt.Fprintf(w, "  │  ··· %s gap ···\n", formatDuration(gap))
					fmt.Fprintf(w, "  │\n")
				}
			}
			fmt.Fprintf(w, "  ┌─ %s\n", e.Timestamp.Format("15:04:05"))
		}
		lastTime = e.Timestamp

		icon := severityIcon(e.Severity)
		typeLabel := eventTypeLabel(e.Type)
		fmt.Fprintf(w, "  │ %s [%s] %s\n", icon, typeLabel, e.Service)
		fmt.Fprintf(w, "  │   %s\n", e.Description)

		// Show key details
		if traceID, ok := e.Details["trace_id"]; ok {
			fmt.Fprintf(w, "  │   🔗 trace: %s\n", traceID)
		}
		if sample, ok := e.Details["sample"]; ok && sample != "" {
			for _, line := range wrapText(sample, 65) {
				fmt.Fprintf(w, "  │   > %s\n", line)
			}
		}
	}

	fmt.Fprintf(w, "  └─\n")

	// Summary
	fmt.Fprintf(w, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	critical, warning, info := countSeverities(tl.Events)
	fmt.Fprintf(w, "  Summary: 🔴 %d critical  🟡 %d warning  🔵 %d info\n", critical, warning, info)

	if tl.AINarrative != "" {
		fmt.Fprintf(w, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Fprintf(w, "  🤖 AI INCIDENT NARRATIVE\n")
		fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
		for _, line := range strings.Split(tl.AINarrative, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}

	fmt.Fprintf(w, "\n")
}

// RenderMarkdown renders the timeline as Markdown.
func (tl *Timeline) RenderMarkdown(w io.Writer) {
	fmt.Fprintf(w, "# 🔭 Incident Timeline\n\n")
	fmt.Fprintf(w, "- **Instance:** %s\n", tl.Instance)
	fmt.Fprintf(w, "- **Window:** %s → %s (%s)\n",
		tl.StartTime.Format("15:04:05"), tl.EndTime.Format("15:04:05"), tl.Duration)
	fmt.Fprintf(w, "- **Services:** %s\n", strings.Join(tl.Services, ", "))
	fmt.Fprintf(w, "- **Events:** %d\n\n", len(tl.Events))

	if len(tl.Events) == 0 {
		fmt.Fprintf(w, "✅ No incidents detected.\n")
		return
	}

	fmt.Fprintf(w, "## Timeline\n\n")
	fmt.Fprintf(w, "| Time | Severity | Type | Service | Description |\n")
	fmt.Fprintf(w, "|------|----------|------|---------|-------------|\n")

	for _, e := range tl.Events {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
			e.Timestamp.Format("15:04:05"),
			e.Severity,
			e.Type,
			e.Service,
			truncateStr(e.Description, 80))
	}

	if tl.AINarrative != "" {
		fmt.Fprintf(w, "\n## AI Narrative\n\n%s\n", tl.AINarrative)
	}
}

// Helper functions

func normalizeError(body string) string {
	// Simple normalization: remove numbers, UUIDs, timestamps
	if len(body) > 100 {
		body = body[:100]
	}
	return strings.TrimSpace(body)
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func severityFromCount(count int) string {
	switch {
	case count >= 50:
		return "critical"
	case count >= 10:
		return "warning"
	default:
		return "info"
	}
}

func latencySeverity(p99Ms float64) string {
	switch {
	case p99Ms > 10000:
		return "critical"
	case p99Ms > 5000:
		return "warning"
	default:
		return "info"
	}
}

func severityIcon(s string) string {
	switch s {
	case "critical":
		return "🔴"
	case "warning":
		return "🟡"
	default:
		return "🔵"
	}
}

func eventTypeLabel(t EventType) string {
	switch t {
	case EventErrorSpike:
		return "ERR SPIKE"
	case EventNewError:
		return "NEW ERROR"
	case EventServiceDown:
		return "SVC DOWN "
	case EventLatencySpike:
		return "LATENCY  "
	case EventRecovery:
		return "RECOVERY "
	case EventLogAnomaly:
		return "ANOMALY  "
	default:
		return string(t)
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func wrapText(s string, width int) []string {
	if len(s) <= width {
		return []string{s}
	}
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return lines
}

func countSeverities(events []Event) (critical, warning, info int) {
	for _, e := range events {
		switch e.Severity {
		case "critical":
			critical++
		case "warning":
			warning++
		default:
			info++
		}
	}
	return
}

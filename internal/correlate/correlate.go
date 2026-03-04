// Package correlate provides cross-signal correlation across services.
// Unlike explain (which focuses on one service), correlate finds causal
// chains and temporal patterns across the entire system.
package correlate

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// Options configures the correlate command.
type Options struct {
	Duration     int    // minutes to look back
	Service      string // optional: focus on a specific service
	BucketSize   int    // seconds per time bucket (default 60)
	MinEvents    int    // minimum events in a bucket to count as a cluster
	AnthropicKey string // for AI analysis
}

// Signal represents a timestamped event from any telemetry source.
type Signal struct {
	Timestamp   time.Time
	Source      string // "logs", "traces"
	Service     string
	Severity    string // log severity or trace status
	Summary     string // short description
	DurationMs  float64
	IsError     bool
}

// Cluster represents a temporal cluster of correlated signals.
type Cluster struct {
	Start    time.Time
	End      time.Time
	Signals  []Signal
	Services map[string]int // service → signal count
	Errors   int
	Score    float64 // severity score 0-100
}

// PropagationEdge represents a causal link between services.
type PropagationEdge struct {
	From      string
	To        string
	DelayMs   float64 // average delay between signals
	Count     int     // number of correlated events
	ErrorRate float64 // % of correlated events that are errors
}

// Result holds the full correlation analysis.
type Result struct {
	TimeRange    time.Duration
	Services     []types.Service
	Signals      []Signal
	Clusters     []Cluster
	Propagation  []PropagationEdge
	CollectedAt  time.Time
}

// Run collects signals and performs correlation analysis.
func Run(ctx context.Context, client signoz.SignozQuerier, instanceName string, opts Options) (*Result, error) {
	if opts.BucketSize <= 0 {
		opts.BucketSize = 60
	}
	if opts.MinEvents <= 0 {
		opts.MinEvents = 3
	}

	result := &Result{
		TimeRange:   time.Duration(opts.Duration) * time.Minute,
		CollectedAt: time.Now().UTC(),
	}

	// 1. Get all services
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}
	result.Services = services

	// Filter to specific service or all with errors
	targetServices := services
	if opts.Service != "" {
		targetServices = nil
		for _, s := range services {
			if s.Name == opts.Service {
				targetServices = append(targetServices, s)
				break
			}
		}
		if len(targetServices) == 0 {
			return nil, fmt.Errorf("service %q not found", opts.Service)
		}
	}

	// 2. Collect signals from all target services
	for _, svc := range targetServices {
		// Error logs
		if logResult, err := client.QueryLogs(ctx, svc.Name, opts.Duration, 100, "error"); err == nil {
			for _, log := range logResult.Logs {
				body := log.Body
				if len(body) > 120 {
					body = body[:120] + "..."
				}
				result.Signals = append(result.Signals, Signal{
					Timestamp: log.Timestamp,
					Source:    "logs",
					Service:   svc.Name,
					Severity:  log.SeverityText,
					Summary:   body,
					IsError:   true,
				})
			}
		}

		// Traces (focus on errors and slow spans)
		if traceResult, err := client.QueryTraces(ctx, svc.Name, opts.Duration, 100); err == nil {
			for _, t := range traceResult.Traces {
				isError := t.StatusCode != "" && t.StatusCode != "OK" && t.StatusCode != "0"
				isSlow := t.DurationMs() > 1000
				if !isError && !isSlow {
					continue
				}
				summary := t.OperationName
				if isError {
					summary += fmt.Sprintf(" [status:%s]", t.StatusCode)
				}
				if isSlow {
					summary += fmt.Sprintf(" [%.0fms]", t.DurationMs())
				}
				result.Signals = append(result.Signals, Signal{
					Timestamp:  t.Timestamp,
					Source:     "traces",
					Service:    t.ServiceName,
					Severity:   t.StatusCode,
					Summary:    summary,
					DurationMs: t.DurationMs(),
					IsError:    isError,
				})
			}
		}
	}

	// Sort all signals by time
	sort.Slice(result.Signals, func(i, j int) bool {
		return result.Signals[i].Timestamp.Before(result.Signals[j].Timestamp)
	})

	// 3. Find temporal clusters
	result.Clusters = findClusters(result.Signals, opts.BucketSize, opts.MinEvents)

	// 4. Detect propagation patterns
	result.Propagation = detectPropagation(result.Signals, opts.BucketSize)

	return result, nil
}

// findClusters groups signals into time buckets and identifies clusters.
func findClusters(signals []Signal, bucketSec, minEvents int) []Cluster {
	if len(signals) == 0 {
		return nil
	}

	bucketDur := time.Duration(bucketSec) * time.Second

	// Group signals into time buckets
	type bucket struct {
		start   time.Time
		signals []Signal
	}

	var buckets []bucket
	var current *bucket

	for _, sig := range signals {
		if current == nil || sig.Timestamp.Sub(current.start) >= bucketDur {
			if current != nil && len(current.signals) >= minEvents {
				buckets = append(buckets, *current)
			}
			current = &bucket{start: sig.Timestamp}
		}
		current.signals = append(current.signals, sig)
	}
	if current != nil && len(current.signals) >= minEvents {
		buckets = append(buckets, *current)
	}

	// Merge adjacent buckets and build clusters
	var clusters []Cluster
	for _, b := range buckets {
		c := Cluster{
			Start:    b.signals[0].Timestamp,
			End:      b.signals[len(b.signals)-1].Timestamp,
			Signals:  b.signals,
			Services: make(map[string]int),
		}
		for _, sig := range b.signals {
			c.Services[sig.Service]++
			if sig.IsError {
				c.Errors++
			}
		}
		// Score: more services + more errors = higher severity
		svcFactor := math.Min(float64(len(c.Services))/3.0, 1.0) * 40
		errFactor := math.Min(float64(c.Errors)/float64(len(c.Signals)), 1.0) * 40
		volFactor := math.Min(float64(len(c.Signals))/20.0, 1.0) * 20
		c.Score = svcFactor + errFactor + volFactor
		clusters = append(clusters, c)
	}

	// Sort by score descending
	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Score > clusters[j].Score
	})

	return clusters
}

// detectPropagation finds temporal patterns suggesting error propagation between services.
func detectPropagation(signals []Signal, bucketSec int) []PropagationEdge {
	if len(signals) < 2 {
		return nil
	}

	window := time.Duration(bucketSec) * time.Second

	// For each pair of services, check if errors in service A
	// are followed by errors in service B within the time window.
	type edgeKey struct{ from, to string }
	edges := make(map[edgeKey]*PropagationEdge)

	errorSignals := make([]Signal, 0)
	for _, s := range signals {
		if s.IsError {
			errorSignals = append(errorSignals, s)
		}
	}

	for i, a := range errorSignals {
		for j := i + 1; j < len(errorSignals); j++ {
			b := errorSignals[j]
			delay := b.Timestamp.Sub(a.Timestamp)
			if delay > window {
				break // sorted by time, no more matches
			}
			if delay <= 0 || a.Service == b.Service {
				continue
			}

			key := edgeKey{from: a.Service, to: b.Service}
			edge, exists := edges[key]
			if !exists {
				edge = &PropagationEdge{From: a.Service, To: b.Service}
				edges[key] = edge
			}
			edge.Count++
			edge.DelayMs += float64(delay.Milliseconds())
		}
	}

	// Calculate averages and filter noise
	var result []PropagationEdge
	for _, edge := range edges {
		if edge.Count < 2 {
			continue // need at least 2 correlated events
		}
		edge.DelayMs /= float64(edge.Count)
		result = append(result, *edge)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	return result
}

// Render prints the correlation results to terminal.
func Render(r *Result) {
	fmt.Println()
	fmt.Printf("\n━━━ %s ━━━\n", "Cross-Signal Correlation")
	fmt.Printf("  Time window: %s  |  Signals: %d  |  Services: %d\n\n",
		r.TimeRange, len(r.Signals), len(r.Services))

	// Service summary
	fmt.Printf("\n▸ %s\n", "Service Health")
	for _, svc := range r.Services {
		rate := svc.ErrorRate
		if svc.NumCalls > 0 && rate == 0 {
			rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}
		status := "✅"
		if rate > 5 {
			status = "🔴"
		} else if rate > 1 {
			status = "🟡"
		}
		fmt.Printf("  %s %-30s %6d calls  %4d errors  (%.2f%%)\n",
			status, svc.Name, svc.NumCalls, svc.NumErrors, rate)
	}

	// Clusters
	if len(r.Clusters) > 0 {
		fmt.Println()
		fmt.Printf("\n▸ %s\n", fmt.Sprintf("Event Clusters (%d found)", len(r.Clusters)))
		limit := 5
		if len(r.Clusters) < limit {
			limit = len(r.Clusters)
		}
		for i, c := range r.Clusters[:limit] {
			severity := "LOW"
			color := "green"
			if c.Score >= 60 {
				severity = "CRITICAL"
				color = "red"
			} else if c.Score >= 30 {
				severity = "MEDIUM"
				color = "yellow"
			}
			_ = color

			svcs := make([]string, 0, len(c.Services))
			for s, n := range c.Services {
				svcs = append(svcs, fmt.Sprintf("%s(%d)", s, n))
			}
			sort.Strings(svcs)

			fmt.Printf("\n  Cluster #%d — %s (score: %.0f)\n", i+1, severity, c.Score)
			fmt.Printf("    Time: %s → %s (%s)\n",
				c.Start.Format("15:04:05"), c.End.Format("15:04:05"),
				c.End.Sub(c.Start).Round(time.Second))
			fmt.Printf("    Signals: %d (%d errors)\n", len(c.Signals), c.Errors)
			fmt.Printf("    Services: %s\n", strings.Join(svcs, ", "))

			// Show top signals in cluster
			showMax := 5
			if len(c.Signals) < showMax {
				showMax = len(c.Signals)
			}
			for _, sig := range c.Signals[:showMax] {
				icon := "📝"
				if sig.Source == "traces" {
					icon = "🔗"
				}
				errIcon := ""
				if sig.IsError {
					errIcon = " ❌"
				}
				fmt.Printf("    %s [%s] %s: %s%s\n",
					icon, sig.Timestamp.Format("15:04:05"), sig.Service, sig.Summary, errIcon)
			}
			if len(c.Signals) > showMax {
				fmt.Printf("    ... and %d more signals\n", len(c.Signals)-showMax)
			}
		}
	} else {
		fmt.Println()
		fmt.Println("  No event clusters detected — system looks quiet ✅")
	}

	// Propagation
	if len(r.Propagation) > 0 {
		fmt.Println()
		fmt.Printf("\n▸ %s\n", "Error Propagation Patterns")
		for _, edge := range r.Propagation {
			fmt.Printf("  %s → %s  (%d correlated events, avg delay: %.0fms)\n",
				edge.From, edge.To, edge.Count, edge.DelayMs)
		}
	}

	fmt.Println()
}

// RenderMarkdown renders correlation results as markdown.
func RenderMarkdown(r *Result) string {
	var b strings.Builder

	b.WriteString("# Cross-Signal Correlation Report\n\n")
	b.WriteString(fmt.Sprintf("**Time window:** %s | **Signals:** %d | **Services:** %d\n\n",
		r.TimeRange, len(r.Signals), len(r.Services)))

	b.WriteString("## Service Health\n\n")
	b.WriteString("| Service | Calls | Errors | Error Rate | Status |\n")
	b.WriteString("|---------|------:|-------:|-----------:|--------|\n")
	for _, svc := range r.Services {
		rate := svc.ErrorRate
		if svc.NumCalls > 0 && rate == 0 {
			rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}
		status := "✅ Healthy"
		if rate > 5 {
			status = "🔴 Critical"
		} else if rate > 1 {
			status = "🟡 Degraded"
		}
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %.2f%% | %s |\n",
			svc.Name, svc.NumCalls, svc.NumErrors, rate, status))
	}

	if len(r.Clusters) > 0 {
		b.WriteString(fmt.Sprintf("\n## Event Clusters (%d)\n\n", len(r.Clusters)))
		for i, c := range r.Clusters {
			severity := "🟢 LOW"
			if c.Score >= 60 {
				severity = "🔴 CRITICAL"
			} else if c.Score >= 30 {
				severity = "🟡 MEDIUM"
			}
			b.WriteString(fmt.Sprintf("### Cluster #%d — %s (score: %.0f)\n\n", i+1, severity, c.Score))
			b.WriteString(fmt.Sprintf("- **Time:** %s → %s\n", c.Start.Format("15:04:05"), c.End.Format("15:04:05")))
			b.WriteString(fmt.Sprintf("- **Signals:** %d (%d errors)\n", len(c.Signals), c.Errors))
			svcs := make([]string, 0, len(c.Services))
			for s := range c.Services {
				svcs = append(svcs, s)
			}
			sort.Strings(svcs)
			b.WriteString(fmt.Sprintf("- **Services:** %s\n\n", strings.Join(svcs, ", ")))
		}
	}

	if len(r.Propagation) > 0 {
		b.WriteString("## Error Propagation\n\n")
		b.WriteString("```mermaid\ngraph LR\n")
		for _, edge := range r.Propagation {
			b.WriteString(fmt.Sprintf("    %s -->|%dx, ~%.0fms| %s\n",
				sanitizeMermaid(edge.From), edge.Count, edge.DelayMs, sanitizeMermaid(edge.To)))
		}
		b.WriteString("```\n")
	}

	return b.String()
}

func sanitizeMermaid(s string) string {
	r := strings.NewReplacer("-", "_", ".", "_", "/", "_")
	return r.Replace(s)
}

// BuildAIPrompt creates a prompt for AI correlation analysis.
func BuildAIPrompt(r *Result) string {
	var b strings.Builder

	b.WriteString("You are an expert SRE performing cross-signal correlation analysis.\n")
	b.WriteString("Analyze the following observability data from multiple services and identify:\n")
	b.WriteString("1. Root cause chains — which service triggered the cascade?\n")
	b.WriteString("2. Temporal correlations — events that happen together\n")
	b.WriteString("3. Propagation paths — how errors spread between services\n")
	b.WriteString("4. Actionable recommendations\n\n")

	b.WriteString(RenderMarkdown(r))

	// Add raw signal timeline
	b.WriteString("\n## Signal Timeline (chronological)\n\n")
	limit := 100
	if len(r.Signals) < limit {
		limit = len(r.Signals)
	}
	for _, sig := range r.Signals[:limit] {
		errMark := ""
		if sig.IsError {
			errMark = " [ERROR]"
		}
		b.WriteString(fmt.Sprintf("- %s | %s | %s | %s%s\n",
			sig.Timestamp.Format("15:04:05.000"), sig.Source, sig.Service, sig.Summary, errMark))
	}

	b.WriteString("\n## Instructions\n\n")
	b.WriteString("Provide:\n")
	b.WriteString("1. **Incident Summary** — What happened, in plain English\n")
	b.WriteString("2. **Root Cause Chain** — The sequence of events from trigger to impact\n")
	b.WriteString("3. **Blast Radius** — Which services are affected and how\n")
	b.WriteString("4. **Remediation Steps** — Ordered by priority\n")
	b.WriteString("5. **Prevention** — How to avoid this in the future\n\n")
	b.WriteString("Be specific. Reference actual timestamps, services, and error messages.\n")

	return b.String()
}

// RunWithAI performs correlation and streams AI analysis.
func RunWithAI(ctx context.Context, client signoz.SignozQuerier, instanceName string, opts Options, writer interface{ Write([]byte) (int, error) }) error {
	result, err := Run(ctx, client, instanceName, opts)
	if err != nil {
		return err
	}

	Render(result)

	if len(result.Signals) == 0 {
		fmt.Println("  No signals to analyze — skipping AI correlation.")
		return nil
	}

	fmt.Println()
	fmt.Printf("\n▸ %s\n", "AI Correlation Analysis")
	fmt.Println()

	prompt := BuildAIPrompt(result)
	analyzer := ai.New(opts.AnthropicKey)
	return analyzer.Analyze(prompt, writer)
}

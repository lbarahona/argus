package top

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/signoz"
)

// SortField determines how services are sorted.
type SortField int

const (
	SortByErrors SortField = iota
	SortByErrorRate
	SortByCalls
	SortByName
)

// Options configures the top view.
type Options struct {
	Limit    int
	SortBy   SortField
	Duration int // minutes for log lookup
}

// ServiceInfo aggregates service data for the top view.
type ServiceInfo struct {
	Name         string
	Calls        int
	Errors       int
	ErrorRate    float64
	RecentErrors int // errors from logs in the duration window
	Severity     string // "critical", "warning", "healthy"
}

// Result holds the top view data.
type Result struct {
	Services    []ServiceInfo
	GeneratedAt time.Time
	Instance    string
	Duration    int
}

// Run fetches and ranks services.
func Run(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*Result, error) {
	services, err := client.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing services: %w", err)
	}

	// Also get recent error logs for richer context
	dur := opts.Duration
	if dur <= 0 {
		dur = 60
	}

	recentErrorCounts := make(map[string]int)
	if result, err := client.QueryLogs(ctx, "", dur, 500, "ERROR"); err == nil {
		for _, l := range result.Logs {
			recentErrorCounts[l.ServiceName]++
		}
	}

	var infos []ServiceInfo
	for _, s := range services {
		severity := "healthy"
		if s.ErrorRate > 5 {
			severity = "critical"
		} else if s.ErrorRate > 1 {
			severity = "warning"
		} else if s.NumErrors > 0 {
			severity = "warning"
		}

		infos = append(infos, ServiceInfo{
			Name:         s.Name,
			Calls:        s.NumCalls,
			Errors:       s.NumErrors,
			ErrorRate:    s.ErrorRate,
			RecentErrors: recentErrorCounts[s.Name],
			Severity:     severity,
		})
	}

	// Sort
	switch opts.SortBy {
	case SortByErrors:
		sort.Slice(infos, func(i, j int) bool { return infos[i].Errors > infos[j].Errors })
	case SortByErrorRate:
		sort.Slice(infos, func(i, j int) bool { return infos[i].ErrorRate > infos[j].ErrorRate })
	case SortByCalls:
		sort.Slice(infos, func(i, j int) bool { return infos[i].Calls > infos[j].Calls })
	case SortByName:
		sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit < len(infos) {
		infos = infos[:limit]
	}

	return &Result{
		Services:    infos,
		GeneratedAt: time.Now(),
		Instance:    instKey,
		Duration:    dur,
	}, nil
}

// RenderTerminal displays the top view.
func (r *Result) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n🔭 ARGUS TOP — %s\n", r.Instance)
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "  %s | Recent errors: last %d min\n\n", r.GeneratedAt.Format("15:04:05"), r.Duration)

	if len(r.Services) == 0 {
		fmt.Fprintf(w, "  No services found.\n")
		return
	}

	// Header
	fmt.Fprintf(w, "  %-35s %10s %10s %9s %8s  %s\n",
		"SERVICE", "CALLS", "ERRORS", "ERR RATE", "RECENT", "HEALTH")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 82))

	for _, s := range r.Services {
		icon := severityIcon(s.Severity)
		bar := errorBar(s.ErrorRate)

		fmt.Fprintf(w, "  %-35s %10d %10d %8.1f%% %8d  %s %s\n",
			truncate(s.Name, 35), s.Calls, s.Errors, s.ErrorRate, s.RecentErrors, icon, bar)
	}

	fmt.Fprintf(w, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

func severityIcon(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "warning":
		return "🟡"
	default:
		return "🟢"
	}
}

func errorBar(rate float64) string {
	blocks := int(rate / 2) // each block = 2%
	if blocks > 25 {
		blocks = 25
	}
	if blocks == 0 && rate > 0 {
		blocks = 1
	}
	return strings.Repeat("█", blocks) + strings.Repeat("░", 25-blocks)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}


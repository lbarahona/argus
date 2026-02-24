package diff

import (
	"context"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
)

// ServiceDiff represents the change in a service between two windows.
type ServiceDiff struct {
	Name          string
	CallsBefore   int
	CallsAfter    int
	ErrorsBefore  int
	ErrorsAfter   int
	RateBefore    float64
	RateAfter     float64
	CallsChange   float64 // percentage
	ErrorsChange  float64 // percentage
	RateChange    float64 // absolute change in error rate
	Status        string  // "improved", "degraded", "stable", "new", "gone"
}

// DiffResult holds comparison data between two time windows.
type DiffResult struct {
	Instance     string
	WindowA      string // e.g., "60-120 min ago"
	WindowB      string // e.g., "0-60 min ago"
	DurationMin  int
	Services     []ServiceDiff
	Summary      DiffSummary
	GeneratedAt  time.Time
}

// DiffSummary provides a high-level overview.
type DiffSummary struct {
	TotalCallsBefore  int
	TotalCallsAfter   int
	TotalErrorsBefore int
	TotalErrorsAfter  int
	Improved          int
	Degraded          int
	Stable            int
	New               int
	Gone              int
}

// Options configures the diff comparison.
type Options struct {
	Duration int // minutes per window (default 60, so compares last hour vs previous hour)
}

// Compare fetches service data for two consecutive time windows and computes diffs.
func Compare(ctx context.Context, client signoz.SignozQuerier, instKey string, opts Options) (*DiffResult, error) {
	// We can only get current services snapshot from Signoz /services endpoint.
	// For a real diff, we'd need historical data. Since Signoz services endpoint
	// returns aggregate data, we'll fetch error logs from two time windows to compare.

	dur := opts.Duration
	if dur <= 0 {
		dur = 60
	}

	// Window B (recent): 0 to dur minutes ago
	recentLogs, err := client.QueryLogs(ctx, "", dur, 500, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying recent logs: %w", err)
	}

	// Window A (previous): dur to 2*dur minutes ago
	previousLogs, err := client.QueryLogs(ctx, "", dur*2, 500, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying previous logs: %w", err)
	}

	// Current services for call counts
	services, _ := client.ListServices(ctx)

	now := time.Now()
	cutoff := now.Add(-time.Duration(dur) * time.Minute)

	// Split previous logs into window A (older) and window B (recent)
	recentErrors := countByService(recentLogs.Logs, cutoff, now)
	previousErrors := countByService(previousLogs.Logs, now.Add(-time.Duration(dur*2)*time.Minute), cutoff)

	// Build service map from current services
	serviceMap := make(map[string]types.Service)
	for _, s := range services {
		serviceMap[s.Name] = s
	}

	// Collect all service names
	allServices := make(map[string]bool)
	for k := range recentErrors {
		allServices[k] = true
	}
	for k := range previousErrors {
		allServices[k] = true
	}
	for _, s := range services {
		allServices[s.Name] = true
	}

	result := &DiffResult{
		Instance:    instKey,
		WindowA:     fmt.Sprintf("%d-%d min ago", dur*2, dur),
		WindowB:     fmt.Sprintf("0-%d min ago", dur),
		DurationMin: dur,
		GeneratedAt: now,
	}

	for name := range allServices {
		before := previousErrors[name]
		after := recentErrors[name]

		d := ServiceDiff{
			Name:         name,
			ErrorsBefore: before,
			ErrorsAfter:  after,
		}

		// Pull call data from current services
		if s, ok := serviceMap[name]; ok {
			d.CallsAfter = s.NumCalls
			d.RateAfter = s.ErrorRate
		}

		// Compute changes
		if before > 0 {
			d.ErrorsChange = (float64(after) - float64(before)) / float64(before) * 100
		} else if after > 0 {
			d.ErrorsChange = 100
		}

		// Determine status
		switch {
		case before == 0 && after > 0:
			d.Status = "new"
			result.Summary.New++
		case before > 0 && after == 0:
			d.Status = "gone"
			result.Summary.Gone++
		case after > before && d.ErrorsChange > 20:
			d.Status = "degraded"
			result.Summary.Degraded++
		case after < before && d.ErrorsChange < -20:
			d.Status = "improved"
			result.Summary.Improved++
		default:
			d.Status = "stable"
			result.Summary.Stable++
		}

		result.Services = append(result.Services, d)
		result.Summary.TotalErrorsBefore += before
		result.Summary.TotalErrorsAfter += after
	}

	// Sort: degraded first, then by error count
	sort.Slice(result.Services, func(i, j int) bool {
		order := map[string]int{"degraded": 0, "new": 1, "stable": 2, "improved": 3, "gone": 4}
		oi, oj := order[result.Services[i].Status], order[result.Services[j].Status]
		if oi != oj {
			return oi < oj
		}
		return result.Services[i].ErrorsAfter > result.Services[j].ErrorsAfter
	})

	return result, nil
}

func countByService(logs []types.LogEntry, from, to time.Time) map[string]int {
	counts := make(map[string]int)
	for _, l := range logs {
		if (l.Timestamp.Equal(from) || l.Timestamp.After(from)) && l.Timestamp.Before(to) {
			counts[l.ServiceName]++
		}
	}
	return counts
}

// RenderTerminal displays the diff in a terminal.
func (r *DiffResult) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n🔭 ARGUS SERVICE DIFF\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "  Instance: %s  |  Window: %d min\n", r.Instance, r.DurationMin)
	fmt.Fprintf(w, "  Comparing: [%s] vs [%s]\n\n", r.WindowA, r.WindowB)

	// Summary
	totalChange := r.Summary.TotalErrorsAfter - r.Summary.TotalErrorsBefore
	changeIcon := "→"
	if totalChange > 0 {
		changeIcon = "↑"
	} else if totalChange < 0 {
		changeIcon = "↓"
	}
	fmt.Fprintf(w, "  📊 Summary: %d errors → %d errors (%s%d)\n",
		r.Summary.TotalErrorsBefore, r.Summary.TotalErrorsAfter, changeIcon, int(math.Abs(float64(totalChange))))
	fmt.Fprintf(w, "     🔴 %d degraded  🟢 %d improved  ⚪ %d stable  🆕 %d new  👻 %d gone\n\n",
		r.Summary.Degraded, r.Summary.Improved, r.Summary.Stable, r.Summary.New, r.Summary.Gone)

	if len(r.Services) == 0 {
		fmt.Fprintf(w, "  No services with errors found.\n")
		return
	}

	// Table header
	fmt.Fprintf(w, "  %-30s %8s %8s %10s %s\n", "SERVICE", "BEFORE", "AFTER", "CHANGE", "STATUS")
	fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 70))

	for _, s := range r.Services {
		if s.ErrorsBefore == 0 && s.ErrorsAfter == 0 {
			continue // skip services with no errors in either window
		}

		statusIcon := statusEmoji(s.Status)
		change := fmt.Sprintf("%+.0f%%", s.ErrorsChange)
		if s.ErrorsBefore == 0 && s.ErrorsAfter == 0 {
			change = "—"
		}

		fmt.Fprintf(w, "  %-30s %8d %8d %10s %s %s\n",
			truncate(s.Name, 30), s.ErrorsBefore, s.ErrorsAfter, change, statusIcon, s.Status)
	}

	fmt.Fprintf(w, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

func statusEmoji(status string) string {
	switch status {
	case "degraded":
		return "🔴"
	case "improved":
		return "🟢"
	case "new":
		return "🆕"
	case "gone":
		return "👻"
	default:
		return "⚪"
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}


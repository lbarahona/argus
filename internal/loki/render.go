package loki

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
	colorWhite  = "\033[37m"
)

// FormatJSON marshals any value as indented JSON.
func FormatJSON(v interface{}) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error())
	}
	return string(data)
}

// FormatLabels renders label names in a readable format.
func FormatLabels(labels []string) string {
	if len(labels) == 0 {
		return fmt.Sprintf("\n  %s(no labels found)%s\n\n", colorGray, colorReset)
	}

	sort.Strings(labels)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s🏷️  Labels%s (%d)\n\n", colorBold, colorReset, len(labels)))
	for _, l := range labels {
		b.WriteString(fmt.Sprintf("  • %s%s%s\n", colorCyan, l, colorReset))
	}
	b.WriteString("\n")
	return b.String()
}

// FormatLabelValues renders label values in a readable format.
func FormatLabelValues(label string, values []string) string {
	if len(values) == 0 {
		return fmt.Sprintf("\n  %s(no values for %q)%s\n\n", colorGray, label, colorReset)
	}

	sort.Strings(values)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s🏷️  Values for %s%s (%d)\n\n", colorBold, label, colorReset, len(values)))
	for _, v := range values {
		b.WriteString(fmt.Sprintf("  • %s%s%s\n", colorWhite, v, colorReset))
	}
	b.WriteString("\n")
	return b.String()
}

// FormatLogEntries renders parsed log entries.
func FormatLogEntries(entries []LogEntry, showLabels bool) string {
	if len(entries) == 0 {
		return fmt.Sprintf("\n  %s(no log entries found)%s\n\n", colorGray, colorReset)
	}

	// Sort by timestamp (newest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s📋 Log Entries%s (%d)\n\n", colorBold, colorReset, len(entries)))

	for _, e := range entries {
		ts := e.Timestamp.Format("15:04:05.000")
		b.WriteString(fmt.Sprintf("  %s%s%s ", colorGray, ts, colorReset))

		if showLabels {
			labelStr := formatLabelSet(e.Labels)
			if labelStr != "" {
				b.WriteString(fmt.Sprintf("%s%s%s ", colorCyan, labelStr, colorReset))
			}
		}

		// Truncate long lines
		line := e.Line
		if len(line) > 200 {
			line = line[:197] + "..."
		}

		// Color based on content
		if containsError(line) {
			b.WriteString(fmt.Sprintf("%s%s%s\n", colorRed, line, colorReset))
		} else if containsWarn(line) {
			b.WriteString(fmt.Sprintf("%s%s%s\n", colorYellow, line, colorReset))
		} else {
			b.WriteString(fmt.Sprintf("%s\n", line))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// FormatSeries renders series data.
func FormatSeries(series []map[string]string) string {
	if len(series) == 0 {
		return fmt.Sprintf("\n  %s(no matching series)%s\n\n", colorGray, colorReset)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s📊 Series%s (%d)\n\n", colorBold, colorReset, len(series)))

	for i, s := range series {
		if i >= 50 {
			b.WriteString(fmt.Sprintf("  %s... and %d more%s\n", colorGray, len(series)-50, colorReset))
			break
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", colorCyan, formatLabelSet(s), colorReset))
	}
	b.WriteString("\n")
	return b.String()
}

// FormatStats renders index statistics.
func FormatStats(stats *StatsResponse) string {
	if stats == nil {
		return fmt.Sprintf("\n  %s(no stats available)%s\n\n", colorGray, colorReset)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s📈 Index Statistics%s\n\n", colorBold, colorReset))
	b.WriteString(fmt.Sprintf("  Streams: %s%d%s\n", colorWhite, stats.Streams, colorReset))
	b.WriteString(fmt.Sprintf("  Chunks:  %s%d%s\n", colorWhite, stats.Chunks, colorReset))
	b.WriteString(fmt.Sprintf("  Entries: %s%d%s\n", colorWhite, stats.Entries, colorReset))
	b.WriteString(fmt.Sprintf("  Bytes:   %s%s%s\n", colorWhite, humanBytes(stats.Bytes), colorReset))
	b.WriteString("\n")
	return b.String()
}

// FormatStatus renders health and version information.
func FormatStatus(healthy bool, latency time.Duration, info *BuildInfo) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s🔍 Loki Status%s\n\n", colorBold, colorReset))

	if healthy {
		b.WriteString(fmt.Sprintf("  Health:  %s● Healthy%s (%s)\n", colorGreen, colorReset, latency.Round(time.Millisecond)))
	} else {
		b.WriteString(fmt.Sprintf("  Health:  %s● Unhealthy%s (%s)\n", colorRed, colorReset, latency.Round(time.Millisecond)))
	}

	if info != nil {
		b.WriteString(fmt.Sprintf("  Version: %s%s%s\n", colorWhite, info.Version, colorReset))
		if info.Branch != "" {
			b.WriteString(fmt.Sprintf("  Branch:  %s\n", info.Branch))
		}
		if info.GoVersion != "" {
			b.WriteString(fmt.Sprintf("  Go:      %s\n", info.GoVersion))
		}
	}

	b.WriteString("\n")
	return b.String()
}

// FormatSummary renders the aggregated summary.
func FormatSummary(s *Summary) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\n%s🔍 Loki Summary%s\n\n", colorBold, colorReset))

	if s.Healthy {
		b.WriteString(fmt.Sprintf("  Health:  %s● Healthy%s (%s)\n", colorGreen, colorReset, s.Latency.Round(time.Millisecond)))
	} else {
		b.WriteString(fmt.Sprintf("  Health:  %s● Unhealthy%s (%s)\n", colorRed, colorReset, s.Latency.Round(time.Millisecond)))
	}

	if s.Version != "" {
		b.WriteString(fmt.Sprintf("  Version: %s%s%s\n", colorWhite, s.Version, colorReset))
	}

	b.WriteString(fmt.Sprintf("  Labels:  %d\n", s.Labels))
	b.WriteString(fmt.Sprintf("  Series:  %d\n", s.Series))

	if s.Stats != nil {
		b.WriteString(fmt.Sprintf("\n  %sIngestion (last 1h):%s\n", colorBold, colorReset))
		b.WriteString(fmt.Sprintf("    Streams: %d\n", s.Stats.Streams))
		b.WriteString(fmt.Sprintf("    Entries: %d\n", s.Stats.Entries))
		b.WriteString(fmt.Sprintf("    Bytes:   %s\n", humanBytes(s.Stats.Bytes)))
	}

	b.WriteString("\n")
	return b.String()
}

// formatLabelSet renders a label map as {key=value, ...}.
func formatLabelSet(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, fmt.Sprintf("%s=%q", k, v))
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ", ") + "}"
}

// humanBytes formats bytes into human-readable form.
func humanBytes(b uint64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
		tb = 1024 * gb
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1f TB", float64(b)/float64(tb))
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// containsError checks if a log line likely contains error content.
func containsError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "error") || strings.Contains(lower, "err=") ||
		strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") ||
		strings.Contains(lower, "exception")
}

// containsWarn checks if a log line likely contains warning content.
func containsWarn(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "warn") || strings.Contains(lower, "warning")
}

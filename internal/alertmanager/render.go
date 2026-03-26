package alertmanager

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

// FormatAlerts renders alerts in a human-readable format.
func FormatAlerts(alerts []Alert, showAll bool) string {
	if len(alerts) == 0 {
		return fmt.Sprintf("\n  %s✅ No alerts firing%s\n", colorGreen, colorReset)
	}

	var b strings.Builder

	summary := BuildSummary(alerts)
	b.WriteString(fmt.Sprintf("\n%s🔔 Alertmanager — %d alerts%s", colorBold, summary.TotalAlerts, colorReset))
	b.WriteString(fmt.Sprintf(" (%s%d active%s", colorRed, summary.ActiveAlerts, colorReset))
	if summary.SuppressedAlerts > 0 {
		b.WriteString(fmt.Sprintf(", %s%d suppressed%s", colorYellow, summary.SuppressedAlerts, colorReset))
	}
	b.WriteString(")\n\n")

	// Sort by severity (critical first), then by starts_at
	sorted := make([]Alert, len(alerts))
	copy(sorted, alerts)
	sort.Slice(sorted, func(i, j int) bool {
		si := severityRank(sorted[i].Labels["severity"])
		sj := severityRank(sorted[j].Labels["severity"])
		if si != sj {
			return si < sj
		}
		return sorted[i].StartsAt.After(sorted[j].StartsAt)
	})

	for _, a := range sorted {
		if !showAll && a.Status.State != "active" {
			continue
		}
		b.WriteString(formatSingleAlert(a))
	}

	// Severity breakdown
	b.WriteString(fmt.Sprintf("%s━━━ ", colorGray))
	for _, sev := range []string{"critical", "warning", "info", "unknown"} {
		if count, ok := summary.BySeverity[sev]; ok && count > 0 {
			b.WriteString(fmt.Sprintf("%s%d %s%s ", severityColor(sev), count, sev, colorGray))
		}
	}
	b.WriteString(fmt.Sprintf("━━━%s\n", colorReset))

	return b.String()
}

func formatSingleAlert(a Alert) string {
	var b strings.Builder

	sev := a.Labels["severity"]
	icon := SeverityIcon(sev)
	color := severityColor(sev)
	name := a.Labels["alertname"]
	if name == "" {
		name = "unknown"
	}

	state := a.Status.State
	stateColor := colorGreen
	if state == "active" {
		stateColor = colorRed
	} else if state == "suppressed" {
		stateColor = colorYellow
	}

	b.WriteString(fmt.Sprintf("  %s%s %s%s%s", color, icon, colorBold, name, colorReset))
	b.WriteString(fmt.Sprintf("  %s[%s]%s", stateColor, state, colorReset))
	b.WriteString(fmt.Sprintf("  %sfiring %s%s\n", colorGray, formatDuration(time.Since(a.StartsAt)), colorReset))

	// Instance / service / job
	for _, key := range []string{"instance", "service", "job", "namespace", "pod"} {
		if val, ok := a.Labels[key]; ok {
			b.WriteString(fmt.Sprintf("    %s%s:%s %s\n", colorCyan, key, colorReset, val))
		}
	}

	// Summary or description annotation
	if summary, ok := a.Annotations["summary"]; ok {
		b.WriteString(fmt.Sprintf("    %s%s%s\n", colorWhite, summary, colorReset))
	} else if desc, ok := a.Annotations["description"]; ok {
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		b.WriteString(fmt.Sprintf("    %s%s%s\n", colorWhite, desc, colorReset))
	}

	// Silenced by
	if len(a.Status.SilencedBy) > 0 {
		b.WriteString(fmt.Sprintf("    %s🔇 silenced by: %s%s\n", colorYellow, strings.Join(a.Status.SilencedBy, ", "), colorReset))
	}

	b.WriteString("\n")
	return b.String()
}

// FormatSilences renders silences in a human-readable format.
func FormatSilences(silences []Silence, showExpired bool) string {
	if len(silences) == 0 {
		return fmt.Sprintf("\n  %sNo silences%s\n", colorGray, colorReset)
	}

	var b strings.Builder
	active := 0
	for _, s := range silences {
		if s.Status.State == "active" {
			active++
		}
	}

	b.WriteString(fmt.Sprintf("\n%s🔇 Silences — %d active%s\n\n", colorBold, active, colorReset))

	for _, s := range silences {
		if !showExpired && s.Status.State == "expired" {
			continue
		}
		b.WriteString(formatSingleSilence(s))
	}

	return b.String()
}

func formatSingleSilence(s Silence) string {
	var b strings.Builder

	stateColor := colorGreen
	icon := "🔇"
	if s.Status.State == "expired" {
		stateColor = colorGray
		icon = "⏰"
	} else if s.Status.State == "pending" {
		stateColor = colorYellow
		icon = "⏳"
	}

	b.WriteString(fmt.Sprintf("  %s %s%s%s  %s%s%s\n", icon, stateColor, s.Status.State, colorReset, colorGray, s.ID[:8], colorReset))
	b.WriteString(fmt.Sprintf("    %s%s%s\n", colorWhite, s.Comment, colorReset))
	b.WriteString(fmt.Sprintf("    %sby %s%s", colorGray, s.CreatedBy, colorReset))

	if s.Status.State == "active" {
		remaining := time.Until(s.EndsAt)
		b.WriteString(fmt.Sprintf("  %sexpires in %s%s", colorCyan, formatDuration(remaining), colorReset))
	}
	b.WriteString("\n")

	// Matchers
	for _, m := range s.Matchers {
		op := "="
		if !m.IsEqual {
			op = "!="
		}
		if m.IsRegex {
			op += "~"
		}
		b.WriteString(fmt.Sprintf("    %s%s%s%s%s\n", colorCyan, m.Name, op, m.Value, colorReset))
	}
	b.WriteString("\n")
	return b.String()
}

// FormatStatus renders Alertmanager status info.
func FormatStatus(status *AMStatus, healthy bool, latency time.Duration) string {
	var b strings.Builder

	icon := "✅"
	color := colorGreen
	if !healthy {
		icon = "🔴"
		color = colorRed
	}

	b.WriteString(fmt.Sprintf("\n%s%s Alertmanager%s", color, icon, colorReset))
	b.WriteString(fmt.Sprintf("  %sv%s%s", colorGray, status.VersionInfo.Version, colorReset))
	b.WriteString(fmt.Sprintf("  %s(%dms)%s\n", colorGray, latency.Milliseconds(), colorReset))

	b.WriteString(fmt.Sprintf("  Cluster: %s (%s)\n", status.Cluster.Name, status.Cluster.Status))
	b.WriteString(fmt.Sprintf("  Peers: %d\n", len(status.Cluster.Peers)))
	for _, p := range status.Cluster.Peers {
		b.WriteString(fmt.Sprintf("    • %s (%s)\n", p.Name, p.Address))
	}

	return b.String()
}

// FormatJSON returns alerts as indented JSON.
func FormatJSON(alerts []Alert) (string, error) {
	data, err := json.MarshalIndent(alerts, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// FormatSilencesJSON returns silences as JSON.
func FormatSilencesJSON(silences []Silence) (string, error) {
	data, err := json.MarshalIndent(silences, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func severityRank(sev string) int {
	switch sev {
	case "critical":
		return 0
	case "warning":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

// SeverityIcon returns an emoji icon for the given severity level.
func SeverityIcon(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "warning":
		return "⚠️"
	case "info":
		return "ℹ️"
	default:
		return "❓"
	}
}

func severityColor(sev string) string {
	switch sev {
	case "critical":
		return colorRed
	case "warning":
		return colorYellow
	case "info":
		return colorCyan
	default:
		return colorGray
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if hours > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dd", days)
}

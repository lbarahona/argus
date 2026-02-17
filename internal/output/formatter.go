package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/lbarahona/argus/pkg/types"
)

var (
	// Colors
	primary = lipgloss.Color("#7C3AED") // purple
	success = lipgloss.Color("#10B981") // green
	danger  = lipgloss.Color("#EF4444") // red
	warning = lipgloss.Color("#F59E0B") // amber
	muted   = lipgloss.Color("#6B7280") // gray
	accent  = lipgloss.Color("#3B82F6") // blue

	// Styles
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primary).
			MarginBottom(1)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(success).
			Bold(true)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(danger).
			Bold(true)

	WarningStyle = lipgloss.NewStyle().
			Foreground(warning)

	MutedStyle = lipgloss.NewStyle().
			Foreground(muted)

	AccentStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	LabelStyle = lipgloss.NewStyle().
			Foreground(muted).
			Width(16)

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primary).
			Padding(0, 1)

	// Severity styles
	severityError = lipgloss.NewStyle().Foreground(danger).Bold(true)
	severityWarn  = lipgloss.NewStyle().Foreground(warning).Bold(true)
	severityInfo  = lipgloss.NewStyle().Foreground(accent)
	severityDebug = lipgloss.NewStyle().Foreground(muted)
)

// PrintBanner prints the Argus banner.
func PrintBanner() {
	banner := `
   ╔═══════════════════════════════════╗
   ║   🔭 ARGUS                        ║
   ║   AI-Powered Observability CLI    ║
   ╚═══════════════════════════════════╝`
	fmt.Println(lipgloss.NewStyle().Foreground(primary).Render(banner))
	fmt.Println()
}

// PrintInstances displays a table of configured instances.
func PrintInstances(instances map[string]types.Instance, defaultInst string) {
	fmt.Println(TitleStyle.Render("📡 Configured Instances"))
	fmt.Println()

	if len(instances) == 0 {
		fmt.Println(WarningStyle.Render("  No instances configured. Run: argus config init"))
		return
	}

	for key, inst := range instances {
		marker := "  "
		if key == defaultInst {
			marker = "► "
		}

		name := inst.Name
		if key == defaultInst {
			name += " (default)"
		}

		fmt.Printf("%s%s\n", marker, AccentStyle.Render(name))
		fmt.Printf("    %s %s\n", LabelStyle.Render("Key:"), key)
		fmt.Printf("    %s %s\n", LabelStyle.Render("URL:"), inst.URL)
		masked := maskKey(inst.APIKey)
		fmt.Printf("    %s %s\n", LabelStyle.Render("API Key:"), masked)
		if inst.APIVersion != "" {
			fmt.Printf("    %s %s\n", LabelStyle.Render("API Version:"), inst.APIVersion)
		}
		fmt.Println()
	}
}

// PrintHealthStatuses displays health check results.
func PrintHealthStatuses(statuses []types.HealthStatus) {
	fmt.Println(TitleStyle.Render("🏥 Instance Health"))
	fmt.Println()

	for _, s := range statuses {
		icon := SuccessStyle.Render("●")
		status := SuccessStyle.Render("healthy")
		if !s.Healthy {
			icon = ErrorStyle.Render("●")
			status = ErrorStyle.Render("unhealthy")
		}

		fmt.Printf("  %s %s — %s\n", icon, AccentStyle.Render(s.InstanceName), status)
		fmt.Printf("    %s %s\n", LabelStyle.Render("URL:"), s.URL)
		if s.Healthy {
			fmt.Printf("    %s %s\n", LabelStyle.Render("Latency:"), formatDuration(s.Latency))
		} else {
			fmt.Printf("    %s %s\n", LabelStyle.Render("Error:"), ErrorStyle.Render(s.Message))
		}
		fmt.Println()
	}
}

// PrintVersion displays version information.
func PrintVersion(version, commit, date string) {
	fmt.Println(TitleStyle.Render("🔭 Argus"))
	fmt.Printf("  %s %s\n", LabelStyle.Render("Version:"), version)
	fmt.Printf("  %s %s\n", LabelStyle.Render("Commit:"), commit)
	fmt.Printf("  %s %s\n", LabelStyle.Render("Built:"), date)
}

// PrintAnalyzing shows an analysis header.
func PrintAnalyzing(query string) {
	fmt.Println()
	fmt.Println(TitleStyle.Render("🤖 AI Analysis"))
	fmt.Printf("  %s %s\n\n", LabelStyle.Render("Query:"), query)
	fmt.Println(MutedStyle.Render(strings.Repeat("─", 50)))
	fmt.Println()
}

// PrintLogs displays formatted log entries with severity colors.
func PrintLogs(logs []types.LogEntry) {
	if len(logs) == 0 {
		fmt.Println(MutedStyle.Render("  No logs found."))
		return
	}

	fmt.Println(TitleStyle.Render(fmt.Sprintf("📋 Logs (%d entries)", len(logs))))
	fmt.Println()

	for _, log := range logs {
		ts := MutedStyle.Render(log.Timestamp.Format("15:04:05.000"))
		sev := formatSeverity(log.SeverityText)
		svc := ""
		if log.ServiceName != "" {
			svc = AccentStyle.Render("["+log.ServiceName+"]") + " "
		}

		body := log.Body
		if len(body) > 200 {
			body = body[:200] + "..."
		}

		fmt.Printf("  %s %s %s%s\n", ts, sev, svc, body)
	}
	fmt.Println()
}

// PrintServices displays a table of services with error rates.
func PrintServices(services []types.Service) {
	if len(services) == 0 {
		fmt.Println(MutedStyle.Render("  No services found."))
		return
	}

	fmt.Println(TitleStyle.Render(fmt.Sprintf("🔧 Services (%d)", len(services))))
	fmt.Println()

	// Header
	fmt.Printf("  %-35s %10s %10s %10s\n",
		AccentStyle.Render("SERVICE"),
		AccentStyle.Render("CALLS"),
		AccentStyle.Render("ERRORS"),
		AccentStyle.Render("ERR RATE"),
	)
	fmt.Printf("  %s\n", MutedStyle.Render(strings.Repeat("─", 70)))

	for _, svc := range services {
		errRate := fmt.Sprintf("%.1f%%", svc.ErrorRate)
		errStyle := MutedStyle
		if svc.ErrorRate > 5 {
			errStyle = ErrorStyle
		} else if svc.ErrorRate > 1 {
			errStyle = WarningStyle
		}

		fmt.Printf("  %-35s %10d %10d %10s\n",
			svc.Name,
			svc.NumCalls,
			svc.NumErrors,
			errStyle.Render(errRate),
		)
	}
	fmt.Println()
}

// PrintTraces displays formatted trace entries.
func PrintTraces(traces []types.TraceEntry) {
	if len(traces) == 0 {
		fmt.Println(MutedStyle.Render("  No traces found."))
		return
	}

	fmt.Println(TitleStyle.Render(fmt.Sprintf("🔍 Traces (%d spans)", len(traces))))
	fmt.Println()

	for _, t := range traces {
		ts := MutedStyle.Render(t.Timestamp.Format("15:04:05.000"))
		dur := formatTraceDuration(t.DurationMs())
		svc := AccentStyle.Render(t.ServiceName)
		op := t.OperationName

		statusIcon := SuccessStyle.Render("✓")
		if t.StatusCode == "ERROR" || t.StatusCode == "STATUS_CODE_ERROR" {
			statusIcon = ErrorStyle.Render("✗")
		}

		traceID := MutedStyle.Render(truncateID(t.TraceID))

		fmt.Printf("  %s %s %s %s %s %s\n", ts, statusIcon, svc, op, dur, traceID)
	}
	fmt.Println()
}

// PrintMetrics displays formatted metric entries.
func PrintMetrics(metrics []types.MetricEntry) {
	if len(metrics) == 0 {
		fmt.Println(MutedStyle.Render("  No metrics found."))
		return
	}

	fmt.Println(TitleStyle.Render(fmt.Sprintf("📊 Metrics (%d data points)", len(metrics))))
	fmt.Println()

	for _, m := range metrics {
		ts := MutedStyle.Render(m.Timestamp.Format("15:04:05"))
		labels := ""
		for k, v := range m.Labels {
			labels += fmt.Sprintf(" %s=%s", MutedStyle.Render(k), v)
		}
		fmt.Printf("  %s  %10.2f%s\n", ts, m.Value, labels)
	}
	fmt.Println()
}

// PrintDashboard prints a combined dashboard view.
func PrintDashboard(statuses []types.HealthStatus, services []types.Service, recentLogs []types.LogEntry) {
	fmt.Println(TitleStyle.Render("📊 Argus Dashboard"))
	fmt.Println(MutedStyle.Render(strings.Repeat("═", 60)))
	fmt.Println()

	// Health section
	PrintHealthStatuses(statuses)

	// Top services
	if len(services) > 0 {
		// Show top 10
		top := services
		if len(top) > 10 {
			top = top[:10]
		}
		PrintServices(top)
	}

	// Recent errors
	if len(recentLogs) > 0 {
		fmt.Println(TitleStyle.Render("🚨 Recent Errors"))
		fmt.Println()
		top := recentLogs
		if len(top) > 10 {
			top = top[:10]
		}
		for _, log := range top {
			ts := MutedStyle.Render(log.Timestamp.Format("15:04:05"))
			svc := ""
			if log.ServiceName != "" {
				svc = AccentStyle.Render("["+log.ServiceName+"]") + " "
			}
			body := log.Body
			if len(body) > 120 {
				body = body[:120] + "..."
			}
			fmt.Printf("  %s %s %s%s\n", ts, formatSeverity(log.SeverityText), svc, body)
		}
		fmt.Println()
	}
}

func formatSeverity(sev string) string {
	switch strings.ToUpper(sev) {
	case "ERROR", "FATAL", "CRITICAL":
		return severityError.Render(fmt.Sprintf("%-5s", sev))
	case "WARN", "WARNING":
		return severityWarn.Render(fmt.Sprintf("%-5s", sev))
	case "INFO":
		return severityInfo.Render(fmt.Sprintf("%-5s", sev))
	case "DEBUG", "TRACE":
		return severityDebug.Render(fmt.Sprintf("%-5s", sev))
	default:
		return MutedStyle.Render(fmt.Sprintf("%-5s", sev))
	}
}

func formatTraceDuration(ms float64) string {
	if ms < 1 {
		return MutedStyle.Render(fmt.Sprintf("%.0fµs", ms*1000))
	}
	if ms < 1000 {
		style := SuccessStyle
		if ms > 500 {
			style = WarningStyle
		}
		if ms > 2000 {
			style = ErrorStyle
		}
		return style.Render(fmt.Sprintf("%.0fms", ms))
	}
	return ErrorStyle.Render(fmt.Sprintf("%.1fs", ms/1000))
}

func truncateID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

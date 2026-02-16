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
	primary   = lipgloss.Color("#7C3AED") // purple
	success   = lipgloss.Color("#10B981") // green
	danger    = lipgloss.Color("#EF4444") // red
	warning   = lipgloss.Color("#F59E0B") // amber
	muted     = lipgloss.Color("#6B7280") // gray
	accent    = lipgloss.Color("#3B82F6") // blue

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

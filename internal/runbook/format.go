package runbook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))
	labelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	stepNumStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#3B82F6"))
	commandStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	manualStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true)
	sevP1Style    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
	sevP2Style    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F97316")).Bold(true)
	sevP3Style    = lipgloss.NewStyle().Foreground(lipgloss.Color("#EAB308"))
	sevP4Style    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	passedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981"))
	failedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))
)

func severityStyle(sev string) lipgloss.Style {
	switch strings.ToUpper(sev) {
	case "P1":
		return sevP1Style
	case "P2":
		return sevP2Style
	case "P3":
		return sevP3Style
	default:
		return sevP4Style
	}
}

// PrintList renders a list of runbooks to the terminal
func PrintList(w io.Writer, runbooks []*Runbook) {
	if len(runbooks) == 0 {
		fmt.Fprintln(w, "\n📚 No runbooks found. Run: argus runbook init")
		return
	}

	fmt.Fprintf(w, "\n📚 Runbooks (%d)\n\n", len(runbooks))

	for _, rb := range runbooks {
		sev := ""
		if rb.Severity != "" {
			sev = severityStyle(rb.Severity).Render(rb.Severity) + " "
		}
		cat := ""
		if rb.Category != "" {
			cat = labelStyle.Render("["+rb.Category+"]") + " "
		}

		fmt.Fprintf(w, "  %s%s%s\n", sev, cat, titleStyle.Render(rb.Name))

		if rb.Description != "" {
			fmt.Fprintf(w, "     %s\n", labelStyle.Render(rb.Description))
		}

		fmt.Fprintf(w, "     %s %s  %s %d steps",
			labelStyle.Render("ID:"), rb.ID,
			labelStyle.Render("Steps:"), len(rb.Steps))

		if len(rb.Tags) > 0 {
			fmt.Fprintf(w, "  %s %s", labelStyle.Render("Tags:"), strings.Join(rb.Tags, ", "))
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w)
	}
}

// PrintShow renders a detailed runbook view
func PrintShow(w io.Writer, rb *Runbook) {
	fmt.Fprintln(w)

	// Header
	sev := ""
	if rb.Severity != "" {
		sev = " " + severityStyle(rb.Severity).Render("["+rb.Severity+"]")
	}
	fmt.Fprintf(w, "📖 %s%s\n", titleStyle.Render(rb.Name), sev)

	if rb.Description != "" {
		fmt.Fprintf(w, "   %s\n", rb.Description)
	}
	fmt.Fprintln(w)

	// Metadata
	if rb.Category != "" {
		fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("Category:"), rb.Category)
	}
	if len(rb.Tags) > 0 {
		fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("Tags:"), strings.Join(rb.Tags, ", "))
	}
	if rb.Author != "" {
		fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("Author:"), rb.Author)
	}
	if rb.OnFailure != "" {
		fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("On Failure:"), rb.OnFailure)
	}
	fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("ID:"), rb.ID)
	fmt.Fprintln(w)

	// Steps
	fmt.Fprintf(w, "   %s\n\n", titleStyle.Render(fmt.Sprintf("Steps (%d)", len(rb.Steps))))

	for i, step := range rb.Steps {
		num := stepNumStyle.Render(fmt.Sprintf("   %d.", i+1))
		manual := ""
		if step.Manual {
			manual = " " + manualStyle.Render("[MANUAL]")
		}
		fmt.Fprintf(w, "%s %s%s\n", num, step.Name, manual)

		if step.Description != "" {
			fmt.Fprintf(w, "      %s\n", labelStyle.Render(step.Description))
		}
		if step.Command != "" {
			fmt.Fprintf(w, "      $ %s\n", commandStyle.Render(step.Command))
		}
		if step.Check != "" {
			fmt.Fprintf(w, "      %s %s\n", labelStyle.Render("Check:"), commandStyle.Render(step.Check))
		}
		if step.Rollback != "" {
			fmt.Fprintf(w, "      %s %s\n", labelStyle.Render("Rollback:"), step.Rollback)
		}
		if step.Timeout != "" {
			fmt.Fprintf(w, "      %s %s\n", labelStyle.Render("Timeout:"), step.Timeout)
		}
		if step.Notes != "" {
			fmt.Fprintf(w, "      💡 %s\n", step.Notes)
		}
		fmt.Fprintln(w)
	}
}

// PrintRunLog renders a run log
func PrintRunLog(w io.Writer, log *RunLog) {
	statusIcon := "✅"
	if log.Status == "failed" {
		statusIcon = "❌"
	} else if log.Status == "aborted" {
		statusIcon = "⏹️"
	} else if log.Status == "running" {
		statusIcon = "🔄"
	}

	fmt.Fprintf(w, "\n%s Run: %s\n", statusIcon, titleStyle.Render(log.RunbookName))
	fmt.Fprintf(w, "   %s %s\n", labelStyle.Render("Started:"), log.StartedAt.Format("2006-01-02 15:04:05"))
	if !log.CompletedAt.IsZero() {
		fmt.Fprintf(w, "   %s %s (%s)\n", labelStyle.Render("Completed:"),
			log.CompletedAt.Format("15:04:05"),
			log.CompletedAt.Sub(log.StartedAt).Round(1e9).String())
	}
	fmt.Fprintln(w)

	for _, sr := range log.StepResults {
		icon := "⬜"
		style := labelStyle
		switch sr.Status {
		case "passed":
			icon = "✅"
			style = passedStyle
		case "failed":
			icon = "❌"
			style = failedStyle
		case "skipped":
			icon = "⏭️"
		}
		fmt.Fprintf(w, "   %s %s %s\n", icon, sr.StepName, style.Render("["+sr.Status+"]"))
		if sr.Error != "" {
			fmt.Fprintf(w, "      %s\n", failedStyle.Render(sr.Error))
		}
	}
	fmt.Fprintln(w)
}

// FormatJSON returns JSON representation
func FormatJSON(v interface{}) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

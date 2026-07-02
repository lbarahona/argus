package deploy

import (
	"fmt"
	"io"
	"math"
	"strings"
)

// RenderTerminal displays the deployment analysis in the terminal.
func (r *Result) RenderTerminal(w io.Writer) {
	fmt.Fprintf(w, "\n🚀 ARGUS DEPLOY TRACKER\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "  Instance: %s  |  Window: %dm  |  Sensitivity: %s\n", r.Instance, r.Duration, r.Sensitivity)
	fmt.Fprintf(w, "  Generated: %s\n\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))

	if r.Truncated && r.DataCaveat != "" {
		fmt.Fprintf(w, "  ⚠️  %s\n\n", r.DataCaveat)
	}

	// Overall verdict
	verdictIcon := impactIcon(r.Summary.OverallImpact)
	fmt.Fprintf(w, "  %s Overall Impact: %s (score: %s)\n",
		verdictIcon, strings.ToUpper(r.Summary.OverallImpact), scoreColor(r.Summary.OverallScore))
	fmt.Fprintf(w, "  📊 %d changes detected across %d services (%d affected)\n",
		r.Summary.TotalChanges, len(r.Services), r.Summary.ServicesAffected)

	if r.Summary.MostImpacted != "" {
		fmt.Fprintf(w, "  🎯 Most impacted: %s\n", r.Summary.MostImpacted)
	}

	fmt.Fprintf(w, "     🟢 %d positive  🔴 %d negative  ⚪ %d neutral  🟡 %d mixed\n\n",
		r.Summary.Positive, r.Summary.Negative, r.Summary.Neutral, r.Summary.Mixed)

	// Timeline of change points
	if len(r.ChangePoints) > 0 {
		fmt.Fprintf(w, "  ⏱️  CHANGE TIMELINE\n")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 75))
		for _, cp := range r.ChangePoints {
			icon := changeIcon(cp.Type)
			conf := fmt.Sprintf("%.0f%%", cp.Confidence*100)
			fmt.Fprintf(w, "  %s %s  %-25s  %s  [%s]\n",
				cp.DetectedAt.Format("15:04"),
				icon,
				truncate(cp.Service, 25),
				cp.Description,
				conf)
		}
		fmt.Fprintln(w)
	}

	// Service impact details
	if len(r.Services) > 0 {
		fmt.Fprintf(w, "  📋 SERVICE IMPACT\n")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 75))
		fmt.Fprintf(w, "  %-28s %8s %12s %12s %10s\n", "SERVICE", "IMPACT", "ERRORS", "P99 (ms)", "SCORE")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 75))

		for _, svc := range r.Services {
			icon := impactIcon(svc.Impact)
			errStr := fmt.Sprintf("%d → %d", svc.ErrorsBefore, svc.ErrorsAfter)
			p99Str := "—"
			if svc.P99Before > 0 || svc.P99After > 0 {
				p99Str = fmt.Sprintf("%.0f → %.0f", svc.P99Before, svc.P99After)
			}
			fmt.Fprintf(w, "  %s %-25s %8s %12s %12s %10s\n",
				icon,
				truncate(svc.Name, 25),
				svc.Impact,
				errStr,
				p99Str,
				scoreColor(svc.ImpactScore))

			// Show individual changes indented
			for _, c := range svc.Changes {
				cIcon := changeIcon(c.Type)
				fmt.Fprintf(w, "    %s %s\n", cIcon, c.Description)
			}
		}
		fmt.Fprintln(w)
	}

	if len(r.ChangePoints) == 0 {
		fmt.Fprintf(w, "  ✅ No significant behavioral changes detected.\n")
		fmt.Fprintf(w, "  Either no deployments occurred, or changes were within normal bounds.\n\n")
	}

	// AI Summary
	if r.AISummary != "" {
		fmt.Fprintf(w, "  🤖 AI ANALYSIS\n")
		fmt.Fprintf(w, "  %s\n", strings.Repeat("─", 75))
		for _, line := range strings.Split(r.AISummary, "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// RenderMarkdown outputs the report in markdown format.
func (r *Result) RenderMarkdown(w io.Writer) {
	fmt.Fprintf(w, "# 🚀 Argus Deploy Tracker Report\n\n")
	fmt.Fprintf(w, "- **Instance:** %s\n", r.Instance)
	fmt.Fprintf(w, "- **Window:** %d minutes\n", r.Duration)
	fmt.Fprintf(w, "- **Sensitivity:** %s\n", r.Sensitivity)
	fmt.Fprintf(w, "- **Generated:** %s\n\n", r.GeneratedAt.Format("2006-01-02 15:04:05"))

	if r.Truncated && r.DataCaveat != "" {
		fmt.Fprintf(w, "> ⚠️ **%s**\n\n", r.DataCaveat)
	}

	// Summary
	fmt.Fprintf(w, "## Summary\n\n")
	fmt.Fprintf(w, "| Metric | Value |\n")
	fmt.Fprintf(w, "|--------|-------|\n")
	fmt.Fprintf(w, "| Overall Impact | **%s** (%.1f) |\n", strings.ToUpper(r.Summary.OverallImpact), r.Summary.OverallScore)
	fmt.Fprintf(w, "| Changes Detected | %d |\n", r.Summary.TotalChanges)
	fmt.Fprintf(w, "| Services Affected | %d |\n", r.Summary.ServicesAffected)
	fmt.Fprintf(w, "| Most Impacted | %s |\n", r.Summary.MostImpacted)
	fmt.Fprintf(w, "| Positive / Negative / Neutral / Mixed | %d / %d / %d / %d |\n\n",
		r.Summary.Positive, r.Summary.Negative, r.Summary.Neutral, r.Summary.Mixed)

	// Timeline
	if len(r.ChangePoints) > 0 {
		fmt.Fprintf(w, "## Change Timeline\n\n")
		fmt.Fprintf(w, "| Time | Service | Type | Description | Confidence |\n")
		fmt.Fprintf(w, "|------|---------|------|-------------|------------|\n")
		for _, cp := range r.ChangePoints {
			fmt.Fprintf(w, "| %s | %s | %s | %s | %.0f%% |\n",
				cp.DetectedAt.Format("15:04"), cp.Service, cp.Type, cp.Description, cp.Confidence*100)
		}
		fmt.Fprintln(w)
	}

	// Service Impact
	if len(r.Services) > 0 {
		fmt.Fprintf(w, "## Service Impact\n\n")
		fmt.Fprintf(w, "| Service | Impact | Score | Errors | P99 (ms) |\n")
		fmt.Fprintf(w, "|---------|--------|-------|--------|----------|\n")
		for _, svc := range r.Services {
			p99Str := "—"
			if svc.P99Before > 0 || svc.P99After > 0 {
				p99Str = fmt.Sprintf("%.0f → %.0f", svc.P99Before, svc.P99After)
			}
			fmt.Fprintf(w, "| %s | %s %s | %.1f | %d → %d | %s |\n",
				svc.Name, impactIcon(svc.Impact), svc.Impact, svc.ImpactScore,
				svc.ErrorsBefore, svc.ErrorsAfter, p99Str)
		}
		fmt.Fprintln(w)

		// Detailed changes per service
		fmt.Fprintf(w, "### Detailed Changes\n\n")
		for _, svc := range r.Services {
			fmt.Fprintf(w, "**%s** (%s, score: %.1f)\n\n", svc.Name, svc.Impact, svc.ImpactScore)
			for _, c := range svc.Changes {
				fmt.Fprintf(w, "- %s %s\n", changeIcon(c.Type), c.Description)
			}
			fmt.Fprintln(w)
		}
	}

	// AI Summary
	if r.AISummary != "" {
		fmt.Fprintf(w, "## AI Analysis\n\n%s\n", r.AISummary)
	}
}

func impactIcon(impact string) string {
	switch impact {
	case ImpactPositive:
		return "🟢"
	case ImpactNegative:
		return "🔴"
	case ImpactMixed:
		return "🟡"
	default:
		return "⚪"
	}
}

func changeIcon(changeType string) string {
	switch changeType {
	case ChangeErrorSpike:
		return "📈"
	case ChangeErrorDrop:
		return "📉"
	case ChangeLatencySpike:
		return "🐌"
	case ChangeLatencyDrop:
		return "⚡"
	case ChangeTrafficSurge:
		return "🌊"
	case ChangeTrafficDrop:
		return "📭"
	case ChangeNewErrors:
		return "🆕"
	case ChangeServiceAppear:
		return "🌟"
	default:
		return "❓"
	}
}

func scoreColor(score float64) string {
	absScore := math.Abs(score)
	sign := "+"
	if score < 0 {
		sign = ""
	}
	if absScore >= 50 {
		return fmt.Sprintf("%s%.1f ‼️", sign, score)
	} else if absScore >= 20 {
		return fmt.Sprintf("%s%.1f ⚠️", sign, score)
	}
	return fmt.Sprintf("%s%.1f", sign, score)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

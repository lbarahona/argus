package correlate

import (
	"fmt"
	"strings"
	"time"
)

// RenderStack formats stack correlation results for terminal output.
func RenderStack(result *StackResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Cross-tool correlation, last %s\n\n", result.Duration)
	if len(result.Groups) == 0 {
		b.WriteString("No correlated alert groups found.\n")
		return b.String()
	}

	for i, group := range result.Groups {
		fmt.Fprintf(&b, "%d. %s\n", i+1, renderGroupHeadline(group))
		fmt.Fprintf(&b, "   Sources: %s\n", strings.Join(group.Sources, ", "))
		fmt.Fprintf(&b, "   Alerts: %s\n", strings.Join(group.AlertNames, ", "))
		for _, sig := range group.Signals {
			line := fmt.Sprintf("   - [%s] %s", sig.Source, sig.Name)
			meta := make([]string, 0, 3)
			if sig.State != "" {
				meta = append(meta, sig.State)
			}
			if !sig.StartsAt.IsZero() {
				meta = append(meta, sig.StartsAt.Format(time.RFC3339))
			}
			if sig.Summary != "" {
				meta = append(meta, truncate(sig.Summary, 100))
			}
			if len(meta) > 0 {
				line += " (" + strings.Join(meta, ", ") + ")"
			}
			b.WriteString(line + "\n")
		}
		if len(group.Logs) > 0 {
			b.WriteString("   Loki samples:\n")
			for _, entry := range group.Logs {
				fmt.Fprintf(&b, "   - %s %s\n", entry.Timestamp.Format(time.RFC3339), truncate(strings.TrimSpace(entry.Line), 120))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderGroupHeadline(group CorrelatedGroup) string {
	service := group.Service
	if service == "" {
		service = "unknown-service"
	}
	return fmt.Sprintf("%s (%s, score %d)", service, group.Severity, group.Score)
}

func truncate(v string, limit int) string {
	if len(v) <= limit {
		return v
	}
	if limit <= 3 {
		return v[:limit]
	}
	return v[:limit-3] + "..."
}

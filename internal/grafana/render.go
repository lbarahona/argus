package grafana

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lbarahona/argus/internal/textutil"
)

// FormatDashboards renders a list of dashboards grouped by folder.
func FormatDashboards(dashboards []Dashboard) string {
	if len(dashboards) == 0 {
		return "No dashboards found."
	}

	// Group by folder
	grouped := make(map[string][]Dashboard)
	for _, d := range dashboards {
		folder := d.FolderTitle
		if folder == "" {
			folder = "General"
		}
		grouped[folder] = append(grouped[folder], d)
	}

	// Sort folder names
	folders := make([]string, 0, len(grouped))
	for f := range grouped {
		folders = append(folders, f)
	}
	sort.Strings(folders)

	var b strings.Builder
	fmt.Fprintf(&b, "📊 Dashboards (%d)\n", len(dashboards))
	b.WriteString(strings.Repeat("─", 60) + "\n")

	for _, folder := range folders {
		dashes := grouped[folder]
		sort.Slice(dashes, func(i, j int) bool {
			return dashes[i].Title < dashes[j].Title
		})

		fmt.Fprintf(&b, "\n📁 %s (%d)\n", folder, len(dashes))
		for _, d := range dashes {
			star := "  "
			if d.IsStarred {
				star = "⭐"
			}
			tags := ""
			if len(d.Tags) > 0 {
				tags = " [" + strings.Join(d.Tags, ", ") + "]"
			}
			fmt.Fprintf(&b, "  %s %s%s\n", star, d.Title, tags)
			fmt.Fprintf(&b, "     uid: %s\n", d.UID)
		}
	}

	return b.String()
}

// FormatDashboardDetail renders detailed dashboard info.
func FormatDashboardDetail(dm *DashboardMeta) string {
	var b strings.Builder

	title := "Untitled"
	if t, ok := dm.Dashboard["title"].(string); ok {
		title = t
	}

	fmt.Fprintf(&b, "📊 %s\n", title)
	b.WriteString(strings.Repeat("─", 60) + "\n")

	if desc, ok := dm.Dashboard["description"].(string); ok && desc != "" {
		fmt.Fprintf(&b, "Description: %s\n", desc)
	}

	fmt.Fprintf(&b, "Slug:    %s\n", dm.Meta.Slug)
	fmt.Fprintf(&b, "Version: %d\n", dm.Meta.Version)
	fmt.Fprintf(&b, "Created: %s by %s\n", dm.Meta.Created.Format("2006-01-02 15:04"), dm.Meta.CreatedBy)
	fmt.Fprintf(&b, "Updated: %s by %s\n", dm.Meta.Updated.Format("2006-01-02 15:04"), dm.Meta.UpdatedBy)

	if dm.Meta.FolderTitle != "" {
		fmt.Fprintf(&b, "Folder:  %s\n", dm.Meta.FolderTitle)
	}

	// Count panels
	if panels, ok := dm.Dashboard["panels"].([]interface{}); ok {
		panelTypes := make(map[string]int)
		for _, p := range panels {
			if pm, ok := p.(map[string]interface{}); ok {
				ptype := "unknown"
				if t, ok := pm["type"].(string); ok {
					ptype = t
				}
				panelTypes[ptype]++
			}
		}
		fmt.Fprintf(&b, "\nPanels: %d\n", len(panels))
		for pt, count := range panelTypes {
			fmt.Fprintf(&b, "  • %s: %d\n", pt, count)
		}
	}

	// Tags
	if tags, ok := dm.Dashboard["tags"].([]interface{}); ok && len(tags) > 0 {
		tagStrs := make([]string, 0, len(tags))
		for _, t := range tags {
			if s, ok := t.(string); ok {
				tagStrs = append(tagStrs, s)
			}
		}
		if len(tagStrs) > 0 {
			fmt.Fprintf(&b, "Tags:    %s\n", strings.Join(tagStrs, ", "))
		}
	}

	return b.String()
}

// FormatDatasources renders a list of data sources.
func FormatDatasources(datasources []Datasource) string {
	if len(datasources) == 0 {
		return "No data sources configured."
	}

	// Sort by name
	sort.Slice(datasources, func(i, j int) bool {
		return datasources[i].Name < datasources[j].Name
	})

	var b strings.Builder
	fmt.Fprintf(&b, "🔌 Data Sources (%d)\n", len(datasources))
	b.WriteString(strings.Repeat("─", 60) + "\n\n")

	// Group by type
	grouped := make(map[string][]Datasource)
	for _, ds := range datasources {
		grouped[ds.Type] = append(grouped[ds.Type], ds)
	}

	types := make([]string, 0, len(grouped))
	for t := range grouped {
		types = append(types, t)
	}
	sort.Strings(types)

	for _, t := range types {
		sources := grouped[t]
		icon := dsTypeIcon(t)
		fmt.Fprintf(&b, "%s %s (%d)\n", icon, t, len(sources))
		for _, ds := range sources {
			def := ""
			if ds.IsDefault {
				def = " (default)"
			}
			ro := ""
			if ds.ReadOnly {
				ro = " [read-only]"
			}
			fmt.Fprintf(&b, "  • %s%s%s\n", ds.Name, def, ro)
			fmt.Fprintf(&b, "    url: %s  access: %s\n", ds.URL, ds.Access)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// FormatAlertRules renders alert rules grouped by rule group.
func FormatAlertRules(rules []AlertRule) string {
	if len(rules) == 0 {
		return "No alert rules configured."
	}

	// Group by RuleGroup
	grouped := make(map[string][]AlertRule)
	for _, r := range rules {
		grouped[r.RuleGroup] = append(grouped[r.RuleGroup], r)
	}

	groups := make([]string, 0, len(grouped))
	for g := range grouped {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	var b strings.Builder
	fmt.Fprintf(&b, "🔔 Alert Rules (%d)\n", len(rules))
	b.WriteString(strings.Repeat("─", 60) + "\n")

	for _, group := range groups {
		rr := grouped[group]
		sort.Slice(rr, func(i, j int) bool {
			return rr[i].Title < rr[j].Title
		})

		fmt.Fprintf(&b, "\n📋 %s (%d rules)\n", group, len(rr))
		for _, r := range rr {
			dur := r.For
			if dur == "" {
				dur = "0s"
			}
			fmt.Fprintf(&b, "  • %s\n", r.Title)
			fmt.Fprintf(&b, "    for: %s  noData: %s  execErr: %s\n", dur, r.NoDataState, r.ExecErrState)
			if summary, ok := r.Annotations["summary"]; ok {
				fmt.Fprintf(&b, "    summary: %s\n", truncate(summary, 80))
			}
			if len(r.Labels) > 0 {
				fmt.Fprintf(&b, "    labels: %s\n", formatLabels(r.Labels))
			}
		}
	}

	return b.String()
}

// FormatAlertInstances renders firing alert instances.
func FormatAlertInstances(instances []GrafanaAlertInstance) string {
	if len(instances) == 0 {
		return "✅ No firing alerts."
	}

	// Sort: active first, then by startsAt descending
	sort.Slice(instances, func(i, j int) bool {
		if instances[i].Status.State != instances[j].Status.State {
			return instances[i].Status.State == "active"
		}
		return instances[i].StartsAt.After(instances[j].StartsAt)
	})

	var b strings.Builder
	active := 0
	for _, inst := range instances {
		if inst.Status.State == "active" {
			active++
		}
	}
	fmt.Fprintf(&b, "🚨 Alert Instances (%d total, %d active)\n", len(instances), active)
	b.WriteString(strings.Repeat("─", 60) + "\n\n")

	for _, inst := range instances {
		icon := stateIcon(inst.Status.State)
		name := inst.Labels["alertname"]
		if name == "" {
			name = "unnamed"
		}
		sev := inst.Labels["severity"]
		if sev == "" {
			sev = "none"
		}

		fmt.Fprintf(&b, "%s %s [%s] (%s)\n", icon, name, sev, inst.Status.State)
		if since := time.Since(inst.StartsAt); inst.StartsAt.After(time.Time{}) {
			fmt.Fprintf(&b, "   firing for %s\n", formatDuration(since))
		}
		if summary, ok := inst.Annotations["summary"]; ok {
			fmt.Fprintf(&b, "   %s\n", truncate(summary, 80))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// FormatFolders renders a list of folders.
func FormatFolders(folders []Folder) string {
	if len(folders) == 0 {
		return "No folders found."
	}

	sort.Slice(folders, func(i, j int) bool {
		return folders[i].Title < folders[j].Title
	})

	var b strings.Builder
	fmt.Fprintf(&b, "📁 Folders (%d)\n", len(folders))
	b.WriteString(strings.Repeat("─", 60) + "\n\n")

	for _, f := range folders {
		fmt.Fprintf(&b, "  • %s\n", f.Title)
		fmt.Fprintf(&b, "    uid: %s  url: %s\n", f.UID, f.URL)
	}

	return b.String()
}

// FormatSummary renders a one-line overview.
func FormatSummary(s *Summary) string {
	var b strings.Builder
	b.WriteString("📊 Grafana Summary\n")
	b.WriteString(strings.Repeat("─", 60) + "\n\n")

	if s.OrgName != "" {
		fmt.Fprintf(&b, "Organization: %s\n", s.OrgName)
	}
	if s.Version != "" {
		fmt.Fprintf(&b, "Version:      %s\n", s.Version)
	}
	fmt.Fprintf(&b, "Dashboards:   %d\n", s.Dashboards)
	fmt.Fprintf(&b, "Folders:      %d\n", s.Folders)
	fmt.Fprintf(&b, "Data Sources: %d\n", s.Datasources)
	fmt.Fprintf(&b, "Alert Rules:  %d\n", s.AlertRules)

	if s.FiringAlerts > 0 {
		fmt.Fprintf(&b, "🔥 Firing:     %d\n", s.FiringAlerts)
	} else {
		b.WriteString("✅ No firing alerts\n")
	}

	return b.String()
}

// FormatStatus renders health + org info.
func FormatStatus(health *HealthResponse, org *OrgInfo) string {
	var b strings.Builder
	b.WriteString("🏥 Grafana Status\n")
	b.WriteString(strings.Repeat("─", 60) + "\n\n")

	if health != nil {
		fmt.Fprintf(&b, "Version:  %s\n", health.Version)
		fmt.Fprintf(&b, "Commit:   %s\n", health.Commit)
		fmt.Fprintf(&b, "Database: %s\n", health.Database)
	}
	if org != nil {
		fmt.Fprintf(&b, "Org:      %s (id: %d)\n", org.Name, org.ID)
	}

	return b.String()
}

// FormatJSON renders any value as indented JSON.
func FormatJSON(v interface{}) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(data)
}

// --- helpers ---

func stateIcon(state string) string {
	switch state {
	case "active":
		return "🔴"
	case "suppressed":
		return "🟡"
	default:
		return "⚪"
	}
}

func dsTypeIcon(dsType string) string {
	switch dsType {
	case "prometheus":
		return "🔥"
	case "loki":
		return "📜"
	case "elasticsearch", "opensearch":
		return "🔍"
	case "mysql", "postgres", "mssql":
		return "🗄️"
	case "influxdb":
		return "📈"
	case "graphite":
		return "📉"
	case "cloudwatch":
		return "☁️"
	case "tempo", "jaeger", "zipkin":
		return "🔗"
	default:
		return "📦"
	}
}

func formatLabels(labels map[string]string) string {
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

func formatDuration(d time.Duration) string {
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
	h := int(d.Hours()) % 24
	if h > 0 {
		return fmt.Sprintf("%dd%dh", days, h)
	}
	return fmt.Sprintf("%dd", days)
}

func truncate(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	// Reserve room for the "..." suffix so the total rendered length still
	// matches maxLen; textutil.Truncate handles the rune-safe slicing.
	return textutil.Truncate(s, maxLen-3)
}

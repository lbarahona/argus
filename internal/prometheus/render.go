package prometheus

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
)

// FormatRules renders rule groups in a human-readable format.
func FormatRules(data *RulesData, ruleType string) string {
	if data == nil || len(data.Groups) == 0 {
		return "No rule groups found."
	}

	var sb strings.Builder
	totalRules := 0
	for _, g := range data.Groups {
		totalRules += len(g.Rules)
	}

	sb.WriteString(fmt.Sprintf("%s%sRule Groups: %d | Rules: %d%s\n\n",
		colorBold, colorCyan, len(data.Groups), totalRules, colorReset))

	for _, g := range data.Groups {
		filteredRules := filterRules(g.Rules, ruleType)
		if len(filteredRules) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("%s%s%s%s", colorBold, colorCyan, g.Name, colorReset))
		if g.File != "" {
			sb.WriteString(fmt.Sprintf(" %s(%s)%s", colorGray, g.File, colorReset))
		}
		sb.WriteString(fmt.Sprintf(" %s[%.0fs interval]%s\n", colorGray, g.Interval, colorReset))

		for _, r := range filteredRules {
			icon := "📊"
			if r.Type == "alerting" {
				icon = stateIcon(r.State)
			}
			sb.WriteString(fmt.Sprintf("  %s %s%s%s", icon, colorBold, r.Name, colorReset))

			if r.Type == "alerting" && r.State != "" && r.State != "inactive" {
				sb.WriteString(fmt.Sprintf(" %s", stateLabel(r.State)))
			}
			if r.Health != "" && r.Health != "ok" {
				sb.WriteString(fmt.Sprintf(" %s⚠ %s%s", colorYellow, r.Health, colorReset))
			}
			sb.WriteString("\n")

			// Show query (truncated)
			query := r.Query
			if len(query) > 120 {
				query = query[:117] + "..."
			}
			sb.WriteString(fmt.Sprintf("    %squery:%s %s\n", colorGray, colorReset, query))

			// Show duration for alerting rules
			if r.Type == "alerting" && r.Duration > 0 {
				sb.WriteString(fmt.Sprintf("    %sfor:%s %s\n", colorGray, colorReset, formatDuration(time.Duration(r.Duration)*time.Second)))
			}

			// Show labels
			if len(r.Labels) > 0 {
				sb.WriteString(fmt.Sprintf("    %slabels:%s %s\n", colorGray, colorReset, formatLabels(r.Labels)))
			}

			// Show firing alerts count
			if len(r.Alerts) > 0 {
				firing := 0
				pending := 0
				for _, a := range r.Alerts {
					switch a.State {
					case "firing":
						firing++
					case "pending":
						pending++
					}
				}
				parts := []string{}
				if firing > 0 {
					parts = append(parts, fmt.Sprintf("%s%d firing%s", colorRed, firing, colorReset))
				}
				if pending > 0 {
					parts = append(parts, fmt.Sprintf("%s%d pending%s", colorYellow, pending, colorReset))
				}
				sb.WriteString(fmt.Sprintf("    %salerts:%s %s\n", colorGray, colorReset, strings.Join(parts, ", ")))
			}

			if r.LastError != "" {
				sb.WriteString(fmt.Sprintf("    %s%serror: %s%s\n", colorRed, colorBold, r.LastError, colorReset))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatTargets renders targets in a human-readable format.
func FormatTargets(data *TargetsData) string {
	if data == nil || len(data.ActiveTargets) == 0 {
		return "No active targets found."
	}

	var sb strings.Builder

	// Group by scrape pool
	pools := make(map[string][]Target)
	for _, t := range data.ActiveTargets {
		pools[t.ScrapePool] = append(pools[t.ScrapePool], t)
	}

	// Sort pool names
	poolNames := make([]string, 0, len(pools))
	for name := range pools {
		poolNames = append(poolNames, name)
	}
	sort.Strings(poolNames)

	healthy := 0
	for _, t := range data.ActiveTargets {
		if t.Health == "up" {
			healthy++
		}
	}

	sb.WriteString(fmt.Sprintf("%s%sTargets: %d total | %s%d up%s | %s%d down%s%s\n\n",
		colorBold, colorCyan,
		len(data.ActiveTargets),
		colorGreen, healthy, colorReset,
		colorRed, len(data.ActiveTargets)-healthy, colorReset,
		colorReset))

	for _, poolName := range poolNames {
		targets := pools[poolName]
		sb.WriteString(fmt.Sprintf("%s%s%s%s\n", colorBold, colorCyan, poolName, colorReset))

		for _, t := range targets {
			healthIcon := "🟢"
			healthColor := colorGreen
			if t.Health != "up" {
				healthIcon = "🔴"
				healthColor = colorRed
			}

			// Get instance label or scrape URL
			instance := t.Labels["instance"]
			if instance == "" {
				instance = t.ScrapeURL
			}

			sb.WriteString(fmt.Sprintf("  %s %s%s%s", healthIcon, healthColor, instance, colorReset))

			if !t.LastScrape.IsZero() {
				ago := time.Since(t.LastScrape)
				sb.WriteString(fmt.Sprintf(" %s(%s ago, %.1fms)%s",
					colorGray, formatDuration(ago), t.LastScrapeDur*1000, colorReset))
			}
			sb.WriteString("\n")

			if t.LastError != "" {
				sb.WriteString(fmt.Sprintf("    %s%serror: %s%s\n", colorRed, colorBold, t.LastError, colorReset))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatAlerts renders Prometheus alerts in a human-readable format.
func FormatAlerts(data *AlertsData) string {
	if data == nil || len(data.Alerts) == 0 {
		return "No active alerts."
	}

	var sb strings.Builder

	// Sort by state (firing first) then name
	alerts := make([]PromAlert, len(data.Alerts))
	copy(alerts, data.Alerts)
	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].State != alerts[j].State {
			return alerts[i].State == "firing"
		}
		return alerts[i].Labels["alertname"] < alerts[j].Labels["alertname"]
	})

	firing := 0
	pending := 0
	for _, a := range alerts {
		switch a.State {
		case "firing":
			firing++
		case "pending":
			pending++
		}
	}

	sb.WriteString(fmt.Sprintf("%s%sAlerts: %s%d firing%s", colorBold, colorCyan, colorRed, firing, colorReset))
	if pending > 0 {
		sb.WriteString(fmt.Sprintf(" | %s%d pending%s", colorYellow, pending, colorReset))
	}
	sb.WriteString(fmt.Sprintf("%s\n\n", colorReset))

	for _, a := range alerts {
		icon := "🔴"
		stateColor := colorRed
		if a.State == "pending" {
			icon = "🟡"
			stateColor = colorYellow
		}

		name := a.Labels["alertname"]
		sb.WriteString(fmt.Sprintf("  %s %s%s%s%s %s[%s]%s",
			icon, colorBold, stateColor, name, colorReset,
			colorGray, a.State, colorReset))

		if !a.ActiveAt.IsZero() {
			sb.WriteString(fmt.Sprintf(" %s(active %s)%s", colorGray, formatDuration(time.Since(a.ActiveAt)), colorReset))
		}
		sb.WriteString("\n")

		// Key labels (exclude alertname)
		keyLabels := make(map[string]string)
		for k, v := range a.Labels {
			if k != "alertname" {
				keyLabels[k] = v
			}
		}
		if len(keyLabels) > 0 {
			sb.WriteString(fmt.Sprintf("    %slabels:%s %s\n", colorGray, colorReset, formatLabels(keyLabels)))
		}

		// Summary annotation
		if summary := a.Annotations["summary"]; summary != "" {
			if len(summary) > 120 {
				summary = summary[:117] + "..."
			}
			sb.WriteString(fmt.Sprintf("    %ssummary:%s %s\n", colorGray, colorReset, summary))
		}
	}

	return sb.String()
}

// FormatQuery renders instant query results.
func FormatQuery(result *QueryResult) string {
	if result == nil {
		return "No result."
	}

	var sb strings.Builder

	switch result.ResultType {
	case "vector":
		var samples []VectorSample
		if err := json.Unmarshal(result.Result, &samples); err != nil {
			return fmt.Sprintf("Error decoding vector result: %v", err)
		}
		if len(samples) == 0 {
			return "Empty result."
		}
		sb.WriteString(fmt.Sprintf("%s%sVector: %d samples%s\n\n", colorBold, colorCyan, len(samples), colorReset))
		for _, s := range samples {
			labels := formatLabels(s.Metric)
			val := "?"
			if len(s.Value) > 1 {
				val = fmt.Sprintf("%v", s.Value[1])
			}
			sb.WriteString(fmt.Sprintf("  %s%s%s → %s%s%s\n", colorBold, labels, colorReset, colorGreen, val, colorReset))
		}

	case "matrix":
		var series []MatrixSeries
		if err := json.Unmarshal(result.Result, &series); err != nil {
			return fmt.Sprintf("Error decoding matrix result: %v", err)
		}
		if len(series) == 0 {
			return "Empty result."
		}
		sb.WriteString(fmt.Sprintf("%s%sMatrix: %d series%s\n\n", colorBold, colorCyan, len(series), colorReset))
		for _, s := range series {
			labels := formatLabels(s.Metric)
			sb.WriteString(fmt.Sprintf("  %s%s%s (%d samples)\n", colorBold, labels, colorReset, len(s.Values)))
			// Show last 5 samples
			start := 0
			if len(s.Values) > 5 {
				start = len(s.Values) - 5
				sb.WriteString(fmt.Sprintf("    %s... (%d earlier samples)%s\n", colorGray, start, colorReset))
			}
			for i := start; i < len(s.Values); i++ {
				v := s.Values[i]
				ts := "?"
				val := "?"
				if tsFloat, ok := v[0].(float64); ok {
					ts = time.Unix(int64(tsFloat), 0).Format("15:04:05")
				}
				if len(v) > 1 {
					val = fmt.Sprintf("%v", v[1])
				}
				sb.WriteString(fmt.Sprintf("    %s%s%s  %s\n", colorGray, ts, colorReset, val))
			}
		}

	case "scalar":
		sb.WriteString(fmt.Sprintf("%s%sScalar:%s %s\n", colorBold, colorCyan, colorReset, string(result.Result)))

	case "string":
		sb.WriteString(fmt.Sprintf("%s%sString:%s %s\n", colorBold, colorCyan, colorReset, string(result.Result)))

	default:
		sb.WriteString(fmt.Sprintf("Unknown result type: %s\n%s\n", result.ResultType, string(result.Result)))
	}

	return sb.String()
}

// FormatSummary renders a quick overview.
func FormatSummary(s Summary) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s%sPrometheus Summary%s\n\n", colorBold, colorCyan, colorReset))

	sb.WriteString(fmt.Sprintf("  %sRules:%s %d groups, %d alert rules, %d recording rules\n",
		colorBold, colorReset, s.TotalRuleGroups, s.TotalAlertRules, s.TotalRecordRules))

	firingColor := colorGreen
	if s.FiringAlerts > 0 {
		firingColor = colorRed
	}
	pendingColor := colorGreen
	if s.PendingAlerts > 0 {
		pendingColor = colorYellow
	}
	sb.WriteString(fmt.Sprintf("  %sAlerts:%s %s%d firing%s, %s%d pending%s\n",
		colorBold, colorReset,
		firingColor, s.FiringAlerts, colorReset,
		pendingColor, s.PendingAlerts, colorReset))

	targetColor := colorGreen
	if s.UnhealthyTargets > 0 {
		targetColor = colorRed
	}
	sb.WriteString(fmt.Sprintf("  %sTargets:%s %d active (%s%d up%s, %s%d down%s)\n",
		colorBold, colorReset,
		s.ActiveTargets,
		colorGreen, s.HealthyTargets, colorReset,
		targetColor, s.UnhealthyTargets, colorReset))

	return sb.String()
}

// FormatStatus renders Prometheus runtime and build info.
func FormatStatus(runtime *RuntimeInfo, build *BuildInfo, healthy bool) string {
	var sb strings.Builder

	healthIcon := "🟢"
	healthText := "Healthy"
	if !healthy {
		healthIcon = "🔴"
		healthText = "Unhealthy"
	}

	sb.WriteString(fmt.Sprintf("%s%sPrometheus Status%s  %s %s\n\n", colorBold, colorCyan, colorReset, healthIcon, healthText))

	if build != nil {
		sb.WriteString(fmt.Sprintf("  %sVersion:%s  %s\n", colorBold, colorReset, build.Version))
		sb.WriteString(fmt.Sprintf("  %sBranch:%s   %s\n", colorBold, colorReset, build.Branch))
		sb.WriteString(fmt.Sprintf("  %sGo:%s       %s\n", colorBold, colorReset, build.GoVersion))
	}

	if runtime != nil {
		sb.WriteString(fmt.Sprintf("  %sRetention:%s %s\n", colorBold, colorReset, runtime.StorageRetention))
		sb.WriteString(fmt.Sprintf("  %sGoroutines:%s %d\n", colorBold, colorReset, runtime.GoroutineCount))
		sb.WriteString(fmt.Sprintf("  %sGOMAXPROCS:%s %d\n", colorBold, colorReset, runtime.GOMAXPROCS))
		if runtime.StartTime != "" {
			sb.WriteString(fmt.Sprintf("  %sStarted:%s  %s\n", colorBold, colorReset, runtime.StartTime))
		}
		if !runtime.ReloadConfigSuccess {
			sb.WriteString(fmt.Sprintf("  %s%s⚠ Last config reload failed%s\n", colorRed, colorBold, colorReset))
		}
	}

	return sb.String()
}

// FormatJSON renders any data as formatted JSON.
func FormatJSON(data interface{}) string {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error encoding JSON: %v", err)
	}
	return string(b)
}

// --- helpers ---

func filterRules(rules []Rule, ruleType string) []Rule {
	if ruleType == "" {
		return rules
	}
	var filtered []Rule
	for _, r := range rules {
		if r.Type == ruleType {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func stateIcon(state string) string {
	switch state {
	case "firing":
		return "🔴"
	case "pending":
		return "🟡"
	case "inactive":
		return "🟢"
	default:
		return "⚪"
	}
}

func stateLabel(state string) string {
	switch state {
	case "firing":
		return fmt.Sprintf("%s[FIRING]%s", colorRed, colorReset)
	case "pending":
		return fmt.Sprintf("%s[PENDING]%s", colorYellow, colorReset)
	default:
		return ""
	}
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd%dh", days, hours)
}

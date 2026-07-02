package postmortem

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/incident"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/signoz"
	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────

// Postmortem represents a structured post-incident review document.
type Postmortem struct {
	ID           string            `yaml:"id" json:"id"`
	IncidentID   string            `yaml:"incident_id" json:"incident_id"`
	Title        string            `yaml:"title" json:"title"`
	Severity     string            `yaml:"severity" json:"severity"`
	Status       string            `yaml:"status" json:"status"` // draft, review, published
	Services     []string          `yaml:"services,omitempty" json:"services,omitempty"`
	Commander    string            `yaml:"commander,omitempty" json:"commander,omitempty"`
	Authors      []string          `yaml:"authors,omitempty" json:"authors,omitempty"`
	Labels       map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	CreatedAt    time.Time         `yaml:"created_at" json:"created_at"`
	UpdatedAt    time.Time         `yaml:"updated_at" json:"updated_at"`
	IncidentTime IncidentWindow    `yaml:"incident_time" json:"incident_time"`
	Summary      string            `yaml:"summary" json:"summary"`
	Impact       Impact            `yaml:"impact" json:"impact"`
	Timeline     []TimelineEvent   `yaml:"timeline" json:"timeline"`
	RootCause    string            `yaml:"root_cause" json:"root_cause"`
	Contributing []string          `yaml:"contributing_factors,omitempty" json:"contributing_factors,omitempty"`
	Detection    Detection         `yaml:"detection" json:"detection"`
	Resolution   string            `yaml:"resolution" json:"resolution"`
	ActionItems  []ActionItem      `yaml:"action_items" json:"action_items"`
	Metrics      MetricsSummary    `yaml:"metrics_summary" json:"metrics_summary"`
	Lessons      []string          `yaml:"lessons_learned,omitempty" json:"lessons_learned,omitempty"`
}

// IncidentWindow represents the time window of the incident.
type IncidentWindow struct {
	Start    time.Time `yaml:"start" json:"start"`
	End      time.Time `yaml:"end" json:"end"`
	Duration string    `yaml:"duration" json:"duration"`
}

// Impact captures the blast radius and user-facing effects.
type Impact struct {
	Description      string   `yaml:"description" json:"description"`
	AffectedServices []string `yaml:"affected_services,omitempty" json:"affected_services,omitempty"`
	UserFacing       bool     `yaml:"user_facing" json:"user_facing"`
	ErrorCount       int      `yaml:"error_count" json:"error_count"`
	DurationMinutes  float64  `yaml:"duration_minutes" json:"duration_minutes"`
}

// TimelineEvent is a single event in the postmortem timeline.
type TimelineEvent struct {
	Timestamp   time.Time `yaml:"timestamp" json:"timestamp"`
	Type        string    `yaml:"type" json:"type"` // status_change, error_spike, detection, mitigation, resolution
	Description string    `yaml:"description" json:"description"`
	Source      string    `yaml:"source,omitempty" json:"source,omitempty"` // incident, logs, metrics, manual
}

// Detection describes how the incident was detected.
type Detection struct {
	Method        string  `yaml:"method" json:"method"` // alert, manual, customer_report, monitoring
	TimeToDetect  string  `yaml:"time_to_detect" json:"time_to_detect"`
	DetectMinutes float64 `yaml:"detect_minutes" json:"detect_minutes"`
}

// ActionItem is a follow-up task from the postmortem.
type ActionItem struct {
	ID       string `yaml:"id" json:"id"`
	Priority string `yaml:"priority" json:"priority"` // P0, P1, P2, P3
	Type     string `yaml:"type" json:"type"`         // prevent, detect, mitigate, process
	Title    string `yaml:"title" json:"title"`
	Owner    string `yaml:"owner,omitempty" json:"owner,omitempty"`
	Status   string `yaml:"status" json:"status"` // todo, in_progress, done
	DueDate  string `yaml:"due_date,omitempty" json:"due_date,omitempty"`
}

// MetricsSummary captures key metrics during the incident.
type MetricsSummary struct {
	PeakErrorRate  float64        `yaml:"peak_error_rate" json:"peak_error_rate"`
	TotalErrors    int            `yaml:"total_errors" json:"total_errors"`
	TotalCalls     int            `yaml:"total_calls" json:"total_calls"`
	ServiceMetrics []ServiceMetric `yaml:"service_metrics,omitempty" json:"service_metrics,omitempty"`
	TopErrors      []ErrorSummary `yaml:"top_errors,omitempty" json:"top_errors,omitempty"`
}

// ServiceMetric captures per-service metrics during the incident.
type ServiceMetric struct {
	Service    string  `yaml:"service" json:"service"`
	ErrorRate  float64 `yaml:"error_rate" json:"error_rate"`
	ErrorCount int     `yaml:"error_count" json:"error_count"`
	Calls      int     `yaml:"calls" json:"calls"`
}

// ErrorSummary captures top error messages during the incident.
type ErrorSummary struct {
	Message string `yaml:"message" json:"message"`
	Count   int    `yaml:"count" json:"count"`
	Service string `yaml:"service" json:"service"`
}

// PostmortemStore holds all postmortems.
type PostmortemStore struct {
	Postmortems []Postmortem `yaml:"postmortems" json:"postmortems"`
}

// Options for generating a postmortem.
type Options struct {
	IncidentID string
	UseAI      bool
	AIProvider ai.Provider
	Format     string // terminal, markdown, json
	Querier    signoz.SignozQuerier
}

// ──────────────────────────────────────────────
// Storage
// ──────────────────────────────────────────────

func storeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus")
}

func storePath() string {
	return filepath.Join(storeDir(), "postmortems.yaml")
}

// Load reads the postmortem store from disk.
func Load() (*PostmortemStore, error) {
	path := storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &PostmortemStore{}, nil
		}
		return nil, fmt.Errorf("reading postmortems: %w", err)
	}
	var store PostmortemStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parsing postmortems: %w", err)
	}
	return &store, nil
}

// Save writes the postmortem store to disk.
func (s *PostmortemStore) Save() error {
	dir := storeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating store dir: %w", err)
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling postmortems: %w", err)
	}
	return os.WriteFile(storePath(), data, 0o644)
}

// FindByID finds a postmortem by ID.
func (s *PostmortemStore) FindByID(id string) *Postmortem {
	for i := range s.Postmortems {
		if s.Postmortems[i].ID == id {
			return &s.Postmortems[i]
		}
	}
	return nil
}

// FindByIncidentID finds a postmortem by its incident ID.
func (s *PostmortemStore) FindByIncidentID(incidentID string) *Postmortem {
	for i := range s.Postmortems {
		if s.Postmortems[i].IncidentID == incidentID {
			return &s.Postmortems[i]
		}
	}
	return nil
}

// RecentPostmortems returns the N most recent postmortems.
func (s *PostmortemStore) RecentPostmortems(n int) []Postmortem {
	sorted := make([]Postmortem, len(s.Postmortems))
	copy(sorted, s.Postmortems)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})
	if n > 0 && n < len(sorted) {
		return sorted[:n]
	}
	return sorted
}

// ──────────────────────────────────────────────
// Generation
// ──────────────────────────────────────────────

// Generate creates a postmortem from an incident, enriching it with Signoz data and optional AI analysis.
func Generate(ctx context.Context, opts Options) (*Postmortem, error) {
	// Load incident
	incStore, err := incident.Load()
	if err != nil {
		return nil, fmt.Errorf("loading incidents: %w", err)
	}
	inc := incStore.FindByID(opts.IncidentID)
	if inc == nil {
		return nil, fmt.Errorf("incident %q not found", opts.IncidentID)
	}

	now := time.Now()
	pm := &Postmortem{
		ID:         fmt.Sprintf("pm-%d", now.UnixMilli()),
		IncidentID: inc.ID,
		Title:      fmt.Sprintf("Postmortem: %s", inc.Title),
		Severity:   inc.Severity,
		Status:     "draft",
		Services:   inc.Services,
		Commander:  inc.Commander,
		Authors:    []string{},
		Labels:     inc.Labels,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Build incident window
	pm.IncidentTime = buildIncidentWindow(inc)

	// Build timeline from incident events
	pm.Timeline = buildTimeline(inc)

	// Detection info
	pm.Detection = analyzeDetection(inc)

	// Basic summary
	pm.Summary = buildSummary(inc)
	pm.Resolution = buildResolution(inc)

	// Gather Signoz metrics if querier is available
	if opts.Querier != nil {
		enrichWithMetrics(ctx, pm, opts.Querier)
	}

	// AI analysis for root cause, action items, and lessons
	if opts.UseAI && opts.AIProvider != nil {
		enrichWithAI(pm, opts.AIProvider)
	} else {
		pm.RootCause = "Root cause analysis pending. Run with --ai flag for AI-powered analysis."
		pm.ActionItems = generateBasicActionItems(pm)
	}

	return pm, nil
}

func buildIncidentWindow(inc *incident.Incident) IncidentWindow {
	w := IncidentWindow{
		Start: inc.CreatedAt,
	}
	if inc.ResolvedAt != nil {
		w.End = *inc.ResolvedAt
		d := w.End.Sub(w.Start)
		w.Duration = formatDuration(d)
	} else {
		w.End = time.Now()
		d := w.End.Sub(w.Start)
		w.Duration = formatDuration(d) + " (ongoing)"
	}
	return w
}

func buildTimeline(inc *incident.Incident) []TimelineEvent {
	events := make([]TimelineEvent, 0, len(inc.Timeline)+1)

	// Add creation event
	events = append(events, TimelineEvent{
		Timestamp:   inc.CreatedAt,
		Type:        "detection",
		Description: fmt.Sprintf("Incident created: %s [%s]", inc.Title, inc.Severity),
		Source:      "incident",
	})

	// Add timeline entries
	for _, te := range inc.Timeline {
		evType := "status_change"
		if strings.Contains(strings.ToLower(te.Message), "mitigat") {
			evType = "mitigation"
		} else if strings.Contains(strings.ToLower(te.Message), "resolv") || te.Status == "resolved" {
			evType = "resolution"
		} else if strings.Contains(strings.ToLower(te.Message), "detect") || strings.Contains(strings.ToLower(te.Message), "alert") {
			evType = "detection"
		}

		events = append(events, TimelineEvent{
			Timestamp:   te.Timestamp,
			Type:        evType,
			Description: fmt.Sprintf("[%s] %s", te.Status, te.Message),
			Source:      "incident",
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	return events
}

func analyzeDetection(inc *incident.Incident) Detection {
	d := Detection{
		Method: "manual",
	}

	if len(inc.Timeline) > 0 {
		first := inc.Timeline[0]
		detectTime := first.Timestamp.Sub(inc.CreatedAt)
		if detectTime < 0 {
			detectTime = 0
		}
		d.DetectMinutes = detectTime.Minutes()
		d.TimeToDetect = formatDuration(detectTime)

		msg := strings.ToLower(first.Message)
		if strings.Contains(msg, "alert") {
			d.Method = "alert"
		} else if strings.Contains(msg, "monitor") {
			d.Method = "monitoring"
		} else if strings.Contains(msg, "customer") || strings.Contains(msg, "user") {
			d.Method = "customer_report"
		}
	}

	return d
}

func buildSummary(inc *incident.Incident) string {
	svcList := "unknown services"
	if len(inc.Services) > 0 {
		svcList = strings.Join(inc.Services, ", ")
	}

	status := "is ongoing"
	if inc.ResolvedAt != nil {
		dur := inc.ResolvedAt.Sub(inc.CreatedAt)
		status = fmt.Sprintf("lasted %s", formatDuration(dur))
	}

	sev := inc.Severity
	if len(sev) > 0 {
		sev = strings.ToUpper(sev[:1]) + sev[1:]
	}
	return fmt.Sprintf("%s severity incident affecting %s. The incident %s. %s",
		sev, svcList, status, defaultStr(inc.Description, ""))
}

func buildResolution(inc *incident.Incident) string {
	if inc.ResolvedAt == nil {
		return "Incident is still ongoing."
	}

	for i := len(inc.Timeline) - 1; i >= 0; i-- {
		if inc.Timeline[i].Status == "resolved" {
			return inc.Timeline[i].Message
		}
	}
	return "Incident was resolved."
}

// ──────────────────────────────────────────────
// Metrics enrichment
// ──────────────────────────────────────────────

func enrichWithMetrics(ctx context.Context, pm *Postmortem, q signoz.SignozQuerier) {
	// Calculate duration in minutes for querying
	dur := pm.IncidentTime.End.Sub(pm.IncidentTime.Start)
	durationMinutes := int(dur.Minutes()) + 20 // pad by 10 min each side
	if durationMinutes < 30 {
		durationMinutes = 30
	}

	// Get service list
	services, err := q.ListServices(ctx)
	if err != nil {
		return
	}

	var totalErrors int
	var totalCalls int
	var peakErrorRate float64

	for _, svc := range services {
		sm := ServiceMetric{
			Service:    svc.Name,
			ErrorRate:  svc.ErrorRate,
			ErrorCount: svc.NumErrors,
			Calls:      svc.NumCalls,
		}
		pm.Metrics.ServiceMetrics = append(pm.Metrics.ServiceMetrics, sm)
		totalErrors += svc.NumErrors
		totalCalls += svc.NumCalls
		if svc.ErrorRate > peakErrorRate {
			peakErrorRate = svc.ErrorRate
		}
	}

	pm.Metrics.TotalErrors = totalErrors
	pm.Metrics.TotalCalls = totalCalls
	pm.Metrics.PeakErrorRate = peakErrorRate

	pm.Impact.ErrorCount = totalErrors
	pm.Impact.DurationMinutes = dur.Minutes()
	if len(pm.Services) > 0 {
		pm.Impact.AffectedServices = pm.Services
	}

	// Get error logs for top errors from affected services
	if len(pm.Services) > 0 {
		for _, svcName := range pm.Services {
			result, err := q.QueryLogs(ctx, svcName, durationMinutes, 50, "error")
			if err != nil || result == nil || len(result.Logs) == 0 {
				continue
			}

			// Count error messages
			msgCounts := make(map[string]int)
			for _, l := range result.Logs {
				body := truncateString(l.Body, 120)
				if body != "" {
					msgCounts[body]++
				}
			}

			for msg, count := range msgCounts {
				pm.Metrics.TopErrors = append(pm.Metrics.TopErrors, ErrorSummary{
					Message: msg,
					Count:   count,
					Service: svcName,
				})
			}
		}

		sort.Slice(pm.Metrics.TopErrors, func(i, j int) bool {
			return pm.Metrics.TopErrors[i].Count > pm.Metrics.TopErrors[j].Count
		})
		if len(pm.Metrics.TopErrors) > 10 {
			pm.Metrics.TopErrors = pm.Metrics.TopErrors[:10]
		}
	}
}

// ──────────────────────────────────────────────
// AI enrichment
// ──────────────────────────────────────────────

func enrichWithAI(pm *Postmortem, provider ai.Provider) {
	analyzer := ai.NewFromProvider(provider)

	prompt := buildAIPrompt(pm)
	result, err := analyzer.AnalyzeSync(prompt)
	if err != nil {
		pm.RootCause = fmt.Sprintf("AI analysis failed: %v. Manual root cause analysis required.", err)
		pm.ActionItems = generateBasicActionItems(pm)
		return
	}

	parseAIResponse(pm, result)
}

func buildAIPrompt(pm *Postmortem) string {
	var sb strings.Builder

	sb.WriteString("You are an expert SRE conducting a blameless postmortem analysis. ")
	sb.WriteString("Based on the following incident data, provide:\n\n")
	sb.WriteString("1. ROOT CAUSE: A clear, technical root cause analysis\n")
	sb.WriteString("2. CONTRIBUTING FACTORS: List of contributing factors (one per line, prefixed with -)\n")
	sb.WriteString("3. ACTION ITEMS: Specific, actionable follow-ups with priority (P0-P3) and type (prevent/detect/mitigate/process)\n")
	sb.WriteString("   Format: [PRIORITY] [TYPE] Description\n")
	sb.WriteString("4. LESSONS LEARNED: Key takeaways (one per line, prefixed with -)\n")
	sb.WriteString("5. IMPACT DESCRIPTION: A concise description of user/business impact\n\n")

	sb.WriteString("Use exactly these section headers: ROOT CAUSE:, CONTRIBUTING FACTORS:, ACTION ITEMS:, LESSONS LEARNED:, IMPACT:\n\n")

	sb.WriteString("--- INCIDENT DATA ---\n")
	sb.WriteString(fmt.Sprintf("Title: %s\n", pm.Title))
	sb.WriteString(fmt.Sprintf("Severity: %s\n", pm.Severity))
	sb.WriteString(fmt.Sprintf("Services: %s\n", strings.Join(pm.Services, ", ")))
	sb.WriteString(fmt.Sprintf("Duration: %s\n", pm.IncidentTime.Duration))
	sb.WriteString(fmt.Sprintf("Detection: %s (TTD: %s)\n", pm.Detection.Method, pm.Detection.TimeToDetect))
	sb.WriteString(fmt.Sprintf("Resolution: %s\n\n", pm.Resolution))

	sb.WriteString("Timeline:\n")
	for _, ev := range pm.Timeline {
		sb.WriteString(fmt.Sprintf("  %s [%s] %s\n", ev.Timestamp.Format("15:04:05"), ev.Type, ev.Description))
	}

	if pm.Metrics.TotalErrors > 0 {
		sb.WriteString(fmt.Sprintf("\nMetrics:\n"))
		sb.WriteString(fmt.Sprintf("  Total errors: %d\n", pm.Metrics.TotalErrors))
		sb.WriteString(fmt.Sprintf("  Peak error rate: %.2f%%\n", pm.Metrics.PeakErrorRate))

		if len(pm.Metrics.TopErrors) > 0 {
			sb.WriteString("\nTop errors:\n")
			for _, e := range pm.Metrics.TopErrors {
				sb.WriteString(fmt.Sprintf("  [%s] (%dx) %s\n", e.Service, e.Count, e.Message))
			}
		}
	}

	return sb.String()
}

func parseAIResponse(pm *Postmortem, response string) {
	sections := map[string]string{}
	currentSection := ""
	var currentContent strings.Builder

	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)

		// Only match section headers at start of line (not inside list items)
		isSectionLine := !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "[")

		newSection := ""
		if isSectionLine {
			// Map of header prefixes to section keys
			headers := []struct {
				prefix  string
				section string
			}{
				{"ROOT CAUSE:", "root_cause"},
				{"CONTRIBUTING FACTORS:", "contributing"},
				{"ACTION ITEMS:", "actions"},
				{"LESSONS LEARNED:", "lessons"},
				{"IMPACT:", "impact"},
			}
			for _, h := range headers {
				if strings.HasPrefix(upper, h.prefix) {
					newSection = h.section
					// Strip the header prefix from the line (case-insensitive)
					line = strings.TrimSpace(line)
					if len(line) >= len(h.prefix) {
						line = strings.TrimSpace(line[len(h.prefix):])
					}
					break
				}
			}
		}

		if newSection != "" {
			if currentSection != "" {
				sections[currentSection] = strings.TrimSpace(currentContent.String())
			}
			currentSection = newSection
			currentContent.Reset()
			if t := strings.TrimSpace(line); t != "" {
				currentContent.WriteString(t)
				currentContent.WriteString("\n")
			}
			continue
		}

		if currentSection != "" {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}
	if currentSection != "" {
		sections[currentSection] = strings.TrimSpace(currentContent.String())
	}

	if rc, ok := sections["root_cause"]; ok {
		pm.RootCause = rc
	}

	if cf, ok := sections["contributing"]; ok {
		for _, line := range strings.Split(cf, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			if line != "" {
				pm.Contributing = append(pm.Contributing, line)
			}
		}
	}

	if actions, ok := sections["actions"]; ok {
		pm.ActionItems = parseActionItems(actions)
	}
	if len(pm.ActionItems) == 0 {
		pm.ActionItems = generateBasicActionItems(pm)
	}

	if ll, ok := sections["lessons"]; ok {
		for _, line := range strings.Split(ll, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			if line != "" {
				pm.Lessons = append(pm.Lessons, line)
			}
		}
	}

	if imp, ok := sections["impact"]; ok {
		pm.Impact.Description = imp
	}
}

func parseActionItems(text string) []ActionItem {
	var items []ActionItem
	idx := 0

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		if line == "" {
			continue
		}

		idx++
		item := ActionItem{
			ID:       fmt.Sprintf("ai-%d", idx),
			Priority: "P2",
			Type:     "prevent",
			Title:    line,
			Status:   "todo",
		}

		upper := strings.ToUpper(line)
		for _, p := range []string{"P0", "P1", "P2", "P3"} {
			if strings.Contains(upper, "["+p+"]") {
				item.Priority = p
				item.Title = strings.Replace(item.Title, "["+p+"]", "", 1)
				item.Title = strings.Replace(item.Title, "["+strings.ToLower(p)+"]", "", 1)
				break
			}
		}

		for _, t := range []string{"prevent", "detect", "mitigate", "process"} {
			tag := "[" + strings.ToUpper(t) + "]"
			if strings.Contains(upper, tag) {
				item.Type = t
				item.Title = strings.Replace(item.Title, tag, "", 1)
				item.Title = strings.Replace(item.Title, "["+t+"]", "", 1)
				break
			}
		}

		item.Title = strings.TrimSpace(item.Title)
		if item.Title != "" {
			items = append(items, item)
		}
	}
	return items
}

func generateBasicActionItems(pm *Postmortem) []ActionItem {
	var items []ActionItem
	idx := 0

	if pm.Detection.Method == "manual" {
		idx++
		items = append(items, ActionItem{
			ID:       fmt.Sprintf("ai-%d", idx),
			Priority: "P1",
			Type:     "detect",
			Title:    "Set up automated alerting for affected services to reduce detection time",
			Status:   "todo",
		})
	}

	if pm.Metrics.PeakErrorRate > 10 { // >10% peak error rate
		idx++
		items = append(items, ActionItem{
			ID:       fmt.Sprintf("ai-%d", idx),
			Priority: "P1",
			Type:     "detect",
			Title:    fmt.Sprintf("Add error rate threshold alerts (peak was %.1f%%)", pm.Metrics.PeakErrorRate),
			Status:   "todo",
		})
	}

	idx++
	items = append(items, ActionItem{
		ID:       fmt.Sprintf("ai-%d", idx),
		Priority: "P2",
		Type:     "process",
		Title:    "Document root cause and resolution steps in runbook",
		Status:   "todo",
	})

	return items
}

// ──────────────────────────────────────────────
// Rendering
// ──────────────────────────────────────────────

// RenderTerminal renders a postmortem to the terminal.
func RenderTerminal(pm *Postmortem) {
	title := fmt.Sprintf("📋 %s", pm.Title)
	fmt.Println(output.TitleStyle.Render(title))
	fmt.Println()

	// Meta
	fmt.Printf("  %s %s  │  %s %s  │  %s %s  │  %s %s\n",
		output.AccentStyle.Render("ID:"), pm.ID,
		output.AccentStyle.Render("Severity:"), incident.SeverityIcon(pm.Severity)+" "+pm.Severity,
		output.AccentStyle.Render("Status:"), pm.Status,
		output.AccentStyle.Render("Incident:"), pm.IncidentID,
	)
	fmt.Printf("  %s %s → %s (%s)\n",
		output.AccentStyle.Render("Window:"),
		pm.IncidentTime.Start.Format("2006-01-02 15:04"),
		pm.IncidentTime.End.Format("15:04"),
		pm.IncidentTime.Duration,
	)
	if len(pm.Services) > 0 {
		fmt.Printf("  %s %s\n", output.AccentStyle.Render("Services:"), strings.Join(pm.Services, ", "))
	}
	if pm.Commander != "" {
		fmt.Printf("  %s %s\n", output.AccentStyle.Render("Commander:"), pm.Commander)
	}
	fmt.Println()

	// Summary
	fmt.Println(output.TitleStyle.Render("📝 Summary"))
	fmt.Printf("  %s\n\n", pm.Summary)

	// Impact
	fmt.Println(output.TitleStyle.Render("💥 Impact"))
	if pm.Impact.Description != "" {
		fmt.Printf("  %s\n", pm.Impact.Description)
	}
	fmt.Printf("  Duration: %.0f minutes  │  Errors: %d  │  Calls: %d  │  Peak Error Rate: %.2f%%\n",
		pm.Impact.DurationMinutes, pm.Impact.ErrorCount, pm.Metrics.TotalCalls,
		pm.Metrics.PeakErrorRate,
	)
	fmt.Println()

	// Timeline
	fmt.Println(output.TitleStyle.Render("⏱️  Timeline"))
	for _, ev := range pm.Timeline {
		icon := timelineIcon(ev.Type)
		fmt.Printf("  %s %s %s\n",
			output.MutedStyle.Render(ev.Timestamp.Format("15:04:05")),
			icon,
			ev.Description,
		)
	}
	fmt.Println()

	// Root cause
	fmt.Println(output.TitleStyle.Render("🔍 Root Cause"))
	fmt.Printf("  %s\n\n", pm.RootCause)

	if len(pm.Contributing) > 0 {
		fmt.Println(output.TitleStyle.Render("🔗 Contributing Factors"))
		for _, f := range pm.Contributing {
			fmt.Printf("  • %s\n", f)
		}
		fmt.Println()
	}

	// Detection
	fmt.Println(output.TitleStyle.Render("🔔 Detection"))
	fmt.Printf("  Method: %s  │  Time to Detect: %s\n\n", pm.Detection.Method, pm.Detection.TimeToDetect)

	// Resolution
	fmt.Println(output.TitleStyle.Render("✅ Resolution"))
	fmt.Printf("  %s\n\n", pm.Resolution)

	// Metrics
	if len(pm.Metrics.ServiceMetrics) > 0 {
		fmt.Println(output.TitleStyle.Render("📊 Service Metrics"))
		fmt.Printf("  %-30s %10s %10s %10s\n", "SERVICE", "ERR RATE", "ERRORS", "CALLS")
		fmt.Printf("  %s\n", strings.Repeat("─", 62))
		for _, sm := range pm.Metrics.ServiceMetrics {
			fmt.Printf("  %-30s %9.2f%% %10d %10d\n",
				truncateString(sm.Service, 30),
				sm.ErrorRate, sm.ErrorCount, sm.Calls,
			)
		}
		fmt.Println()
	}

	if len(pm.Metrics.TopErrors) > 0 {
		fmt.Println(output.TitleStyle.Render("🔴 Top Errors"))
		for _, e := range pm.Metrics.TopErrors {
			fmt.Printf("  [%s] (%dx) %s\n", e.Service, e.Count, e.Message)
		}
		fmt.Println()
	}

	// Action items
	if len(pm.ActionItems) > 0 {
		fmt.Println(output.TitleStyle.Render("📌 Action Items"))
		for _, ai := range pm.ActionItems {
			pIcon := priorityIcon(ai.Priority)
			tIcon := typeIcon(ai.Type)
			fmt.Printf("  %s %s %s %s\n", pIcon, tIcon, ai.Title,
				output.MutedStyle.Render(fmt.Sprintf("[%s]", ai.Status)))
		}
		fmt.Println()
	}

	// Lessons learned
	if len(pm.Lessons) > 0 {
		fmt.Println(output.TitleStyle.Render("💡 Lessons Learned"))
		for _, l := range pm.Lessons {
			fmt.Printf("  • %s\n", l)
		}
		fmt.Println()
	}
}

// RenderList renders a list of postmortems.
func RenderList(postmortems []Postmortem) {
	if len(postmortems) == 0 {
		fmt.Println("  No postmortems found.")
		return
	}

	fmt.Println(output.TitleStyle.Render("📋 Postmortems"))
	fmt.Printf("  %-16s %-12s %-10s %-10s %-40s %s\n",
		"ID", "INCIDENT", "SEVERITY", "STATUS", "TITLE", "CREATED")
	fmt.Printf("  %s\n", strings.Repeat("─", 110))

	for _, pm := range postmortems {
		fmt.Printf("  %-16s %-12s %-10s %-10s %-40s %s\n",
			pm.ID,
			pm.IncidentID,
			incident.SeverityIcon(pm.Severity)+" "+pm.Severity,
			pm.Status,
			truncateString(pm.Title, 40),
			pm.CreatedAt.Format("2006-01-02"),
		)
	}
}

// RenderMarkdown returns a markdown representation of the postmortem.
func RenderMarkdown(pm *Postmortem) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", pm.Title))
	sb.WriteString("| Field | Value |\n|-------|-------|\n")
	sb.WriteString(fmt.Sprintf("| ID | %s |\n", pm.ID))
	sb.WriteString(fmt.Sprintf("| Incident | %s |\n", pm.IncidentID))
	sb.WriteString(fmt.Sprintf("| Severity | %s |\n", pm.Severity))
	sb.WriteString(fmt.Sprintf("| Status | %s |\n", pm.Status))
	sb.WriteString(fmt.Sprintf("| Services | %s |\n", strings.Join(pm.Services, ", ")))
	sb.WriteString(fmt.Sprintf("| Commander | %s |\n", pm.Commander))
	sb.WriteString(fmt.Sprintf("| Window | %s → %s (%s) |\n",
		pm.IncidentTime.Start.Format("2006-01-02 15:04"),
		pm.IncidentTime.End.Format("15:04"),
		pm.IncidentTime.Duration,
	))
	sb.WriteString(fmt.Sprintf("| Created | %s |\n\n", pm.CreatedAt.Format("2006-01-02 15:04")))

	sb.WriteString("## Summary\n\n")
	sb.WriteString(pm.Summary + "\n\n")

	sb.WriteString("## Impact\n\n")
	if pm.Impact.Description != "" {
		sb.WriteString(pm.Impact.Description + "\n\n")
	}
	sb.WriteString(fmt.Sprintf("- **Duration:** %.0f minutes\n", pm.Impact.DurationMinutes))
	sb.WriteString(fmt.Sprintf("- **Total Errors:** %d\n", pm.Impact.ErrorCount))
	sb.WriteString(fmt.Sprintf("- **Peak Error Rate:** %.2f%%\n\n", pm.Metrics.PeakErrorRate))

	sb.WriteString("## Timeline\n\n")
	sb.WriteString("| Time | Type | Description |\n|------|------|-------------|\n")
	for _, ev := range pm.Timeline {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
			ev.Timestamp.Format("15:04:05"), ev.Type, ev.Description))
	}
	sb.WriteString("\n")

	sb.WriteString("## Root Cause\n\n")
	sb.WriteString(pm.RootCause + "\n\n")

	if len(pm.Contributing) > 0 {
		sb.WriteString("## Contributing Factors\n\n")
		for _, f := range pm.Contributing {
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Detection\n\n")
	sb.WriteString(fmt.Sprintf("- **Method:** %s\n", pm.Detection.Method))
	sb.WriteString(fmt.Sprintf("- **Time to Detect:** %s\n\n", pm.Detection.TimeToDetect))

	sb.WriteString("## Resolution\n\n")
	sb.WriteString(pm.Resolution + "\n\n")

	if len(pm.Metrics.ServiceMetrics) > 0 {
		sb.WriteString("## Service Metrics\n\n")
		sb.WriteString("| Service | Error Rate | Errors | Calls |\n")
		sb.WriteString("|---------|-----------|--------|-------|\n")
		for _, sm := range pm.Metrics.ServiceMetrics {
			sb.WriteString(fmt.Sprintf("| %s | %.2f%% | %d | %d |\n",
				sm.Service, sm.ErrorRate, sm.ErrorCount, sm.Calls))
		}
		sb.WriteString("\n")
	}

	if len(pm.Metrics.TopErrors) > 0 {
		sb.WriteString("## Top Errors\n\n")
		for _, e := range pm.Metrics.TopErrors {
			sb.WriteString(fmt.Sprintf("- **[%s]** (%dx) %s\n", e.Service, e.Count, e.Message))
		}
		sb.WriteString("\n")
	}

	if len(pm.ActionItems) > 0 {
		sb.WriteString("## Action Items\n\n")
		sb.WriteString("| Priority | Type | Title | Status |\n")
		sb.WriteString("|----------|------|-------|--------|\n")
		for _, item := range pm.ActionItems {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				item.Priority, item.Type, item.Title, item.Status))
		}
		sb.WriteString("\n")
	}

	if len(pm.Lessons) > 0 {
		sb.WriteString("## Lessons Learned\n\n")
		for _, l := range pm.Lessons {
			sb.WriteString(fmt.Sprintf("- %s\n", l))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatJSON returns the postmortem as formatted JSON.
func FormatJSON(pm *Postmortem) (string, error) {
	data, err := json.MarshalIndent(pm, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

func timelineIcon(eventType string) string {
	switch eventType {
	case "detection":
		return "🔍"
	case "error_spike":
		return "📈"
	case "mitigation":
		return "🛠️"
	case "resolution":
		return "✅"
	case "status_change":
		return "🔄"
	default:
		return "•"
	}
}

func priorityIcon(p string) string {
	switch p {
	case "P0":
		return "🔴"
	case "P1":
		return "🟠"
	case "P2":
		return "🟡"
	case "P3":
		return "🟢"
	default:
		return "⚪"
	}
}

func typeIcon(t string) string {
	switch t {
	case "prevent":
		return "🛡️"
	case "detect":
		return "🔔"
	case "mitigate":
		return "🔧"
	case "process":
		return "📝"
	default:
		return "📌"
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

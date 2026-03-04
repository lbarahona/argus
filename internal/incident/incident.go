package incident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/output"
	"gopkg.in/yaml.v3"
)

// ──────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────

// Severity levels for incidents.
const (
	SeverityCritical = "critical"
	SeverityMajor    = "major"
	SeverityMinor    = "minor"
)

// Status values for incidents.
const (
	StatusOpen       = "open"
	StatusInvestigating = "investigating"
	StatusIdentified = "identified"
	StatusMonitoring = "monitoring"
	StatusResolved   = "resolved"
)

// Incident represents a single incident.
type Incident struct {
	ID          string           `yaml:"id" json:"id"`
	Title       string           `yaml:"title" json:"title"`
	Severity    string           `yaml:"severity" json:"severity"`
	Status      string           `yaml:"status" json:"status"`
	Services    []string         `yaml:"services,omitempty" json:"services,omitempty"`
	Commander   string           `yaml:"commander,omitempty" json:"commander,omitempty"`
	Description string           `yaml:"description,omitempty" json:"description,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	CreatedAt   time.Time        `yaml:"created_at" json:"created_at"`
	ResolvedAt  *time.Time       `yaml:"resolved_at,omitempty" json:"resolved_at,omitempty"`
	Timeline    []TimelineEntry  `yaml:"timeline" json:"timeline"`
	Duration    string           `yaml:"duration,omitempty" json:"duration,omitempty"`
}

// TimelineEntry is a single event in the incident timeline.
type TimelineEntry struct {
	Timestamp time.Time `yaml:"timestamp" json:"timestamp"`
	Status    string    `yaml:"status" json:"status"`
	Message   string    `yaml:"message" json:"message"`
	Author    string    `yaml:"author,omitempty" json:"author,omitempty"`
}

// IncidentStore holds all incidents.
type IncidentStore struct {
	Incidents []Incident `yaml:"incidents" json:"incidents"`
}

// ──────────────────────────────────────────────
// Storage
// ──────────────────────────────────────────────

func storeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".argus")
}

func storePath() string {
	return filepath.Join(storeDir(), "incidents.yaml")
}

// Load reads the incident store from disk.
func Load() (*IncidentStore, error) {
	path := storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &IncidentStore{}, nil
		}
		return nil, fmt.Errorf("reading incidents: %w", err)
	}
	var store IncidentStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parsing incidents: %w", err)
	}
	return &store, nil
}

// Save writes the incident store to disk.
func (s *IncidentStore) Save() error {
	if err := os.MkdirAll(storeDir(), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(storePath(), data, 0644)
}

// ──────────────────────────────────────────────
// Operations
// ──────────────────────────────────────────────

// generateID creates a short incident ID like INC-20260222-001.
func (s *IncidentStore) generateID() string {
	date := time.Now().Format("20060102")
	prefix := "INC-" + date + "-"
	max := 0
	for _, inc := range s.Incidents {
		if strings.HasPrefix(inc.ID, prefix) {
			var num int
			fmt.Sscanf(inc.ID[len(prefix):], "%d", &num)
			if num > max {
				max = num
			}
		}
	}
	return fmt.Sprintf("%s%03d", prefix, max+1)
}

// Create adds a new incident.
func (s *IncidentStore) Create(title, severity string, services []string, commander, description string) *Incident {
	now := time.Now()
	inc := Incident{
		ID:          s.generateID(),
		Title:       title,
		Severity:    severity,
		Status:      StatusOpen,
		Services:    services,
		Commander:   commander,
		Description: description,
		CreatedAt:   now,
		Timeline: []TimelineEntry{
			{
				Timestamp: now,
				Status:    StatusOpen,
				Message:   "Incident created: " + title,
				Author:    commander,
			},
		},
	}
	s.Incidents = append(s.Incidents, inc)
	return &s.Incidents[len(s.Incidents)-1]
}

// FindByID returns an incident by ID (case-insensitive, partial match).
func (s *IncidentStore) FindByID(id string) *Incident {
	id = strings.ToUpper(id)
	for i := range s.Incidents {
		if strings.ToUpper(s.Incidents[i].ID) == id {
			return &s.Incidents[i]
		}
	}
	// Partial match (just the number)
	for i := range s.Incidents {
		if strings.HasSuffix(strings.ToUpper(s.Incidents[i].ID), id) {
			return &s.Incidents[i]
		}
	}
	return nil
}

// Update changes incident status and adds a timeline entry.
func (inc *Incident) Update(status, message, author string) {
	inc.Status = status
	entry := TimelineEntry{
		Timestamp: time.Now(),
		Status:    status,
		Message:   message,
		Author:    author,
	}
	inc.Timeline = append(inc.Timeline, entry)

	if status == StatusResolved {
		now := time.Now()
		inc.ResolvedAt = &now
		inc.Duration = now.Sub(inc.CreatedAt).Round(time.Minute).String()
	}
}

// ActiveIncidents returns non-resolved incidents sorted by severity.
func (s *IncidentStore) ActiveIncidents() []Incident {
	var active []Incident
	for _, inc := range s.Incidents {
		if inc.Status != StatusResolved {
			active = append(active, inc)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return severityRank(active[i].Severity) > severityRank(active[j].Severity)
	})
	return active
}

// RecentIncidents returns the N most recent incidents.
func (s *IncidentStore) RecentIncidents(n int) []Incident {
	sorted := make([]Incident, len(s.Incidents))
	copy(sorted, s.Incidents)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
	})
	if n > 0 && n < len(sorted) {
		sorted = sorted[:n]
	}
	return sorted
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityMajor:
		return 2
	case SeverityMinor:
		return 1
	default:
		return 0
	}
}

// ──────────────────────────────────────────────
// Rendering
// ──────────────────────────────────────────────

// SeverityIcon returns the emoji for a severity level.
func SeverityIcon(s string) string {
	switch s {
	case SeverityCritical:
		return "🔴"
	case SeverityMajor:
		return "🟠"
	case SeverityMinor:
		return "🟡"
	default:
		return "⚪"
	}
}

// StatusIcon returns the emoji for a status.
func StatusIcon(s string) string {
	switch s {
	case StatusOpen:
		return "🚨"
	case StatusInvestigating:
		return "🔍"
	case StatusIdentified:
		return "🎯"
	case StatusMonitoring:
		return "👀"
	case StatusResolved:
		return "✅"
	default:
		return "❓"
	}
}

// RenderList prints a table of incidents.
func RenderList(incidents []Incident, title string) {
	if len(incidents) == 0 {
		fmt.Println(output.MutedStyle.Render("  No incidents found."))
		return
	}

	fmt.Printf("\n%s (%d)\n\n", title, len(incidents))

	// Header
	fmt.Printf("  %-20s %-10s %-15s %-30s %s\n",
		output.MutedStyle.Render("ID"),
		output.MutedStyle.Render("SEV"),
		output.MutedStyle.Render("STATUS"),
		output.MutedStyle.Render("TITLE"),
		output.MutedStyle.Render("AGE"),
	)
	fmt.Println(output.MutedStyle.Render("  " + strings.Repeat("─", 90)))

	for _, inc := range incidents {
		age := time.Since(inc.CreatedAt).Round(time.Minute)
		ageStr := formatDuration(age)
		if inc.Status == StatusResolved && inc.ResolvedAt != nil {
			ageStr = inc.Duration + " (resolved)"
		}

		title := inc.Title
		if len(title) > 28 {
			title = title[:28] + "…"
		}

		sevStyle := output.MutedStyle
		switch inc.Severity {
		case SeverityCritical:
			sevStyle = output.ErrorStyle
		case SeverityMajor:
			sevStyle = output.WarningStyle
		}

		fmt.Printf("  %-20s %s %-8s %s %-13s %-30s %s\n",
			output.AccentStyle.Render(inc.ID),
			SeverityIcon(inc.Severity),
			sevStyle.Render(inc.Severity),
			StatusIcon(inc.Status),
			inc.Status,
			title,
			output.MutedStyle.Render(ageStr),
		)
	}
	fmt.Println()
}

// RenderTimeline prints the timeline for a single incident.
func RenderTimeline(inc *Incident) {
	fmt.Println()
	// Header
	fmt.Printf("  %s %s  %s\n",
		SeverityIcon(inc.Severity),
		output.AccentStyle.Render(inc.ID),
		inc.Title,
	)
	fmt.Printf("  Status: %s %s  |  Severity: %s  |  Commander: %s\n",
		StatusIcon(inc.Status),
		inc.Status,
		inc.Severity,
		defaultStr(inc.Commander, "unassigned"),
	)
	if len(inc.Services) > 0 {
		fmt.Printf("  Services: %s\n", strings.Join(inc.Services, ", "))
	}
	if inc.Description != "" {
		fmt.Printf("  Description: %s\n", inc.Description)
	}
	fmt.Printf("  Created: %s", inc.CreatedAt.Format("2006-01-02 15:04:05"))
	if inc.ResolvedAt != nil {
		fmt.Printf("  |  Resolved: %s  |  Duration: %s",
			inc.ResolvedAt.Format("2006-01-02 15:04:05"), inc.Duration)
	}
	fmt.Println()

	// Timeline
	fmt.Println()
	fmt.Printf("  %s\n", output.MutedStyle.Render("Timeline"))
	fmt.Println(output.MutedStyle.Render("  " + strings.Repeat("─", 70)))

	for i, entry := range inc.Timeline {
		connector := "├"
		if i == len(inc.Timeline)-1 {
			connector = "└"
		}
		author := ""
		if entry.Author != "" {
			author = fmt.Sprintf(" (%s)", entry.Author)
		}
		fmt.Printf("  %s─ %s %s %s%s\n",
			connector,
			output.MutedStyle.Render(entry.Timestamp.Format("15:04:05")),
			StatusIcon(entry.Status),
			entry.Message,
			output.MutedStyle.Render(author),
		)
	}
	fmt.Println()
}

// FormatJSON returns JSON output of incidents.
func FormatJSON(incidents []Incident) (string, error) {
	data, err := json.MarshalIndent(incidents, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func formatDuration(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

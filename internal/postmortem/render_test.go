package postmortem

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// captureStdout captures stdout output from a function.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func samplePostmortem() *Postmortem {
	now := time.Now()
	end := now.Add(2 * time.Hour)
	return &Postmortem{
		ID:         "pm-test-001",
		IncidentID: "inc-001",
		Title:      "Postmortem: Database connection pool exhaustion",
		Severity:   "critical",
		Status:     "draft",
		Services:   []string{"api-gateway", "user-service"},
		Commander:  "oncall-sre",
		Authors:    []string{"sre-team"},
		CreatedAt:  now,
		UpdatedAt:  now,
		IncidentTime: IncidentWindow{
			Start:    now,
			End:      end,
			Duration: "2h",
		},
		Summary:    "Critical severity incident affecting api-gateway, user-service. Connection pool maxed out causing cascading failures.",
		RootCause:  "Connection leak in user-service caused pool exhaustion.",
		Resolution: "Fixed connection handling and deployed hotfix.",
		Impact: Impact{
			Description:      "Users experienced 503 errors on API requests.",
			AffectedServices: []string{"api-gateway", "user-service"},
			UserFacing:       true,
			ErrorCount:       500,
			DurationMinutes:  120,
		},
		Timeline: []TimelineEvent{
			{Timestamp: now, Type: "detection", Description: "Alert fired: high error rate", Source: "incident"},
			{Timestamp: now.Add(5 * time.Minute), Type: "status_change", Description: "[investigating] Looking into the issue", Source: "incident"},
			{Timestamp: now.Add(30 * time.Minute), Type: "mitigation", Description: "[identified] Connection leak found", Source: "incident"},
			{Timestamp: now.Add(90 * time.Minute), Type: "resolution", Description: "[resolved] Fix deployed", Source: "incident"},
		},
		Detection: Detection{
			Method:        "alert",
			TimeToDetect:  "2m",
			DetectMinutes: 2.0,
		},
		ActionItems: []ActionItem{
			{ID: "ai-1", Priority: "P0", Type: "prevent", Title: "Fix connection leak", Status: "todo"},
			{ID: "ai-2", Priority: "P1", Type: "detect", Title: "Add connection pool alerts", Status: "in_progress"},
			{ID: "ai-3", Priority: "P2", Type: "mitigate", Title: "Circuit breaker pattern", Status: "todo"},
			{ID: "ai-4", Priority: "P3", Type: "process", Title: "Update runbook", Status: "done"},
		},
		Contributing: []string{
			"No connection pool monitoring",
			"Missing circuit breaker",
		},
		Lessons: []string{
			"Monitor connection pools",
			"Implement circuit breakers early",
		},
		Metrics: MetricsSummary{
			PeakErrorRate: 0.15,
			TotalErrors:   500,
			TotalCalls:    10000,
			ServiceMetrics: []ServiceMetric{
				{Service: "api-gateway", ErrorRate: 0.05, ErrorCount: 300, Calls: 6000},
				{Service: "user-service", ErrorRate: 0.10, ErrorCount: 200, Calls: 4000},
			},
			TopErrors: []ErrorSummary{
				{Message: "connection pool exhausted", Count: 200, Service: "api-gateway"},
				{Message: "connection timeout", Count: 100, Service: "user-service"},
			},
		},
	}
}

// ──────────────────────────────────────────────
// Tests: RenderTerminal
// ──────────────────────────────────────────────

func TestRenderTerminal_FullPostmortem(t *testing.T) {
	pm := samplePostmortem()
	output := captureStdout(func() {
		RenderTerminal(pm)
	})

	assert.Contains(t, output, "Database connection pool exhaustion")
	assert.Contains(t, output, "pm-test-001")
	assert.Contains(t, output, "critical")
	assert.Contains(t, output, "inc-001")
	assert.Contains(t, output, "oncall-sre")
	assert.Contains(t, output, "api-gateway, user-service")
	assert.Contains(t, output, "Summary")
	assert.Contains(t, output, "Impact")
	assert.Contains(t, output, "Timeline")
	assert.Contains(t, output, "Root Cause")
	assert.Contains(t, output, "Connection leak")
	assert.Contains(t, output, "Contributing Factors")
	assert.Contains(t, output, "Detection")
	assert.Contains(t, output, "Resolution")
	assert.Contains(t, output, "Service Metrics")
	assert.Contains(t, output, "Top Errors")
	assert.Contains(t, output, "Action Items")
	assert.Contains(t, output, "Lessons Learned")
}

func TestRenderTerminal_DataCaveat(t *testing.T) {
	pm := samplePostmortem()
	pm.DataCaveat = "Signoz metrics reflect the query time, not the incident window (querier does not support absolute time ranges)."

	output := captureStdout(func() {
		RenderTerminal(pm)
	})

	assert.Contains(t, output, pm.DataCaveat)
}

func TestRenderTerminal_NoDataCaveat(t *testing.T) {
	pm := samplePostmortem()
	pm.DataCaveat = ""

	output := captureStdout(func() {
		RenderTerminal(pm)
	})

	assert.NotContains(t, output, "Data caveat")
	assert.NotContains(t, output, "⚠️")
}

func TestRenderTerminal_MinimalPostmortem(t *testing.T) {
	pm := &Postmortem{
		ID:         "pm-minimal",
		IncidentID: "inc-min",
		Title:      "Postmortem: Minor issue",
		Severity:   "minor",
		Status:     "draft",
		Summary:    "A minor issue occurred.",
		RootCause:  "Unknown.",
		Resolution: "Issue resolved itself.",
		IncidentTime: IncidentWindow{
			Start:    time.Now(),
			End:      time.Now().Add(10 * time.Minute),
			Duration: "10m",
		},
		Detection: Detection{Method: "manual", TimeToDetect: "5m"},
	}

	output := captureStdout(func() {
		RenderTerminal(pm)
	})

	assert.Contains(t, output, "Minor issue")
	assert.Contains(t, output, "minor")
	// Should NOT have sections with empty data
	assert.NotContains(t, output, "Contributing Factors")
	assert.NotContains(t, output, "Top Errors")
	assert.NotContains(t, output, "Lessons Learned")
}

// ──────────────────────────────────────────────
// Tests: RenderList
// ──────────────────────────────────────────────

func TestRenderList_WithPostmortems(t *testing.T) {
	pms := []Postmortem{
		{
			ID:         "pm-001",
			IncidentID: "inc-001",
			Title:      "Postmortem: DB outage",
			Severity:   "critical",
			Status:     "published",
			CreatedAt:  time.Now().Add(-24 * time.Hour),
		},
		{
			ID:         "pm-002",
			IncidentID: "inc-002",
			Title:      "Postmortem: API latency spike with a very long title that should be truncated",
			Severity:   "major",
			Status:     "draft",
			CreatedAt:  time.Now(),
		},
	}

	output := captureStdout(func() {
		RenderList(pms)
	})

	assert.Contains(t, output, "Postmortems")
	assert.Contains(t, output, "pm-001")
	assert.Contains(t, output, "pm-002")
	assert.Contains(t, output, "inc-001")
	assert.Contains(t, output, "critical")
	assert.Contains(t, output, "major")
}

func TestRenderList_Empty(t *testing.T) {
	output := captureStdout(func() {
		RenderList(nil)
	})

	assert.Contains(t, output, "No postmortems found")
}

// ──────────────────────────────────────────────
// Tests: Icon helpers
// ──────────────────────────────────────────────

func TestTimelineIcon(t *testing.T) {
	tests := []struct {
		eventType string
		expected  string
	}{
		{"detection", "🔍"},
		{"error_spike", "📈"},
		{"mitigation", "🛠️"},
		{"resolution", "✅"},
		{"status_change", "🔄"},
		{"unknown", "•"},
	}

	for _, tt := range tests {
		result := timelineIcon(tt.eventType)
		assert.Equal(t, tt.expected, result, "icon for %s", tt.eventType)
	}
}

func TestPriorityIcon(t *testing.T) {
	tests := []struct {
		priority string
		expected string
	}{
		{"P0", "🔴"},
		{"P1", "🟠"},
		{"P2", "🟡"},
		{"P3", "🟢"},
		{"P9", "⚪"},
	}

	for _, tt := range tests {
		result := priorityIcon(tt.priority)
		assert.Equal(t, tt.expected, result, "icon for %s", tt.priority)
	}
}

func TestTypeIcon(t *testing.T) {
	tests := []struct {
		typ      string
		expected string
	}{
		{"prevent", "🛡️"},
		{"detect", "🔔"},
		{"mitigate", "🔧"},
		{"process", "📝"},
		{"unknown", "📌"},
	}

	for _, tt := range tests {
		result := typeIcon(tt.typ)
		assert.Equal(t, tt.expected, result, "icon for %s", tt.typ)
	}
}

// ──────────────────────────────────────────────
// Tests: buildAIPrompt
// ──────────────────────────────────────────────

func TestBuildAIPrompt(t *testing.T) {
	pm := samplePostmortem()
	prompt := buildAIPrompt(pm)

	assert.Contains(t, prompt, "blameless postmortem")
	assert.Contains(t, prompt, "ROOT CAUSE:")
	assert.Contains(t, prompt, "ACTION ITEMS:")
	assert.Contains(t, prompt, "LESSONS LEARNED:")
	assert.Contains(t, prompt, "Database connection pool exhaustion")
	assert.Contains(t, prompt, "critical")
	assert.Contains(t, prompt, "api-gateway, user-service")
	assert.Contains(t, prompt, "Timeline:")
	assert.Contains(t, prompt, "Total errors: 500")
	assert.Contains(t, prompt, "connection pool exhausted")
}

func TestBuildAIPrompt_DataCaveat(t *testing.T) {
	pm := samplePostmortem()
	pm.DataCaveat = "Signoz metrics reflect the query time, not the incident window (querier does not support absolute time ranges)."

	prompt := buildAIPrompt(pm)
	assert.Contains(t, prompt, "NOTE: "+pm.DataCaveat)
}

func TestBuildAIPrompt_NoDataCaveat(t *testing.T) {
	pm := samplePostmortem()
	pm.DataCaveat = ""

	prompt := buildAIPrompt(pm)
	assert.NotContains(t, prompt, "NOTE:")
}

func TestBuildAIPrompt_NoMetrics(t *testing.T) {
	pm := &Postmortem{
		Title:    "Test Postmortem",
		Severity: "minor",
		Services: []string{"svc"},
		IncidentTime: IncidentWindow{
			Duration: "10m",
		},
		Detection: Detection{
			Method:       "manual",
			TimeToDetect: "5m",
		},
		Resolution: "Fixed.",
		Timeline: []TimelineEvent{
			{Timestamp: time.Now(), Type: "detection", Description: "Found the issue"},
		},
	}

	prompt := buildAIPrompt(pm)
	assert.Contains(t, prompt, "Test Postmortem")
	// Should NOT contain metrics section when TotalErrors is 0
	assert.NotContains(t, prompt, "Total errors:")
}

// ──────────────────────────────────────────────
// Tests: defaultStr
// ──────────────────────────────────────────────

func TestDefaultStr(t *testing.T) {
	assert.Equal(t, "hello", defaultStr("hello", "default"))
	assert.Equal(t, "default", defaultStr("", "default"))
	assert.Equal(t, "", defaultStr("", ""))
}

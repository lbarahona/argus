package postmortem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/internal/incident"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────
// Mock Querier
// ──────────────────────────────────────────────

type mockQuerier struct {
	services []types.Service
	logs     *types.QueryResult
	traces   *types.QueryResult
	metrics  *types.QueryResult
	healthy  bool
}

func (m *mockQuerier) Health(ctx context.Context) (bool, time.Duration, error) {
	return m.healthy, 10 * time.Millisecond, nil
}

func (m *mockQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	return m.logs, nil
}

func (m *mockQuerier) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	return m.traces, nil
}

func (m *mockQuerier) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return m.metrics, nil
}

// ──────────────────────────────────────────────
// Helper: create test incident store
// ──────────────────────────────────────────────

func setupTestIncident(t *testing.T) func() {
	t.Helper()

	// Override home dir for test
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)

	// Create incidents file
	argusDir := filepath.Join(tmpDir, ".argus")
	os.MkdirAll(argusDir, 0o755)

	now := time.Now()
	resolved := now.Add(-10 * time.Minute)

	store := &incident.IncidentStore{
		Incidents: []incident.Incident{
			{
				ID:          "inc-001",
				Title:       "Database connection pool exhaustion",
				Severity:    "critical",
				Status:      "resolved",
				Services:    []string{"api-gateway", "user-service"},
				Commander:   "oncall-sre",
				Description: "Connection pool maxed out causing cascading failures",
				CreatedAt:   now.Add(-2 * time.Hour),
				ResolvedAt:  &resolved,
				Timeline: []incident.TimelineEntry{
					{
						Timestamp: now.Add(-2 * time.Hour),
						Status:    "open",
						Message:   "Alert fired: high error rate on api-gateway",
						Author:    "pagerduty",
					},
					{
						Timestamp: now.Add(-110 * time.Minute),
						Status:    "investigating",
						Message:   "Investigating connection pool metrics",
						Author:    "oncall-sre",
					},
					{
						Timestamp: now.Add(-90 * time.Minute),
						Status:    "identified",
						Message:   "Root cause identified: connection leak in user-service",
						Author:    "oncall-sre",
					},
					{
						Timestamp: now.Add(-60 * time.Minute),
						Status:    "monitoring",
						Message:   "Mitigation deployed, monitoring recovery",
						Author:    "oncall-sre",
					},
					{
						Timestamp: now.Add(-10 * time.Minute),
						Status:    "resolved",
						Message:   "Resolved: connection pool back to normal after fix deployed",
						Author:    "oncall-sre",
					},
				},
			},
			{
				ID:          "inc-002",
				Title:       "Elevated latency on payment service",
				Severity:    "major",
				Status:      "open",
				Services:    []string{"payment-service"},
				Commander:   "",
				Description: "",
				CreatedAt:   now.Add(-30 * time.Minute),
				Timeline: []incident.TimelineEntry{
					{
						Timestamp: now.Add(-30 * time.Minute),
						Status:    "open",
						Message:   "Customer reported slow checkout",
						Author:    "support",
					},
				},
			},
		},
	}

	if err := store.Save(); err != nil {
		t.Fatalf("saving test incidents: %v", err)
	}

	return func() {
		os.Setenv("HOME", origHome)
	}
}

// ──────────────────────────────────────────────
// Tests: Generation
// ──────────────────────────────────────────────

func TestGenerate_ResolvedIncident(t *testing.T) {
	cleanup := setupTestIncident(t)
	defer cleanup()

	pm, err := Generate(context.Background(), Options{
		IncidentID: "inc-001",
	})

	require.NoError(t, err)
	assert.Equal(t, "inc-001", pm.IncidentID)
	assert.Equal(t, "Postmortem: Database connection pool exhaustion", pm.Title)
	assert.Equal(t, "critical", pm.Severity)
	assert.Equal(t, "draft", pm.Status)
	assert.Equal(t, []string{"api-gateway", "user-service"}, pm.Services)
	assert.Equal(t, "oncall-sre", pm.Commander)
	assert.NotEmpty(t, pm.Summary)
	assert.NotEmpty(t, pm.Resolution)
	assert.Contains(t, pm.Resolution, "connection pool back to normal")
}

func TestGenerate_OngoingIncident(t *testing.T) {
	cleanup := setupTestIncident(t)
	defer cleanup()

	pm, err := Generate(context.Background(), Options{
		IncidentID: "inc-002",
	})

	require.NoError(t, err)
	assert.Equal(t, "inc-002", pm.IncidentID)
	assert.Equal(t, "major", pm.Severity)
	assert.Contains(t, pm.IncidentTime.Duration, "ongoing")
	assert.Equal(t, "Incident is still ongoing.", pm.Resolution)
}

func TestGenerate_NotFound(t *testing.T) {
	cleanup := setupTestIncident(t)
	defer cleanup()

	_, err := Generate(context.Background(), Options{
		IncidentID: "inc-999",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGenerate_WithMetrics(t *testing.T) {
	cleanup := setupTestIncident(t)
	defer cleanup()

	q := &mockQuerier{
		services: []types.Service{
			{Name: "api-gateway", NumErrors: 150, NumCalls: 5000, ErrorRate: 3.0},
			{Name: "user-service", NumErrors: 80, NumCalls: 3000, ErrorRate: 2.67},
		},
		logs: &types.QueryResult{
			Logs: []types.LogEntry{
				{Body: "connection pool exhausted", SeverityText: "error", ServiceName: "api-gateway"},
				{Body: "connection pool exhausted", SeverityText: "error", ServiceName: "api-gateway"},
				{Body: "connection pool exhausted", SeverityText: "error", ServiceName: "api-gateway"},
				{Body: "timeout waiting for connection", SeverityText: "error", ServiceName: "api-gateway"},
			},
		},
		healthy: true,
	}

	pm, err := Generate(context.Background(), Options{
		IncidentID: "inc-001",
		Querier:    q,
	})

	require.NoError(t, err)
	assert.Equal(t, 230, pm.Metrics.TotalErrors)
	assert.Equal(t, 8000, pm.Metrics.TotalCalls)
	assert.InDelta(t, 3.0, pm.Metrics.PeakErrorRate, 0.001)
	assert.Len(t, pm.Metrics.ServiceMetrics, 2)
	assert.NotEmpty(t, pm.Metrics.TopErrors)
}

// ──────────────────────────────────────────────
// Tests: Timeline building
// ──────────────────────────────────────────────

func TestBuildTimeline(t *testing.T) {
	now := time.Now()
	resolved := now.Add(2 * time.Hour)
	inc := &incident.Incident{
		ID:        "inc-t1",
		Title:     "Test incident",
		Severity:  "major",
		CreatedAt: now,
		ResolvedAt: &resolved,
		Timeline: []incident.TimelineEntry{
			{Timestamp: now.Add(5 * time.Minute), Status: "investigating", Message: "Investigating the issue"},
			{Timestamp: now.Add(30 * time.Minute), Status: "identified", Message: "Alert triggered from monitoring"},
			{Timestamp: now.Add(60 * time.Minute), Status: "monitoring", Message: "Mitigation applied"},
			{Timestamp: now.Add(120 * time.Minute), Status: "resolved", Message: "Resolved the issue"},
		},
	}

	events := buildTimeline(inc)

	assert.Len(t, events, 5) // 1 creation + 4 timeline entries
	assert.Equal(t, "detection", events[0].Type)                        // creation
	assert.Equal(t, "status_change", events[1].Type)                    // investigating
	assert.Equal(t, "detection", events[2].Type)                        // has "alert"
	assert.Equal(t, "mitigation", events[3].Type)                       // has "mitigat"
	assert.Equal(t, "resolution", events[4].Type)                       // status=resolved
}

func TestBuildTimeline_Empty(t *testing.T) {
	inc := &incident.Incident{
		ID:        "inc-t2",
		Title:     "Empty timeline",
		Severity:  "minor",
		CreatedAt: time.Now(),
	}

	events := buildTimeline(inc)
	assert.Len(t, events, 1) // Just the creation event
}

// ──────────────────────────────────────────────
// Tests: Detection analysis
// ──────────────────────────────────────────────

func TestAnalyzeDetection_Alert(t *testing.T) {
	now := time.Now()
	inc := &incident.Incident{
		CreatedAt: now,
		Timeline: []incident.TimelineEntry{
			{Timestamp: now.Add(2 * time.Minute), Status: "open", Message: "Alert fired: high error rate"},
		},
	}

	d := analyzeDetection(inc)
	assert.Equal(t, "alert", d.Method)
	assert.InDelta(t, 2.0, d.DetectMinutes, 0.1)
}

func TestAnalyzeDetection_CustomerReport(t *testing.T) {
	now := time.Now()
	inc := &incident.Incident{
		CreatedAt: now,
		Timeline: []incident.TimelineEntry{
			{Timestamp: now.Add(15 * time.Minute), Status: "open", Message: "Customer reported slow checkout"},
		},
	}

	d := analyzeDetection(inc)
	assert.Equal(t, "customer_report", d.Method)
}

func TestAnalyzeDetection_NoTimeline(t *testing.T) {
	d := analyzeDetection(&incident.Incident{CreatedAt: time.Now()})
	assert.Equal(t, "manual", d.Method)
}

// ──────────────────────────────────────────────
// Tests: AI response parsing
// ──────────────────────────────────────────────

func TestParseAIResponse(t *testing.T) {
	pm := &Postmortem{}

	response := `ROOT CAUSE: The database connection pool was exhausted due to a connection leak in the user-service.
Connections were not being properly returned after timeout errors.

CONTRIBUTING FACTORS:
- No connection pool monitoring alerts
- Missing circuit breaker between services
- Lack of connection leak detection

ACTION ITEMS:
- [P0] [PREVENT] Fix connection leak in user-service
- [P1] [DETECT] Add connection pool utilization alerts at 80% threshold
- [P2] [MITIGATE] Implement circuit breaker pattern for database connections
- [P3] [PROCESS] Update runbook with connection pool troubleshooting steps

LESSONS LEARNED:
- Connection pool monitoring should be a standard alert for all services
- Circuit breakers prevent cascading failures
- Blameless postmortems encourage transparency

IMPACT: Users experienced 503 errors on 3% of API requests for 2 hours during peak traffic. Approximately 1,500 failed requests affected checkout flow.`

	parseAIResponse(pm, response)

	assert.Contains(t, pm.RootCause, "connection pool was exhausted")
	assert.Len(t, pm.Contributing, 3)
	assert.Contains(t, pm.Contributing[0], "connection pool monitoring")
	assert.Len(t, pm.ActionItems, 4)
	assert.Equal(t, "P0", pm.ActionItems[0].Priority)
	assert.Equal(t, "prevent", pm.ActionItems[0].Type)
	assert.Equal(t, "P1", pm.ActionItems[1].Priority)
	assert.Equal(t, "detect", pm.ActionItems[1].Type)
	assert.Len(t, pm.Lessons, 3)
	assert.Contains(t, pm.Impact.Description, "503 errors")
}

func TestParseAIResponse_Partial(t *testing.T) {
	pm := &Postmortem{}

	response := `ROOT CAUSE: Unknown failure mode.

ACTION ITEMS:
- [P1] [DETECT] Investigate further`

	parseAIResponse(pm, response)

	assert.Equal(t, "Unknown failure mode.", pm.RootCause)
	assert.Len(t, pm.ActionItems, 1)
	assert.Empty(t, pm.Contributing)
	assert.Empty(t, pm.Lessons)
}

// ──────────────────────────────────────────────
// Tests: Action item parsing
// ──────────────────────────────────────────────

func TestParseActionItems(t *testing.T) {
	text := `- [P0] [PREVENT] Fix the root cause
- [P1] [DETECT] Add alerting
- [P2] [MITIGATE] Add circuit breaker
- [P3] [PROCESS] Update documentation`

	items := parseActionItems(text)

	require.Len(t, items, 4)
	assert.Equal(t, "P0", items[0].Priority)
	assert.Equal(t, "prevent", items[0].Type)
	assert.Equal(t, "Fix the root cause", items[0].Title)
	assert.Equal(t, "P3", items[3].Priority)
	assert.Equal(t, "process", items[3].Type)
}

func TestParseActionItems_NoPriority(t *testing.T) {
	text := "- Add better monitoring"
	items := parseActionItems(text)

	require.Len(t, items, 1)
	assert.Equal(t, "P2", items[0].Priority) // default
	assert.Equal(t, "prevent", items[0].Type)  // default
}

// ──────────────────────────────────────────────
// Tests: Basic action items
// ──────────────────────────────────────────────

func TestGenerateBasicActionItems_ManualDetection(t *testing.T) {
	pm := &Postmortem{
		Detection: Detection{Method: "manual"},
		Metrics:   MetricsSummary{PeakErrorRate: 5.0},
	}

	items := generateBasicActionItems(pm)
	assert.True(t, len(items) >= 2)
	assert.Equal(t, "P1", items[0].Priority)
	assert.Equal(t, "detect", items[0].Type)
}

func TestGenerateBasicActionItems_HighErrorRate(t *testing.T) {
	pm := &Postmortem{
		Detection: Detection{Method: "alert"},
		Metrics:   MetricsSummary{PeakErrorRate: 25.0},
	}

	items := generateBasicActionItems(pm)

	// Should have error rate alert + documentation
	found := false
	for _, item := range items {
		if item.Type == "detect" && item.Priority == "P1" {
			found = true
		}
	}
	assert.True(t, found, "should suggest error rate alerts")
}

func TestBuildAIPromptPeakErrorRateNotRescaled(t *testing.T) {
	pm := &Postmortem{
		Title:    "checkout outage",
		Severity: "major",
		Services: []string{"api"},
		Metrics: MetricsSummary{
			TotalErrors:   100,
			PeakErrorRate: 3.0, // percent, as ListServices reports it
		},
	}

	prompt := buildAIPrompt(pm)

	if !strings.Contains(prompt, "Peak error rate: 3.00%") {
		t.Errorf("prompt should report 3.00%% for PeakErrorRate=3.0, got:\n%s", prompt)
	}
}

func TestGenerateBasicActionItemsThresholdIsTenPercent(t *testing.T) {
	pm := &Postmortem{
		Metrics: MetricsSummary{PeakErrorRate: 3.0}, // 3% — below the 10% bar
	}
	items := generateBasicActionItems(pm)
	for _, it := range items {
		if strings.Contains(it.Title, "error rate threshold alerts") {
			t.Errorf("3%% peak error rate must not trigger the high-error-rate action item: %+v", it)
		}
	}
}

// ──────────────────────────────────────────────
// Tests: Storage
// ──────────────────────────────────────────────

func TestStoreSaveAndLoad(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	now := time.Now()
	store := &PostmortemStore{
		Postmortems: []Postmortem{
			{
				ID:        "pm-001",
				Title:     "Test postmortem",
				Severity:  "critical",
				Status:    "draft",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	err := store.Save()
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	require.Len(t, loaded.Postmortems, 1)
	assert.Equal(t, "pm-001", loaded.Postmortems[0].ID)
	assert.Equal(t, "Test postmortem", loaded.Postmortems[0].Title)
}

func TestLoadEmpty(t *testing.T) {
	origHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	store, err := Load()
	require.NoError(t, err)
	assert.Empty(t, store.Postmortems)
}

func TestFindByID(t *testing.T) {
	store := &PostmortemStore{
		Postmortems: []Postmortem{
			{ID: "pm-001", Title: "First"},
			{ID: "pm-002", Title: "Second"},
		},
	}

	pm := store.FindByID("pm-002")
	require.NotNil(t, pm)
	assert.Equal(t, "Second", pm.Title)

	assert.Nil(t, store.FindByID("pm-999"))
}

func TestFindByIncidentID(t *testing.T) {
	store := &PostmortemStore{
		Postmortems: []Postmortem{
			{ID: "pm-001", IncidentID: "inc-001"},
			{ID: "pm-002", IncidentID: "inc-002"},
		},
	}

	pm := store.FindByIncidentID("inc-002")
	require.NotNil(t, pm)
	assert.Equal(t, "pm-002", pm.ID)

	assert.Nil(t, store.FindByIncidentID("inc-999"))
}

func TestRecentPostmortems(t *testing.T) {
	now := time.Now()
	store := &PostmortemStore{
		Postmortems: []Postmortem{
			{ID: "pm-001", CreatedAt: now.Add(-3 * time.Hour)},
			{ID: "pm-002", CreatedAt: now.Add(-1 * time.Hour)},
			{ID: "pm-003", CreatedAt: now.Add(-2 * time.Hour)},
		},
	}

	recent := store.RecentPostmortems(2)
	require.Len(t, recent, 2)
	assert.Equal(t, "pm-002", recent[0].ID) // most recent first
	assert.Equal(t, "pm-003", recent[1].ID)
}

// ──────────────────────────────────────────────
// Tests: Rendering
// ──────────────────────────────────────────────

func TestRenderMarkdown(t *testing.T) {
	now := time.Now()
	end := now.Add(2 * time.Hour)
	pm := &Postmortem{
		ID:         "pm-test",
		IncidentID: "inc-001",
		Title:      "Postmortem: Database outage",
		Severity:   "critical",
		Status:     "draft",
		Services:   []string{"api-gateway"},
		Commander:  "oncall-sre",
		CreatedAt:  now,
		IncidentTime: IncidentWindow{
			Start:    now,
			End:      end,
			Duration: "2h",
		},
		Summary:    "Critical incident affecting api-gateway.",
		RootCause:  "Connection pool leak.",
		Resolution: "Fixed connection handling.",
		Impact: Impact{
			Description:     "Users saw errors.",
			DurationMinutes: 120,
			ErrorCount:      500,
		},
		Timeline: []TimelineEvent{
			{Timestamp: now, Type: "detection", Description: "Incident created"},
		},
		ActionItems: []ActionItem{
			{ID: "ai-1", Priority: "P0", Type: "prevent", Title: "Fix leak", Status: "todo"},
		},
		Lessons:      []string{"Monitor connection pools"},
		Contributing: []string{"No alerting configured"},
		Metrics: MetricsSummary{
			PeakErrorRate: 5.0,
			TotalErrors:   500,
			ServiceMetrics: []ServiceMetric{
				{Service: "api-gateway", ErrorRate: 5.0, ErrorCount: 500, Calls: 10000},
			},
		},
	}

	md := RenderMarkdown(pm)

	assert.Contains(t, md, "# Postmortem: Database outage")
	assert.Contains(t, md, "Connection pool leak")
	assert.Contains(t, md, "Monitor connection pools")
	assert.Contains(t, md, "No alerting configured")
	assert.Contains(t, md, "Fix leak")
	assert.Contains(t, md, "api-gateway")
}

func TestFormatJSON(t *testing.T) {
	pm := &Postmortem{
		ID:       "pm-json",
		Title:    "JSON test",
		Severity: "minor",
		Status:   "draft",
	}

	out, err := FormatJSON(pm)
	require.NoError(t, err)
	assert.Contains(t, out, `"id": "pm-json"`)
	assert.Contains(t, out, `"title": "JSON test"`)
}

// ──────────────────────────────────────────────
// Tests: Helpers
// ──────────────────────────────────────────────

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h"},
		{3*time.Hour + 15*time.Minute, "3h 15m"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, formatDuration(tt.d))
	}
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "hello", truncateString("hello", 10))
	assert.Equal(t, "hel...", truncateString("hello world", 6))
}

func TestBuildSummary(t *testing.T) {
	now := time.Now()
	resolved := now.Add(45 * time.Minute)

	inc := &incident.Incident{
		Severity:    "critical",
		Services:    []string{"api", "web"},
		Description: "Major outage",
		CreatedAt:   now,
		ResolvedAt:  &resolved,
	}

	summary := buildSummary(inc)
	assert.Contains(t, summary, "Critical")
	assert.Contains(t, summary, "api, web")
	assert.Contains(t, summary, "45m")
	assert.Contains(t, summary, "Major outage")
}

func TestBuildIncidentWindow_Resolved(t *testing.T) {
	now := time.Now()
	end := now.Add(2 * time.Hour)
	inc := &incident.Incident{
		CreatedAt:  now,
		ResolvedAt: &end,
	}

	w := buildIncidentWindow(inc)
	assert.Equal(t, now, w.Start)
	assert.Equal(t, end, w.End)
	assert.Equal(t, "2h", w.Duration)
}

func TestBuildIncidentWindow_Ongoing(t *testing.T) {
	inc := &incident.Incident{
		CreatedAt: time.Now().Add(-30 * time.Minute),
	}

	w := buildIncidentWindow(inc)
	assert.Contains(t, w.Duration, "ongoing")
}

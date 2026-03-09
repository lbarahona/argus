package incident

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) func() {
	t.Helper()
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".argus"), 0755)
	return func() { os.Setenv("HOME", origHome) }
}

func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// ──────────────────────────────────────────────
// Existing tests
// ──────────────────────────────────────────────

func TestCreateAndFind(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("API is down", SeverityCritical, []string{"api-service"}, "lester", "500 errors")

	if inc.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if inc.Status != StatusOpen {
		t.Errorf("expected status %s, got %s", StatusOpen, inc.Status)
	}
	if len(inc.Timeline) != 1 {
		t.Errorf("expected 1 timeline entry, got %d", len(inc.Timeline))
	}

	found := store.FindByID(inc.ID)
	if found == nil {
		t.Fatal("expected to find incident by ID")
	}
	if found.Title != "API is down" {
		t.Errorf("expected title 'API is down', got %q", found.Title)
	}
}

func TestUpdate(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Latency spike", SeverityMajor, nil, "", "")

	inc.Update(StatusInvestigating, "Looking into it", "toribio")
	if inc.Status != StatusInvestigating {
		t.Errorf("expected status %s, got %s", StatusInvestigating, inc.Status)
	}
	if len(inc.Timeline) != 2 {
		t.Errorf("expected 2 timeline entries, got %d", len(inc.Timeline))
	}

	inc.Update(StatusResolved, "Fixed the issue", "lester")
	if inc.Status != StatusResolved {
		t.Error("expected resolved status")
	}
	if inc.ResolvedAt == nil {
		t.Error("expected resolved_at to be set")
	}
	if inc.Duration == "" {
		t.Error("expected duration to be set")
	}
}

func TestActiveIncidents(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Create("Minor issue", SeverityMinor, nil, "", "")
	store.Create("Critical issue", SeverityCritical, nil, "", "")

	inc3 := store.Create("Resolved issue", SeverityMajor, nil, "", "")
	inc3.Update(StatusResolved, "Done", "")

	active := store.ActiveIncidents()
	if len(active) != 2 {
		t.Errorf("expected 2 active incidents, got %d", len(active))
	}
	// Critical should be first
	if active[0].Severity != SeverityCritical {
		t.Errorf("expected critical first, got %s", active[0].Severity)
	}
}

func TestSaveAndLoad(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Create("Test incident", SeverityMinor, []string{"svc-a"}, "tester", "testing")

	if err := store.Save(); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded.Incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(loaded.Incidents))
	}
	if loaded.Incidents[0].Title != "Test incident" {
		t.Errorf("expected 'Test incident', got %q", loaded.Incidents[0].Title)
	}
}

func TestRecentIncidents(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Incidents = []Incident{
		{ID: "INC-1", Title: "Old", CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "INC-3", Title: "New", CreatedAt: time.Now()},
		{ID: "INC-2", Title: "Mid", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}

	recent := store.RecentIncidents(2)
	if len(recent) != 2 {
		t.Fatalf("expected 2, got %d", len(recent))
	}
	if recent[0].Title != "New" {
		t.Errorf("expected newest first, got %q", recent[0].Title)
	}
}

func TestGenerateID(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	id1 := store.generateID()
	store.Create("First", SeverityMinor, nil, "", "")
	id2 := store.generateID()

	if id1 == id2 {
		t.Error("expected different IDs")
	}
}

// ──────────────────────────────────────────────
// New tests
// ──────────────────────────────────────────────

func TestSeverityIcon(t *testing.T) {
	tests := []struct {
		severity string
		want     string
	}{
		{SeverityCritical, "🔴"},
		{SeverityMajor, "🟠"},
		{SeverityMinor, "🟡"},
		{"unknown", "⚪"},
		{"", "⚪"},
	}
	for _, tc := range tests {
		got := SeverityIcon(tc.severity)
		if got != tc.want {
			t.Errorf("SeverityIcon(%q) = %q, want %q", tc.severity, got, tc.want)
		}
	}
}

func TestStatusIcon(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{StatusOpen, "🚨"},
		{StatusInvestigating, "🔍"},
		{StatusIdentified, "🎯"},
		{StatusMonitoring, "👀"},
		{StatusResolved, "✅"},
		{"unknown", "❓"},
		{"", "❓"},
	}
	for _, tc := range tests {
		got := StatusIcon(tc.status)
		if got != tc.want {
			t.Errorf("StatusIcon(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestFormatJSON(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc1 := store.Create("DB down", SeverityCritical, []string{"db-svc"}, "alice", "database failure")
	inc2 := store.Create("Slow API", SeverityMinor, nil, "", "")

	result, err := FormatJSON([]Incident{*inc1, *inc2})
	if err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(decoded))
	}
	if decoded[0]["title"] != "DB down" {
		t.Errorf("expected title 'DB down', got %v", decoded[0]["title"])
	}
	if decoded[0]["severity"] != SeverityCritical {
		t.Errorf("expected severity %q, got %v", SeverityCritical, decoded[0]["severity"])
	}
	if decoded[1]["title"] != "Slow API" {
		t.Errorf("expected title 'Slow API', got %v", decoded[1]["title"])
	}
}

func TestFormatJSON_Empty(t *testing.T) {
	result, err := FormatJSON([]Incident{})
	if err != nil {
		t.Fatalf("FormatJSON error: %v", err)
	}
	if strings.TrimSpace(result) != "[]" {
		t.Errorf("expected '[]', got %q", result)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{0, "0m"},
		{59 * time.Minute, "59m"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{3 * time.Hour, "3h0m"},
		{25*time.Hour + 30*time.Minute, "1d1h"},
		{48 * time.Hour, "2d0h"},
		{72*time.Hour + 5*time.Hour, "3d5h"},
	}
	for _, tc := range tests {
		got := formatDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestDefaultStr(t *testing.T) {
	if got := defaultStr("", "fallback"); got != "fallback" {
		t.Errorf("defaultStr('', 'fallback') = %q, want 'fallback'", got)
	}
	if got := defaultStr("value", "fallback"); got != "value" {
		t.Errorf("defaultStr('value', 'fallback') = %q, want 'value'", got)
	}
}

func TestFindByIDPartialMatch(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Partial match test", SeverityMinor, nil, "", "")
	// inc.ID is like "INC-20260309-001"; search by just the suffix "001"
	suffix := inc.ID[len(inc.ID)-3:]

	found := store.FindByID(suffix)
	if found == nil {
		t.Fatalf("expected partial match for suffix %q, got nil", suffix)
	}
	if found.ID != inc.ID {
		t.Errorf("expected ID %q, got %q", inc.ID, found.ID)
	}
}

func TestFindByIDCaseInsensitive(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Case test", SeverityMinor, nil, "", "")

	lower := strings.ToLower(inc.ID)
	found := store.FindByID(lower)
	if found == nil {
		t.Fatalf("expected case-insensitive match for %q, got nil", lower)
	}
	if found.ID != inc.ID {
		t.Errorf("expected ID %q, got %q", inc.ID, found.ID)
	}
}

func TestFindByIDNotFound(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Create("Some incident", SeverityMinor, nil, "", "")

	found := store.FindByID("INC-NOTEXIST")
	if found != nil {
		t.Errorf("expected nil, got %+v", found)
	}
}

func TestRenderListEmpty(t *testing.T) {
	out := captureOutput(t, func() {
		RenderList(nil, "Empty List")
	})
	if !strings.Contains(out, "No incidents found") {
		t.Errorf("expected 'No incidents found' in output, got: %q", out)
	}
}

func TestRenderListNonEmpty(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc1 := store.Create("API down", SeverityCritical, []string{"api"}, "alice", "errors")
	inc2 := store.Create("Slow DB", SeverityMajor, nil, "", "")

	out := captureOutput(t, func() {
		RenderList([]Incident{*inc1, *inc2}, "Active Incidents")
	})
	if !strings.Contains(out, "Active Incidents") {
		t.Errorf("expected title in output, got: %q", out)
	}
	if !strings.Contains(out, "API down") {
		t.Errorf("expected 'API down' in output, got: %q", out)
	}
}

func TestRenderListWithResolvedIncident(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Resolved incident", SeverityMinor, nil, "", "")
	inc.Update(StatusResolved, "All clear", "bob")

	out := captureOutput(t, func() {
		RenderList([]Incident{*inc}, "Recent Incidents")
	})
	if !strings.Contains(out, "resolved") {
		t.Errorf("expected 'resolved' in output, got: %q", out)
	}
}

func TestRenderTimeline(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Timeline test", SeverityCritical, []string{"svc-a", "svc-b"}, "carol", "test description")
	inc.Update(StatusInvestigating, "Investigating now", "carol")
	inc.Update(StatusIdentified, "Root cause found", "dave")

	out := captureOutput(t, func() {
		RenderTimeline(inc)
	})
	if !strings.Contains(out, "Timeline test") {
		t.Errorf("expected title in output, got: %q", out)
	}
	if !strings.Contains(out, "svc-a") {
		t.Errorf("expected service in output, got: %q", out)
	}
	if !strings.Contains(out, "test description") {
		t.Errorf("expected description in output, got: %q", out)
	}
	if !strings.Contains(out, "Investigating now") {
		t.Errorf("expected timeline entry in output, got: %q", out)
	}
}

func TestRenderTimelineResolved(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Resolved TL test", SeverityMajor, nil, "", "")
	inc.Update(StatusResolved, "Fixed it", "eve")

	out := captureOutput(t, func() {
		RenderTimeline(inc)
	})
	if !strings.Contains(out, "Resolved") {
		t.Errorf("expected 'Resolved' in output, got: %q", out)
	}
	if !strings.Contains(out, "Fixed it") {
		t.Errorf("expected timeline message in output, got: %q", out)
	}
}

func TestRenderTimelineNoOptionalFields(t *testing.T) {
	// Incident without commander, services, or description
	inc := &Incident{
		ID:        "INC-20260309-099",
		Title:     "Bare incident",
		Severity:  SeverityMinor,
		Status:    StatusOpen,
		CreatedAt: time.Now(),
		Timeline: []TimelineEntry{
			{
				Timestamp: time.Now(),
				Status:    StatusOpen,
				Message:   "Incident created",
			},
		},
	}

	// Should not panic
	captureOutput(t, func() {
		RenderTimeline(inc)
	})
}

func TestActiveIncidentsAllResolved(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Create("First", SeverityMajor, nil, "", "")
	store.Create("Second", SeverityMinor, nil, "", "")

	// Use FindByID to get valid pointers after all appends are done
	store.FindByID("001").Update(StatusResolved, "Done", "")
	store.FindByID("002").Update(StatusResolved, "Done", "")

	active := store.ActiveIncidents()
	if len(active) != 0 {
		t.Errorf("expected 0 active incidents, got %d", len(active))
	}
}

func TestRecentIncidentsAll(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	store.Incidents = []Incident{
		{ID: "INC-1", Title: "Old", CreatedAt: time.Now().Add(-48 * time.Hour)},
		{ID: "INC-2", Title: "New", CreatedAt: time.Now()},
		{ID: "INC-3", Title: "Mid", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}

	// n=0 should return all (sorted by newest first)
	all := store.RecentIncidents(0)
	if len(all) != 3 {
		t.Fatalf("expected 3 incidents with n=0, got %d", len(all))
	}
	if all[0].Title != "New" {
		t.Errorf("expected newest first, got %q", all[0].Title)
	}

	// n >= len should return all
	allAgain := store.RecentIncidents(100)
	if len(allAgain) != 3 {
		t.Fatalf("expected 3 incidents with n=100, got %d", len(allAgain))
	}
}

func TestLoadCorruptFile(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	incPath := filepath.Join(os.Getenv("HOME"), ".argus", "incidents.yaml")
	// Write corrupt/invalid YAML content
	if err := os.WriteFile(incPath, []byte("incidents: [\x00invalid: {{"), 0644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Error("expected error loading corrupt YAML, got nil")
	}
}

func TestUpdateNonResolvedStatus(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Status test", SeverityMajor, nil, "", "")

	inc.Update(StatusInvestigating, "On it", "frank")
	if inc.ResolvedAt != nil {
		t.Error("ResolvedAt should be nil for non-resolved status")
	}
	if inc.Duration != "" {
		t.Error("Duration should be empty for non-resolved status")
	}

	inc.Update(StatusIdentified, "Found it", "frank")
	if inc.ResolvedAt != nil {
		t.Error("ResolvedAt should be nil for 'identified' status")
	}

	inc.Update(StatusMonitoring, "Watching it", "frank")
	if inc.ResolvedAt != nil {
		t.Error("ResolvedAt should be nil for 'monitoring' status")
	}
}

func TestUpdateAllStatuses(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	statuses := []string{
		StatusOpen,
		StatusInvestigating,
		StatusIdentified,
		StatusMonitoring,
		StatusResolved,
	}

	store := &IncidentStore{}
	inc := store.Create("All statuses test", SeverityMinor, nil, "", "")

	for _, status := range statuses {
		inc.Update(status, "Transitioning to "+status, "tester")
		if inc.Status != status {
			t.Errorf("after Update(%q), status = %q", status, inc.Status)
		}
	}

	// After resolved, ResolvedAt must be set
	if inc.ResolvedAt == nil {
		t.Error("expected ResolvedAt after resolved status")
	}
}

func TestSeverityRank(t *testing.T) {
	tests := []struct {
		severity string
		want     int
	}{
		{SeverityCritical, 3},
		{SeverityMajor, 2},
		{SeverityMinor, 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, tc := range tests {
		got := severityRank(tc.severity)
		if got != tc.want {
			t.Errorf("severityRank(%q) = %d, want %d", tc.severity, got, tc.want)
		}
	}
}

func TestCreateWithLabels(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	store := &IncidentStore{}
	inc := store.Create("Label test", SeverityMinor, nil, "", "")
	// Labels field exists on the struct; set it directly after creation
	inc.Labels = map[string]string{"team": "platform", "env": "prod"}

	if inc.Labels["team"] != "platform" {
		t.Errorf("expected label team=platform, got %q", inc.Labels["team"])
	}
	if inc.Labels["env"] != "prod" {
		t.Errorf("expected label env=prod, got %q", inc.Labels["env"])
	}

	// Persist and reload to confirm labels survive YAML round-trip
	if err := store.Save(); err != nil {
		t.Fatalf("save error: %v", err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded.Incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(loaded.Incidents))
	}
	if loaded.Incidents[0].Labels["team"] != "platform" {
		t.Errorf("label not preserved after round-trip, got %v", loaded.Incidents[0].Labels)
	}
}

func TestLoadNonExistent(t *testing.T) {
	cleanup := setupTestStore(t)
	defer cleanup()

	// No file written — Load should return empty store without error
	store, err := Load()
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
	if len(store.Incidents) != 0 {
		t.Errorf("expected empty incidents, got %d", len(store.Incidents))
	}
}

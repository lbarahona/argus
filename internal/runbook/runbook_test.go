package runbook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{dir: dir}
}

func sampleRunbook() *Runbook {
	return &Runbook{
		Name:        "Test Runbook",
		Description: "A test runbook for unit tests",
		Category:    "testing",
		Severity:    "P3",
		Tags:        []string{"test", "unit"},
		Author:      "test",
		CreatedAt:   time.Now(),
		Steps: []Step{
			{Name: "Step 1", Command: "echo hello", Notes: "First step"},
			{Name: "Step 2", Command: "echo world", Manual: true},
			{Name: "Step 3", Check: "test -f /tmp/ok", Timeout: "10s"},
		},
	}
}

// ──────────────────────────────────────────────
// Store tests
// ──────────────────────────────────────────────

func TestNewStore(t *testing.T) {
	s := NewStore()
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if s.Dir() == "" {
		t.Fatal("expected non-empty directory")
	}
	if !strings.Contains(s.Dir(), ".argus") {
		t.Errorf("expected path to contain .argus, got %s", s.Dir())
	}
}

func TestStoreDir(t *testing.T) {
	s := &Store{dir: "/tmp/test-runbooks"}
	if s.Dir() != "/tmp/test-runbooks" {
		t.Errorf("Dir() = %q, want /tmp/test-runbooks", s.Dir())
	}
}

func TestEnsureDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "runbooks")
	s := &Store{dir: dir}
	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected directory")
	}
}

func TestSaveAndLoad(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if rb.ID == "" {
		t.Fatal("expected ID to be generated")
	}

	loaded, err := s.Load(rb.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Name != rb.Name {
		t.Errorf("Name = %q, want %q", loaded.Name, rb.Name)
	}
	if len(loaded.Steps) != 3 {
		t.Errorf("Steps = %d, want 3", len(loaded.Steps))
	}
	if loaded.Steps[1].Manual != true {
		t.Error("Step 2 should be manual")
	}
}

func TestSavePreservesExistingID(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	rb.ID = "my-custom-id"

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if rb.ID != "my-custom-id" {
		t.Errorf("ID changed to %q, want my-custom-id", rb.ID)
	}

	loaded, err := s.Load("my-custom-id")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "Test Runbook" {
		t.Errorf("Name = %q, want Test Runbook", loaded.Name)
	}
}

func TestSaveUpdatesTimestamp(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	before := time.Now().Add(-time.Second)

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if rb.UpdatedAt.Before(before) {
		t.Error("UpdatedAt not updated on save")
	}
}

func TestPartialIDMatch(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	rb.ID = "test-runbook-abc123"

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load("test-run")
	if err != nil {
		t.Fatalf("partial Load: %v", err)
	}
	if loaded.ID != "test-runbook-abc123" {
		t.Errorf("ID = %q, want test-runbook-abc123", loaded.ID)
	}
}

func TestPartialIDMatchCaseInsensitive(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	rb.ID = "test-runbook-abc123"

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load("TEST-RUN")
	if err != nil {
		t.Fatalf("case-insensitive Load: %v", err)
	}
	if loaded.ID != "test-runbook-abc123" {
		t.Errorf("ID = %q, want test-runbook-abc123", loaded.ID)
	}
}

func TestAmbiguousPartialID(t *testing.T) {
	s := tempStore(t)

	for _, id := range []string{"deploy-api-abc", "deploy-web-def"} {
		rb := sampleRunbook()
		rb.ID = id
		if err := s.Save(rb); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	_, err := s.Load("deploy")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestLoadNotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent runbook")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestLoadBadYAML(t *testing.T) {
	s := tempStore(t)
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	// Write invalid YAML
	bad := filepath.Join(s.dir, "bad-runbook.yaml")
	os.WriteFile(bad, []byte("{{{{not yaml at all"), 0644)

	_, err := s.Load("bad-runbook")
	if err == nil {
		t.Fatal("expected error for bad YAML")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("expected parsing error, got: %v", err)
	}
}

func TestLoadFromNonexistentDir(t *testing.T) {
	s := &Store{dir: "/tmp/nonexistent-argus-dir-" + time.Now().Format("20060102150405")}
	_, err := s.Load("anything")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestList(t *testing.T) {
	s := tempStore(t)

	for _, name := range []string{"Alpha Runbook", "Beta Runbook", "Charlie Runbook"} {
		rb := sampleRunbook()
		rb.Name = name
		if err := s.Save(rb); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List = %d, want 3", len(list))
	}
	// Should be sorted by name
	if list[0].Name != "Alpha Runbook" {
		t.Errorf("first = %q, want Alpha Runbook", list[0].Name)
	}
	if list[2].Name != "Charlie Runbook" {
		t.Errorf("last = %q, want Charlie Runbook", list[2].Name)
	}
}

func TestEmptyList(t *testing.T) {
	s := tempStore(t)
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list != nil {
		t.Errorf("expected nil, got %d items", len(list))
	}
}

func TestListNonexistentDir(t *testing.T) {
	s := &Store{dir: "/tmp/nonexistent-argus-list-" + time.Now().Format("20060102150405")}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List on nonexistent dir should not error: %v", err)
	}
	if list != nil {
		t.Errorf("expected nil list, got %d", len(list))
	}
}

func TestListSkipsDirectories(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}
	// Create a subdirectory (should be skipped)
	os.MkdirAll(filepath.Join(s.dir, "subdir"), 0755)
	// Create a non-yaml file (should be skipped)
	os.WriteFile(filepath.Join(s.dir, "readme.txt"), []byte("hi"), 0644)

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List = %d, want 1 (should skip non-yaml files and dirs)", len(list))
	}
}

func TestListSkipsBadYAML(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}
	// Write a bad yaml file (should be skipped silently)
	os.WriteFile(filepath.Join(s.dir, "bad.yaml"), []byte("{{{{invalid"), 0644)

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List = %d, want 1 (should skip bad YAML)", len(list))
	}
}

func TestDelete(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	rb.ID = "to-delete-abc123"

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Delete("to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := s.Load("to-delete-abc123")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDeleteNonexistent(t *testing.T) {
	s := tempStore(t)
	err := s.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error for deleting nonexistent runbook")
	}
}

func TestSearch(t *testing.T) {
	s := tempStore(t)

	rbs := []struct {
		name        string
		description string
		category    string
		tags        []string
	}{
		{"Pod Crash Recovery", "Fix crashing pods", "kubernetes", []string{"pods", "crash"}},
		{"Database Backup", "Backup postgres DB", "database", []string{"postgres", "backup"}},
		{"Deploy Rollback", "Rollback deployments", "kubernetes", []string{"deploy", "rollback"}},
	}

	for _, r := range rbs {
		rb := sampleRunbook()
		rb.Name = r.name
		rb.Description = r.description
		rb.Category = r.category
		rb.Tags = r.tags
		if err := s.Save(rb); err != nil {
			t.Fatalf("Save %s: %v", r.name, err)
		}
	}

	tests := []struct {
		query string
		want  int
	}{
		{"kubernetes", 2},
		{"database", 1},
		{"crash", 1},  // matches tag "crash" on Pod Crash Recovery
		{"rollback", 1}, // matches tag "rollback" on Deploy Rollback
		{"postgres", 1},
		{"nonexistent", 0},
		{"pod", 1},
		{"backup", 1},
		{"deploy", 1},
	}

	for _, tt := range tests {
		results, err := s.Search(tt.query)
		if err != nil {
			t.Fatalf("Search %q: %v", tt.query, err)
		}
		if len(results) != tt.want {
			names := make([]string, len(results))
			for i, r := range results {
				names[i] = r.Name
			}
			t.Errorf("Search(%q) = %d results %v, want %d", tt.query, len(results), names, tt.want)
		}
	}
}

func TestSearchEmptyStore(t *testing.T) {
	s := tempStore(t)
	results, err := s.Search("anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestInitSamples(t *testing.T) {
	s := tempStore(t)

	if err := InitSamples(s); err != nil {
		t.Fatalf("InitSamples: %v", err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 5 {
		t.Errorf("InitSamples created %d runbooks, want 5", len(list))
	}

	// Verify files exist
	entries, _ := os.ReadDir(s.dir)
	yamlCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".yaml" {
			yamlCount++
		}
	}
	if yamlCount != 5 {
		t.Errorf("found %d yaml files, want 5", yamlCount)
	}

	// Verify sample content
	categories := map[string]bool{}
	for _, rb := range list {
		categories[rb.Category] = true
		if rb.Author != "argus" {
			t.Errorf("sample %q author = %q, want argus", rb.Name, rb.Author)
		}
		if len(rb.Steps) == 0 {
			t.Errorf("sample %q has no steps", rb.Name)
		}
	}
	for _, cat := range []string{"kubernetes", "incident-response", "maintenance", "database"} {
		if !categories[cat] {
			t.Errorf("missing sample category %q", cat)
		}
	}
}

func TestGenerateID(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"High Error Rate Response"},
		{"Pod CrashLoopBackOff"},
		{"Simple"},
		{"With Special Ch@r$!"},
		{"A Very Long Name That Should Be Truncated To Forty Characters At Most Plus Hash"},
	}

	for _, tt := range tests {
		id := generateID(tt.name)
		if id == "" {
			t.Fatalf("generateID(%q) returned empty", tt.name)
		}
		// Should be lowercase with hyphens and hex suffix
		for _, c := range id {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				t.Errorf("ID %q from name %q contains invalid char %q", id, tt.name, string(c))
			}
		}
		// ID slug part should be <= 40 chars (before the hash suffix)
		parts := strings.Split(id, "-")
		// Last part is the 6-char hex hash
		slugParts := parts[:len(parts)-1]
		slug := strings.Join(slugParts, "-")
		if len(slug) > 40 {
			t.Errorf("slug part %q (%d chars) exceeds 40", slug, len(slug))
		}
	}

	// IDs should be unique (different hashes due to timestamp)
	id1 := generateID("test")
	time.Sleep(time.Millisecond)
	id2 := generateID("test")
	if id1 == id2 {
		t.Errorf("expected different IDs, both got %q", id1)
	}
}

func TestMatchesQuery(t *testing.T) {
	rb := &Runbook{
		Name:        "Pod Recovery",
		Description: "Recover crashed pods",
		Category:    "kubernetes",
		Tags:        []string{"pods", "crash"},
	}

	tests := []struct {
		query string
		want  bool
	}{
		{"pod", true},       // name match
		{"recover", true},   // description match
		{"kubernetes", true}, // category match
		{"crash", true},     // tag match
		{"pods", true},      // tag match
		{"missing", false},
	}

	for _, tt := range tests {
		got := matchesQuery(rb, tt.query)
		if got != tt.want {
			t.Errorf("matchesQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────
// Format tests
// ──────────────────────────────────────────────

func TestSeverityStyle(t *testing.T) {
	tests := []struct {
		sev  string
		name string
	}{
		{"P1", "P1"},
		{"P2", "P2"},
		{"P3", "P3"},
		{"P4", "P4"},
		{"p1", "P1 lowercase"},
		{"", "empty"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		style := severityStyle(tt.sev)
		// Just verify it doesn't panic and returns a style
		rendered := style.Render(tt.sev)
		if rendered == "" && tt.sev != "" {
			t.Errorf("severityStyle(%q) rendered empty", tt.sev)
		}
	}
}

func TestPrintListEmpty(t *testing.T) {
	var buf bytes.Buffer
	PrintList(&buf, nil)
	output := buf.String()
	if !strings.Contains(output, "No runbooks found") {
		t.Errorf("expected 'No runbooks found', got: %s", output)
	}
}

func TestPrintListEmptySlice(t *testing.T) {
	var buf bytes.Buffer
	PrintList(&buf, []*Runbook{})
	output := buf.String()
	if !strings.Contains(output, "No runbooks found") {
		t.Errorf("expected 'No runbooks found', got: %s", output)
	}
}

func TestPrintListWithRunbooks(t *testing.T) {
	var buf bytes.Buffer

	runbooks := []*Runbook{
		{
			ID:          "rb-1",
			Name:        "Pod Recovery",
			Description: "Recover crashed pods",
			Category:    "kubernetes",
			Severity:    "P1",
			Tags:        []string{"pods", "k8s"},
			Steps:       []Step{{Name: "step1"}, {Name: "step2"}},
		},
		{
			ID:    "rb-2",
			Name:  "Simple Runbook",
			Steps: []Step{{Name: "step1"}},
		},
	}

	PrintList(&buf, runbooks)
	output := buf.String()

	// Should show count
	if !strings.Contains(output, "Runbooks (2)") {
		t.Errorf("expected 'Runbooks (2)', got: %s", output)
	}
	// Should show names
	if !strings.Contains(output, "Pod Recovery") {
		t.Error("expected Pod Recovery in output")
	}
	if !strings.Contains(output, "Simple Runbook") {
		t.Error("expected Simple Runbook in output")
	}
	// Should show description
	if !strings.Contains(output, "Recover crashed pods") {
		t.Error("expected description in output")
	}
	// Should show ID
	if !strings.Contains(output, "rb-1") {
		t.Error("expected ID in output")
	}
	// Should show step count
	if !strings.Contains(output, "2 steps") {
		t.Error("expected step count in output")
	}
	// Should show tags
	if !strings.Contains(output, "pods, k8s") {
		t.Error("expected tags in output")
	}
}

func TestPrintListNoOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	runbooks := []*Runbook{
		{
			ID:   "rb-no-extras",
			Name: "Minimal",
			Steps: []Step{{Name: "s1"}},
		},
	}
	PrintList(&buf, runbooks)
	output := buf.String()
	if !strings.Contains(output, "Minimal") {
		t.Error("expected name in output")
	}
	// Should not have category brackets or severity
	if strings.Contains(output, "P1") || strings.Contains(output, "P2") {
		t.Error("should not show severity for runbook without one")
	}
}

func TestPrintShow(t *testing.T) {
	var buf bytes.Buffer
	rb := &Runbook{
		ID:          "show-test",
		Name:        "Full Runbook",
		Description: "A comprehensive runbook",
		Category:    "database",
		Severity:    "P2",
		Tags:        []string{"postgres", "backup"},
		Author:      "tester",
		OnFailure:   "escalate",
		Steps: []Step{
			{
				Name:        "Check connections",
				Description: "Verify DB connections",
				Command:     "psql -c 'SELECT 1'",
				Check:       "echo ok",
				Rollback:    "echo rollback",
				Timeout:     "30s",
				Notes:       "Important step",
				Manual:      false,
			},
			{
				Name:   "Manual step",
				Manual: true,
				Notes:  "Do this manually",
			},
			{
				Name:    "Simple command",
				Command: "echo done",
			},
		},
	}

	PrintShow(&buf, rb)
	output := buf.String()

	checks := []string{
		"Full Runbook",
		"A comprehensive runbook",
		"database",
		"postgres, backup",
		"tester",
		"escalate",
		"show-test",
		"Steps (3)",
		"Check connections",
		"Verify DB connections",
		"psql -c 'SELECT 1'",
		"echo ok",
		"echo rollback",
		"30s",
		"Important step",
		"MANUAL",
		"Manual step",
		"echo done",
	}

	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("expected %q in output, got:\n%s", check, output)
		}
	}
}

func TestPrintShowMinimal(t *testing.T) {
	var buf bytes.Buffer
	rb := &Runbook{
		ID:   "minimal",
		Name: "Minimal Runbook",
		Steps: []Step{
			{Name: "Only step", Command: "echo hello"},
		},
	}

	PrintShow(&buf, rb)
	output := buf.String()

	if !strings.Contains(output, "Minimal Runbook") {
		t.Error("expected name")
	}
	if !strings.Contains(output, "Steps (1)") {
		t.Error("expected step count")
	}
	// Optional fields should not appear
	if strings.Contains(output, "Category:") {
		t.Error("should not show Category for empty category")
	}
	if strings.Contains(output, "Author:") {
		t.Error("should not show Author for empty author")
	}
	if strings.Contains(output, "On Failure:") {
		t.Error("should not show On Failure for empty on_failure")
	}
}

func TestPrintRunLog(t *testing.T) {
	start := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		log    *RunLog
		checks []string
	}{
		{
			name: "completed run",
			log: &RunLog{
				RunbookID:   "rb-1",
				RunbookName: "Pod Recovery",
				StartedAt:   start,
				CompletedAt: start.Add(5 * time.Minute),
				Status:      "completed",
				StepResults: []StepResult{
					{StepName: "Step 1", Status: "passed"},
					{StepName: "Step 2", Status: "passed"},
				},
			},
			checks: []string{"✅", "Pod Recovery", "2026-03-10", "Step 1", "passed"},
		},
		{
			name: "failed run",
			log: &RunLog{
				RunbookID:   "rb-2",
				RunbookName: "Deploy Check",
				StartedAt:   start,
				CompletedAt: start.Add(2 * time.Minute),
				Status:      "failed",
				StepResults: []StepResult{
					{StepName: "Step 1", Status: "passed"},
					{StepName: "Step 2", Status: "failed", Error: "connection timeout"},
				},
			},
			checks: []string{"❌", "Deploy Check", "failed", "connection timeout"},
		},
		{
			name: "aborted run",
			log: &RunLog{
				RunbookID:   "rb-3",
				RunbookName: "Aborted Task",
				StartedAt:   start,
				Status:      "aborted",
				StepResults: []StepResult{
					{StepName: "Step 1", Status: "skipped"},
				},
			},
			checks: []string{"⏹️", "Aborted Task", "skipped"},
		},
		{
			name: "running",
			log: &RunLog{
				RunbookID:   "rb-4",
				RunbookName: "In Progress",
				StartedAt:   start,
				Status:      "running",
				StepResults: []StepResult{
					{StepName: "Step 1", Status: "passed"},
					{StepName: "Step 2", Status: "pending"},
				},
			},
			checks: []string{"🔄", "In Progress"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			PrintRunLog(&buf, tt.log)
			output := buf.String()

			for _, check := range tt.checks {
				if !strings.Contains(output, check) {
					t.Errorf("expected %q in output, got:\n%s", check, output)
				}
			}
		})
	}
}

func TestPrintRunLogWithDuration(t *testing.T) {
	start := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer

	log := &RunLog{
		RunbookID:   "rb-dur",
		RunbookName: "Duration Test",
		StartedAt:   start,
		CompletedAt: start.Add(3*time.Minute + 30*time.Second),
		Status:      "completed",
		StepResults: []StepResult{},
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	if !strings.Contains(output, "3m30s") {
		t.Errorf("expected duration in output, got:\n%s", output)
	}
}

func TestFormatJSON(t *testing.T) {
	rb := sampleRunbook()
	rb.ID = "json-test"

	result, err := FormatJSON(rb)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	if !strings.Contains(result, "json-test") {
		t.Error("expected ID in JSON output")
	}
	if !strings.Contains(result, "Test Runbook") {
		t.Error("expected name in JSON output")
	}

	// Should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("JSON output not valid: %v", err)
	}
}

func TestFormatJSONList(t *testing.T) {
	runbooks := []*Runbook{
		{ID: "a", Name: "Alpha"},
		{ID: "b", Name: "Beta"},
	}

	result, err := FormatJSON(runbooks)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("JSON output not valid array: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 items, got %d", len(parsed))
	}
}

func TestFormatJSONRunLog(t *testing.T) {
	log := &RunLog{
		RunbookID:   "rb-1",
		RunbookName: "Test",
		Status:      "completed",
		StepResults: []StepResult{
			{StepName: "s1", Status: "passed"},
		},
	}

	result, err := FormatJSON(log)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// FormatJSON uses json.Marshal which respects json tags; RunLog has yaml tags only
	// so the field name in JSON is the Go struct field name
	if parsed["Status"] != "completed" && parsed["status"] != "completed" {
		t.Errorf("expected status=completed, got keys: %v", parsed)
	}
}

// ──────────────────────────────────────────────
// Edge cases and integration
// ──────────────────────────────────────────────

func TestSaveLoadRoundTrip(t *testing.T) {
	s := tempStore(t)

	original := &Runbook{
		Name:        "Round Trip Test",
		Description: "Testing full round trip",
		Category:    "testing",
		Severity:    "P1",
		Tags:        []string{"test", "roundtrip"},
		Author:      "author",
		OnFailure:   "rollback",
		Steps: []Step{
			{
				Name:        "Full Step",
				Description: "Has everything",
				Command:     "echo full",
				Check:       "echo check",
				Rollback:    "echo rollback",
				Manual:      true,
				Timeout:     "5m",
				Notes:       "Full notes",
			},
		},
	}

	if err := s.Save(original); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load(original.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all fields
	if loaded.Name != original.Name {
		t.Errorf("Name mismatch")
	}
	if loaded.Description != original.Description {
		t.Errorf("Description mismatch")
	}
	if loaded.Category != original.Category {
		t.Errorf("Category mismatch")
	}
	if loaded.Severity != original.Severity {
		t.Errorf("Severity mismatch")
	}
	if loaded.Author != original.Author {
		t.Errorf("Author mismatch")
	}
	if loaded.OnFailure != original.OnFailure {
		t.Errorf("OnFailure mismatch")
	}
	if len(loaded.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(loaded.Tags))
	}

	step := loaded.Steps[0]
	if step.Name != "Full Step" || step.Description != "Has everything" {
		t.Error("Step basic fields mismatch")
	}
	if step.Command != "echo full" || step.Check != "echo check" {
		t.Error("Step command fields mismatch")
	}
	if step.Rollback != "echo rollback" || step.Timeout != "5m" {
		t.Error("Step rollback/timeout mismatch")
	}
	if !step.Manual || step.Notes != "Full notes" {
		t.Error("Step manual/notes mismatch")
	}
}

func TestListUnreadableFile(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	// Create an unreadable file
	unreadable := filepath.Join(s.dir, "unreadable.yaml")
	os.WriteFile(unreadable, []byte("test"), 0644)
	os.Chmod(unreadable, 0000)
	defer os.Chmod(unreadable, 0644)

	// List should still work, skipping unreadable files
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 (skipping unreadable), got %d", len(list))
	}
}

func TestSearchByDescription(t *testing.T) {
	s := tempStore(t)
	rb := sampleRunbook()
	rb.Name = "Generic Name"
	rb.Description = "Handles memory pressure on nodes"
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("memory")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result searching description, got %d", len(results))
	}
}

package incident

import (
	"os"
	"path/filepath"
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

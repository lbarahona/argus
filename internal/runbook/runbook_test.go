package runbook

import (
	"os"
	"path/filepath"
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

func TestSearch(t *testing.T) {
	s := tempStore(t)

	rbs := []struct {
		name     string
		category string
		tags     []string
	}{
		{"Pod Crash Recovery", "kubernetes", []string{"pods", "crash"}},
		{"Database Backup", "database", []string{"postgres", "backup"}},
		{"Deploy Rollback", "kubernetes", []string{"deploy", "rollback"}},
	}

	for _, r := range rbs {
		rb := sampleRunbook()
		rb.Name = r.name
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
		{"crash", 1},
		{"rollback", 1},
		{"nonexistent", 0},
	}

	for _, tt := range tests {
		results, err := s.Search(tt.query)
		if err != nil {
			t.Fatalf("Search %q: %v", tt.query, err)
		}
		if len(results) != tt.want {
			t.Errorf("Search(%q) = %d results, want %d", tt.query, len(results), tt.want)
		}
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

func TestLoadNotFound(t *testing.T) {
	s := tempStore(t)
	_, err := s.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent runbook")
	}
}

func TestGenerateID(t *testing.T) {
	id := generateID("High Error Rate Response")
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
	// Should be lowercase with hyphens
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			t.Errorf("ID contains invalid char %q: %s", string(c), id)
		}
	}
}

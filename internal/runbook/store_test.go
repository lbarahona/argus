package runbook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	s := NewStore()
	if s == nil {
		t.Fatal("NewStore returned nil")
	}
	if s.dir == "" {
		t.Fatal("NewStore dir is empty")
	}
	if !strings.Contains(s.dir, ".argus") {
		t.Errorf("dir = %q, expected to contain .argus", s.dir)
	}
	if !strings.HasSuffix(s.dir, "runbooks") {
		t.Errorf("dir = %q, expected to end with runbooks", s.dir)
	}
}

func TestStoreDir(t *testing.T) {
	s := &Store{dir: "/tmp/test-runbooks"}
	if s.Dir() != "/tmp/test-runbooks" {
		t.Errorf("Dir() = %q, want /tmp/test-runbooks", s.Dir())
	}
}

func TestSave_GeneratesID(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		Name:  "Auto ID Test",
		Steps: []Step{{Name: "s1"}},
	}

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if rb.ID == "" {
		t.Fatal("expected ID to be auto-generated")
	}
	if !strings.HasPrefix(rb.ID, "auto-id-test-") {
		t.Errorf("ID = %q, expected prefix 'auto-id-test-'", rb.ID)
	}
}

func TestSave_PreservesExistingID(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		ID:    "my-custom-id",
		Name:  "Custom ID Test",
		Steps: []Step{{Name: "s1"}},
	}

	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if rb.ID != "my-custom-id" {
		t.Errorf("ID = %q, want my-custom-id", rb.ID)
	}
}

func TestSave_SetsUpdatedAt(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		Name:  "Timestamp Test",
		Steps: []Step{{Name: "s1"}},
	}

	before := time.Now()
	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}
	after := time.Now()

	if rb.UpdatedAt.Before(before) || rb.UpdatedAt.After(after) {
		t.Errorf("UpdatedAt = %v, expected between %v and %v", rb.UpdatedAt, before, after)
	}
}

func TestLoad_AmbiguousID(t *testing.T) {
	s := tempStore(t)

	// Create two runbooks with similar IDs
	rb1 := &Runbook{ID: "deploy-frontend-abc", Name: "Deploy Frontend", Steps: []Step{{Name: "s1"}}}
	rb2 := &Runbook{ID: "deploy-backend-def", Name: "Deploy Backend", Steps: []Step{{Name: "s1"}}}

	if err := s.Save(rb1); err != nil {
		t.Fatalf("Save rb1: %v", err)
	}
	if err := s.Save(rb2); err != nil {
		t.Fatalf("Save rb2: %v", err)
	}

	_, err := s.Load("deploy")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %q, expected 'ambiguous'", err.Error())
	}
}

func TestLoad_PartialCaseInsensitive(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{ID: "MyRunbook-ABC", Name: "My Runbook", Steps: []Step{{Name: "s1"}}}
	if err := s.Save(rb); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Load("myrunbook")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != "MyRunbook-ABC" {
		t.Errorf("ID = %q, want MyRunbook-ABC", loaded.ID)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	s := tempStore(t)
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	// Write invalid YAML
	path := filepath.Join(s.dir, "bad-yaml.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml:::"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := s.Load("bad-yaml")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parsing runbook") {
		t.Errorf("error = %q, expected 'parsing runbook'", err.Error())
	}
}

func TestLoad_PathTraversal(t *testing.T) {
	s := tempStore(t)

	for _, id := range []string{"../evil", "..\\evil", "sub/evil", "sub\\evil"} {
		_, err := s.Load(id)
		if err == nil {
			t.Fatalf("Load(%q): expected error, got nil", id)
		}
		if !strings.Contains(err.Error(), "invalid runbook ID") {
			t.Errorf("Load(%q) error = %q, expected 'invalid runbook ID'", id, err.Error())
		}
	}
}

func TestDelete_RemovesFileByResolvedName(t *testing.T) {
	s := tempStore(t)
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	// A runbook file on disk whose internal `id:` field does not match its filename.
	content := "id: totally-different-id\nname: Mismatched Runbook\nsteps:\n  - name: s1\n"
	if err := os.WriteFile(filepath.Join(s.dir, "on-disk-name.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// A decoy file matching the internal ID — Delete must NOT touch this file.
	decoy := &Runbook{ID: "totally-different-id", Name: "Decoy", Steps: []Step{{Name: "s1"}}}
	if err := s.Save(decoy); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete("on-disk-name"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.dir, "on-disk-name.yaml")); !os.IsNotExist(err) {
		t.Error("expected on-disk-name.yaml to be deleted")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "totally-different-id.yaml")); err != nil {
		t.Error("expected decoy totally-different-id.yaml to remain untouched")
	}
}

func TestList_SkipsDirectories(t *testing.T) {
	s := tempStore(t)
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	// Create a runbook
	rb := &Runbook{ID: "real-rb", Name: "Real", Steps: []Step{{Name: "s1"}}}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory
	if err := os.MkdirAll(filepath.Join(s.dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create a non-yaml file
	if err := os.WriteFile(filepath.Join(s.dir, "notes.txt"), []byte("not yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List returned %d items, want 1", len(list))
	}
}

func TestList_SkipsInvalidYAML(t *testing.T) {
	s := tempStore(t)
	if err := s.EnsureDir(); err != nil {
		t.Fatal(err)
	}

	// Write a valid runbook
	rb := &Runbook{ID: "valid-rb", Name: "Valid", Steps: []Step{{Name: "s1"}}}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	// Write invalid YAML with .yaml extension
	path := filepath.Join(s.dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("{{invalid"), 0644); err != nil {
		t.Fatal(err)
	}

	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Should skip the broken one and still return the valid one
	if len(list) != 1 {
		t.Errorf("List returned %d items, want 1", len(list))
	}
}

func TestList_SortedByName(t *testing.T) {
	s := tempStore(t)

	names := []string{"Zulu", "Alpha", "Mike"}
	for _, name := range names {
		rb := &Runbook{Name: name, Steps: []Step{{Name: "s1"}}}
		if err := s.Save(rb); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}

	if list[0].Name != "Alpha" {
		t.Errorf("first = %q, want Alpha", list[0].Name)
	}
	if list[1].Name != "Mike" {
		t.Errorf("second = %q, want Mike", list[1].Name)
	}
	if list[2].Name != "Zulu" {
		t.Errorf("third = %q, want Zulu", list[2].Name)
	}
}

func TestDelete_NotFound(t *testing.T) {
	s := tempStore(t)
	err := s.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent delete")
	}
}

func TestSearch_ByDescription(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		Name:        "Generic Name",
		Description: "Handles database failover scenarios",
		Steps:       []Step{{Name: "s1"}},
	}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("failover")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'failover', got %d", len(results))
	}
}

func TestSearch_ByTag(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		Name:  "Plain Name",
		Tags:  []string{"prometheus", "alerting"},
		Steps: []Step{{Name: "s1"}},
	}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("prometheus")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for tag search, got %d", len(results))
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		Name:  "Kubernetes Pod Recovery",
		Steps: []Step{{Name: "s1"}},
	}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("KUBERNETES")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for case-insensitive search, got %d", len(results))
	}
}

func TestSearch_NoResults(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{Name: "Something", Steps: []Step{{Name: "s1"}}}
	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	results, err := s.Search("nonexistent-query")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearch_EmptyStore(t *testing.T) {
	s := tempStore(t)
	results, err := s.Search("anything")
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Errorf("expected nil results from empty store, got %d", len(results))
	}
}

func TestGenerateID_LongName(t *testing.T) {
	longName := strings.Repeat("very-long-name-", 10) // 150 chars
	id := generateID(longName)

	// Slug part should be truncated to 40 chars + hyphen + 6 hex chars
	parts := strings.Split(id, "-")
	if len(id) > 50 {
		t.Errorf("ID too long: %d chars, %q", len(id), id)
	}
	if len(parts) < 2 {
		t.Errorf("ID should have hash suffix: %q", id)
	}
}

func TestGenerateID_SpecialChars(t *testing.T) {
	id := generateID("Pod CrashLoop: Fix (v2) @urgent!")
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			t.Errorf("ID contains invalid char %q: %s", string(c), id)
		}
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	// Same name should produce different IDs due to timestamp
	id1 := generateID("Same Name")
	time.Sleep(time.Millisecond) // ensure different nanos
	id2 := generateID("Same Name")

	if id1 == id2 {
		t.Errorf("expected unique IDs, both got %q", id1)
	}
}

func TestEnsureDir_CreatesNestedDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "c", "runbooks")
	s := &Store{dir: dir}

	if err := s.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestMatchesQuery(t *testing.T) {
	rb := &Runbook{
		Name:        "Pod Recovery",
		Description: "Recover crashed pods in production",
		Category:    "kubernetes",
		Tags:        []string{"pods", "k8s", "troubleshooting"},
	}

	tests := []struct {
		query string
		want  bool
	}{
		{"pod", true},          // matches name
		{"production", true},   // matches description
		{"kubernetes", true},   // matches category
		{"troubleshooting", true}, // matches tag
		{"k8s", true},          // matches tag
		{"nonexistent", false}, // no match
		{"recovery", true},     // partial name match
	}

	for _, tt := range tests {
		got := matchesQuery(rb, tt.query)
		if got != tt.want {
			t.Errorf("matchesQuery(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestSaveAndLoad_Roundtrip(t *testing.T) {
	s := tempStore(t)
	rb := &Runbook{
		ID:          "roundtrip-test",
		Name:        "Roundtrip Test",
		Description: "Test full serialization roundtrip",
		Category:    "testing",
		Severity:    "P1",
		Tags:        []string{"test", "roundtrip"},
		Author:      "unit-test",
		OnFailure:   "rollback",
		Steps: []Step{
			{
				Name:        "Step with everything",
				Description: "Full step",
				Command:     "echo test",
				Check:       "test -f output",
				Rollback:    "rm output",
				Manual:      true,
				Timeout:     "1m",
				Notes:       "Test notes",
			},
		},
	}

	if err := s.Save(rb); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load("roundtrip-test")
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Name != rb.Name {
		t.Errorf("Name mismatch: %q vs %q", loaded.Name, rb.Name)
	}
	if loaded.Description != rb.Description {
		t.Errorf("Description mismatch")
	}
	if loaded.Category != rb.Category {
		t.Errorf("Category mismatch")
	}
	if loaded.Severity != rb.Severity {
		t.Errorf("Severity mismatch")
	}
	if loaded.Author != rb.Author {
		t.Errorf("Author mismatch")
	}
	if loaded.OnFailure != rb.OnFailure {
		t.Errorf("OnFailure mismatch")
	}
	if len(loaded.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(loaded.Tags))
	}

	step := loaded.Steps[0]
	if step.Command != "echo test" {
		t.Errorf("Step Command mismatch")
	}
	if step.Check != "test -f output" {
		t.Errorf("Step Check mismatch")
	}
	if step.Rollback != "rm output" {
		t.Errorf("Step Rollback mismatch")
	}
	if !step.Manual {
		t.Error("Step Manual should be true")
	}
	if step.Timeout != "1m" {
		t.Errorf("Step Timeout mismatch")
	}
	if step.Notes != "Test notes" {
		t.Errorf("Step Notes mismatch")
	}
}

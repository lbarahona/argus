package runbook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSeverityStyle(t *testing.T) {
	tests := []struct {
		severity string
	}{
		{"P1"},
		{"P2"},
		{"P3"},
		{"P4"},
		{"p1"},  // lowercase
		{""},    // empty defaults to P4
		{"P99"}, // unknown defaults to P4
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			style := severityStyle(tt.severity)
			// Just verify it returns a valid style and can render without panic
			result := style.Render(tt.severity)
			if result == "" && tt.severity != "" {
				t.Errorf("severityStyle(%q) rendered empty string", tt.severity)
			}
		})
	}
}

func TestPrintList_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintList(&buf, nil)
	output := buf.String()
	if !strings.Contains(output, "No runbooks found") {
		t.Errorf("expected 'No runbooks found', got: %s", output)
	}
	if !strings.Contains(output, "argus runbook init") {
		t.Errorf("expected init hint, got: %s", output)
	}
}

func TestPrintList_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	PrintList(&buf, []*Runbook{})
	output := buf.String()
	if !strings.Contains(output, "No runbooks found") {
		t.Errorf("expected 'No runbooks found' for empty slice, got: %s", output)
	}
}

func TestPrintList_SingleRunbook(t *testing.T) {
	var buf bytes.Buffer
	rbs := []*Runbook{
		{
			ID:          "test-rb-123",
			Name:        "Test Runbook",
			Description: "A test runbook",
			Category:    "testing",
			Severity:    "P2",
			Tags:        []string{"test", "unit"},
			Steps: []Step{
				{Name: "Step 1", Command: "echo hello"},
				{Name: "Step 2", Command: "echo world"},
			},
		},
	}
	PrintList(&buf, rbs)
	output := buf.String()

	if !strings.Contains(output, "Runbooks (1)") {
		t.Errorf("expected count header, got: %s", output)
	}
	if !strings.Contains(output, "Test Runbook") {
		t.Errorf("expected runbook name, got: %s", output)
	}
	if !strings.Contains(output, "A test runbook") {
		t.Errorf("expected description, got: %s", output)
	}
	if !strings.Contains(output, "test-rb-123") {
		t.Errorf("expected ID, got: %s", output)
	}
	if !strings.Contains(output, "2 steps") {
		t.Errorf("expected step count, got: %s", output)
	}
	if !strings.Contains(output, "test, unit") {
		t.Errorf("expected tags, got: %s", output)
	}
}

func TestPrintList_NoOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	rbs := []*Runbook{
		{
			ID:   "minimal-123",
			Name: "Minimal Runbook",
			Steps: []Step{
				{Name: "Step 1"},
			},
		},
	}
	PrintList(&buf, rbs)
	output := buf.String()

	if !strings.Contains(output, "Minimal Runbook") {
		t.Errorf("expected name, got: %s", output)
	}
	// No severity, category, or tags should still render cleanly
	if !strings.Contains(output, "1 steps") {
		t.Errorf("expected step count, got: %s", output)
	}
}

func TestPrintList_MultipleRunbooks(t *testing.T) {
	var buf bytes.Buffer
	rbs := []*Runbook{
		{ID: "alpha-1", Name: "Alpha", Severity: "P1", Steps: []Step{{Name: "s1"}}},
		{ID: "beta-2", Name: "Beta", Category: "infra", Steps: []Step{{Name: "s1"}, {Name: "s2"}}},
		{ID: "gamma-3", Name: "Gamma", Severity: "P3", Category: "db", Tags: []string{"sql"}, Description: "Gamma desc", Steps: []Step{{Name: "s1"}}},
	}
	PrintList(&buf, rbs)
	output := buf.String()

	if !strings.Contains(output, "Runbooks (3)") {
		t.Errorf("expected count 3, got: %s", output)
	}
	if !strings.Contains(output, "Alpha") {
		t.Error("missing Alpha")
	}
	if !strings.Contains(output, "Beta") {
		t.Error("missing Beta")
	}
	if !strings.Contains(output, "Gamma") {
		t.Error("missing Gamma")
	}
}

func TestPrintShow_FullRunbook(t *testing.T) {
	var buf bytes.Buffer
	rb := &Runbook{
		ID:          "show-test-123",
		Name:        "Show Test Runbook",
		Description: "Detailed test for PrintShow",
		Category:    "testing",
		Severity:    "P1",
		Tags:        []string{"test", "show"},
		Author:      "tester",
		OnFailure:   "escalate",
		Steps: []Step{
			{
				Name:        "Automated step",
				Description: "Does something automatically",
				Command:     "echo hello",
				Check:       "test -f /tmp/ok",
				Timeout:     "30s",
				Notes:       "Important note",
			},
			{
				Name:     "Manual step",
				Manual:   true,
				Rollback: "echo rollback",
				Notes:    "Requires human",
			},
			{
				Name:    "Simple step",
				Command: "echo done",
			},
		},
	}

	PrintShow(&buf, rb)
	output := buf.String()

	// Header
	if !strings.Contains(output, "Show Test Runbook") {
		t.Error("missing runbook name")
	}
	if !strings.Contains(output, "Detailed test for PrintShow") {
		t.Error("missing description")
	}

	// Metadata
	if !strings.Contains(output, "testing") {
		t.Error("missing category")
	}
	if !strings.Contains(output, "test, show") {
		t.Error("missing tags")
	}
	if !strings.Contains(output, "tester") {
		t.Error("missing author")
	}
	if !strings.Contains(output, "escalate") {
		t.Error("missing on_failure")
	}
	if !strings.Contains(output, "show-test-123") {
		t.Error("missing ID")
	}

	// Steps header
	if !strings.Contains(output, "Steps (3)") {
		t.Error("missing steps count")
	}

	// Step 1 details
	if !strings.Contains(output, "Automated step") {
		t.Error("missing step 1 name")
	}
	if !strings.Contains(output, "Does something automatically") {
		t.Error("missing step 1 description")
	}
	if !strings.Contains(output, "echo hello") {
		t.Error("missing step 1 command")
	}
	if !strings.Contains(output, "test -f /tmp/ok") {
		t.Error("missing step 1 check")
	}
	if !strings.Contains(output, "30s") {
		t.Error("missing step 1 timeout")
	}
	if !strings.Contains(output, "Important note") {
		t.Error("missing step 1 notes")
	}

	// Step 2 (manual with rollback)
	if !strings.Contains(output, "MANUAL") {
		t.Error("missing MANUAL tag")
	}
	if !strings.Contains(output, "echo rollback") {
		t.Error("missing rollback command")
	}
}

func TestPrintShow_MinimalRunbook(t *testing.T) {
	var buf bytes.Buffer
	rb := &Runbook{
		ID:   "minimal-123",
		Name: "Minimal",
		Steps: []Step{
			{Name: "Only step"},
		},
	}

	PrintShow(&buf, rb)
	output := buf.String()

	if !strings.Contains(output, "Minimal") {
		t.Error("missing name")
	}
	if !strings.Contains(output, "Steps (1)") {
		t.Error("missing steps count")
	}
	// Should not contain optional metadata labels when fields are empty
	if !strings.Contains(output, "minimal-123") {
		t.Error("missing ID")
	}
}

func TestPrintShow_AllSeverities(t *testing.T) {
	severities := []string{"P1", "P2", "P3", "P4"}
	for _, sev := range severities {
		var buf bytes.Buffer
		rb := &Runbook{
			ID:       "sev-test",
			Name:     "Sev Test",
			Severity: sev,
			Steps:    []Step{{Name: "s1"}},
		}
		PrintShow(&buf, rb)
		output := buf.String()
		if !strings.Contains(output, sev) {
			t.Errorf("severity %s not in output", sev)
		}
	}
}

func TestPrintRunLog_Completed(t *testing.T) {
	var buf bytes.Buffer
	start := time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC)
	log := &RunLog{
		RunbookID:   "test-123",
		RunbookName: "Test Run",
		StartedAt:   start,
		CompletedAt: start.Add(2 * time.Minute),
		Status:      "completed",
		StepResults: []StepResult{
			{StepName: "Step 1", Status: "passed", Duration: "10s"},
			{StepName: "Step 2", Status: "passed", Duration: "1m50s"},
		},
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	if !strings.Contains(output, "✅") {
		t.Error("missing completed icon")
	}
	if !strings.Contains(output, "Test Run") {
		t.Error("missing runbook name")
	}
	if !strings.Contains(output, "2026-03-10") {
		t.Error("missing start date")
	}
	if !strings.Contains(output, "2m0s") {
		t.Error("missing duration")
	}
	if !strings.Contains(output, "Step 1") {
		t.Error("missing step 1")
	}
	if !strings.Contains(output, "passed") {
		t.Error("missing passed status")
	}
}

func TestPrintRunLog_Failed(t *testing.T) {
	var buf bytes.Buffer
	start := time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC)
	log := &RunLog{
		RunbookID:   "fail-123",
		RunbookName: "Failed Run",
		StartedAt:   start,
		CompletedAt: start.Add(30 * time.Second),
		Status:      "failed",
		StepResults: []StepResult{
			{StepName: "Step 1", Status: "passed"},
			{StepName: "Step 2", Status: "failed", Error: "connection refused"},
			{StepName: "Step 3", Status: "skipped"},
		},
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	if !strings.Contains(output, "❌") {
		t.Error("missing failed icon")
	}
	if !strings.Contains(output, "connection refused") {
		t.Error("missing error message")
	}
	if !strings.Contains(output, "skipped") {
		t.Error("missing skipped status")
	}
}

func TestPrintRunLog_Aborted(t *testing.T) {
	var buf bytes.Buffer
	log := &RunLog{
		RunbookName: "Aborted Run",
		StartedAt:   time.Now(),
		Status:      "aborted",
		StepResults: []StepResult{
			{StepName: "Step 1", Status: "passed"},
		},
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	if !strings.Contains(output, "⏹️") {
		t.Error("missing aborted icon")
	}
}

func TestPrintRunLog_Running(t *testing.T) {
	var buf bytes.Buffer
	log := &RunLog{
		RunbookName: "Running Task",
		StartedAt:   time.Now(),
		Status:      "running",
		StepResults: []StepResult{
			{StepName: "Step 1", Status: "passed"},
			{StepName: "Step 2", Status: "pending"},
		},
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	if !strings.Contains(output, "🔄") {
		t.Error("missing running icon")
	}
	if !strings.Contains(output, "pending") {
		t.Error("missing pending step")
	}
}

func TestPrintRunLog_NoCompletion(t *testing.T) {
	var buf bytes.Buffer
	log := &RunLog{
		RunbookName: "In Progress",
		StartedAt:   time.Now(),
		Status:      "running",
		StepResults: nil,
	}

	PrintRunLog(&buf, log)
	output := buf.String()

	// Should not show "Completed" line when CompletedAt is zero
	if strings.Contains(output, "Completed:") {
		t.Error("should not show completed time when zero")
	}
}

func TestFormatJSON_Runbook(t *testing.T) {
	rb := &Runbook{
		ID:       "json-test",
		Name:     "JSON Test",
		Category: "testing",
		Steps: []Step{
			{Name: "Step 1", Command: "echo test"},
		},
	}

	result, err := FormatJSON(rb)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}

	if parsed["ID"] != "json-test" {
		t.Errorf("ID = %v, want json-test", parsed["ID"])
	}
	if parsed["Name"] != "JSON Test" {
		t.Errorf("Name = %v, want JSON Test", parsed["Name"])
	}
}

func TestFormatJSON_RunLog(t *testing.T) {
	log := &RunLog{
		RunbookID:   "log-json",
		RunbookName: "Log JSON Test",
		Status:      "completed",
		StartedAt:   time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC),
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

	if parsed["Status"] != "completed" {
		t.Errorf("Status = %v, want completed", parsed["Status"])
	}
}

func TestFormatJSON_List(t *testing.T) {
	rbs := []*Runbook{
		{ID: "a", Name: "Alpha"},
		{ID: "b", Name: "Beta"},
	}

	result, err := FormatJSON(rbs)
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("invalid JSON array: %v", err)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 items, got %d", len(parsed))
	}
}

func TestFormatJSON_InvalidType(t *testing.T) {
	// Channels can't be marshaled to JSON
	ch := make(chan int)
	_, err := FormatJSON(ch)
	if err == nil {
		t.Error("expected error for unmarshalable type")
	}
}

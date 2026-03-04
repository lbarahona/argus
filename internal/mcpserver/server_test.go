package mcpserver

import (
	"fmt"
	"testing"
)

func TestTextResult(t *testing.T) {
	r, err := textResult("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.IsError {
		t.Fatal("expected non-error result")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(r.Content))
	}
}

func TestErrorResult(t *testing.T) {
	r, err := errorResult(fmt.Errorf("boom"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.IsError {
		t.Fatal("expected error result")
	}
}

func TestDefaults(t *testing.T) {
	if defaults(0, 60) != 60 {
		t.Error("expected default value")
	}
	if defaults(30, 60) != 30 {
		t.Error("expected provided value")
	}
	if defaults(-1, 100) != 100 {
		t.Error("expected default for negative")
	}
}

func TestJsonText(t *testing.T) {
	result := jsonText(map[string]string{"key": "value"})
	if result == "" {
		t.Error("expected non-empty JSON")
	}
}

func TestFormatLogsForAI(t *testing.T) {
	// Empty logs
	result := formatLogsForAI(nil)
	if result != "" {
		t.Error("expected empty string for nil logs")
	}
}

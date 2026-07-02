package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lbarahona/argus/internal/ai"
)

// ──────────────────────────────────────────────
// Tests: New constructor
// ──────────────────────────────────────────────

func TestNew(t *testing.T) {
	mock := &mockSignozClient{}
	provider := ai.NewAnthropicProvider("test-key", "")

	s := New(mock, Options{
		InstanceKey:  "prod",
		InstanceName: "Production",
		AIProvider:   provider,
		MaxHistory:   10,
	})

	if s.instanceKey != "prod" {
		t.Errorf("expected instanceKey=prod, got %s", s.instanceKey)
	}
	if s.instanceName != "Production" {
		t.Errorf("expected instanceName=Production, got %s", s.instanceName)
	}
	if s.maxHistory != 10 {
		t.Errorf("expected maxHistory=10, got %d", s.maxHistory)
	}
	if s.stdin == nil {
		t.Error("stdin should default to os.Stdin")
	}
	if s.stdout == nil {
		t.Error("stdout should default to os.Stdout")
	}
}

func TestNew_DefaultMaxHistory(t *testing.T) {
	s := New(&mockSignozClient{}, Options{
		InstanceKey: "test",
		AIProvider:  ai.NewAnthropicProvider("key", ""),
	})

	if s.maxHistory != 20 {
		t.Errorf("expected default maxHistory=20, got %d", s.maxHistory)
	}
}

func TestNew_ZeroMaxHistory(t *testing.T) {
	s := New(&mockSignozClient{}, Options{
		InstanceKey: "test",
		AIProvider:  ai.NewAnthropicProvider("key", ""),
		MaxHistory:  0,
	})

	if s.maxHistory != 20 {
		t.Errorf("expected default maxHistory=20 for zero, got %d", s.maxHistory)
	}
}

func TestNew_NegativeMaxHistory(t *testing.T) {
	s := New(&mockSignozClient{}, Options{
		InstanceKey: "test",
		AIProvider:  ai.NewAnthropicProvider("key", ""),
		MaxHistory:  -5,
	})

	if s.maxHistory != 20 {
		t.Errorf("expected default maxHistory=20 for negative, got %d", s.maxHistory)
	}
}

func TestNew_OddMaxHistoryRoundsUpAndTrims(t *testing.T) {
	s := New(&mockSignozClient{}, Options{
		InstanceKey: "test",
		AIProvider:  ai.NewAnthropicProvider("key", ""),
		MaxHistory:  1,
	})

	if s.maxHistory != 2 {
		t.Errorf("expected maxHistory normalized to 2, got %d", s.maxHistory)
	}

	// 6 history messages should still be trimmed down after normalization.
	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}

	s.trimHistory()

	if len(s.history) > 2 {
		t.Errorf("expected history trimmed to <=2, got %d", len(s.history))
	}
}

func TestNew_NoInstanceName(t *testing.T) {
	s := New(&mockSignozClient{}, Options{
		InstanceKey: "my-instance",
		AIProvider:  ai.NewAnthropicProvider("key", ""),
	})

	if s.instanceName != "my-instance" {
		t.Errorf("expected instanceName to fallback to key, got %s", s.instanceName)
	}
}

// ──────────────────────────────────────────────
// Tests: printBanner
// ──────────────────────────────────────────────

func TestPrintBanner(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out

	s.printBanner()

	output := out.String()
	if !strings.Contains(output, "Interactive Mode") {
		t.Error("banner should show Interactive Mode")
	}
	if !strings.Contains(output, "Test Instance") {
		t.Error("banner should show instance name")
	}
	if !strings.Contains(output, "/help") {
		t.Error("banner should mention /help command")
	}
}

// ──────────────────────────────────────────────
// Tests: trimHistory edge cases
// ──────────────────────────────────────────────

func TestTrimHistory_ExactLimit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.maxHistory = 4

	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}

	s.trimHistory()

	if len(s.history) != 4 {
		t.Errorf("should not trim at exact limit, got %d", len(s.history))
	}
}

func TestTrimHistory_OddExcess(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.maxHistory = 4

	// 5 messages = excess of 1, should round up to 2 to keep pairs
	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
	}

	s.trimHistory()

	if len(s.history) > s.maxHistory {
		t.Errorf("should trim to max, got %d", len(s.history))
	}
	// Most recent should be preserved
	last := s.history[len(s.history)-1]
	if last.Content != "q3" {
		t.Errorf("most recent message should be q3, got %s", last.Content)
	}
}

func TestTrimHistory_EmptyHistory(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.maxHistory = 4
	s.history = nil

	s.trimHistory()

	if s.history != nil {
		t.Error("should stay nil")
	}
}

// ──────────────────────────────────────────────
// Tests: Run with commands interleaved
// ──────────────────────────────────────────────

func TestRun_ClearThenHistoryThenExit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("/clear\n/history\nexit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "cleared") {
		t.Error("should show clear confirmation")
	}
	if !strings.Contains(output, "0 turns") {
		t.Error("should show 0 turns after clear")
	}
}

func TestRun_MultipleEmptyLines(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("\n\n\n\nexit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ──────────────────────────────────────────────
// Tests: handleCommand edge cases
// ──────────────────────────────────────────────

func TestHandleCommand_UnknownSlashCommand(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out

	// Unknown slash commands should NOT be treated as commands
	handled := s.handleCommand("/unknown")
	if handled {
		t.Error("/unknown should not be handled")
	}
}

func TestHandleCommand_HistoryEmpty(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.history = nil

	handled := s.handleCommand("/history")
	if !handled {
		t.Error("/history should be handled")
	}
	if !strings.Contains(out.String(), "0 turns") {
		t.Error("should show 0 turns for empty history")
	}
}

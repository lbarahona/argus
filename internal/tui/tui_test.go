package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock
// ──────────────────────────────────────────────

type mockSignozClient struct {
	listServicesFunc func(ctx context.Context) ([]types.Service, error)
	queryLogsFunc    func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
}

func (m *mockSignozClient) Health(ctx context.Context) (bool, time.Duration, error) {
	return true, 0, nil
}

func (m *mockSignozClient) ListServices(ctx context.Context) ([]types.Service, error) {
	if m.listServicesFunc != nil {
		return m.listServicesFunc(ctx)
	}
	return nil, nil
}

func (m *mockSignozClient) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if m.queryLogsFunc != nil {
		return m.queryLogsFunc(ctx, service, durationMinutes, limit, severityFilter)
	}
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func newTestSession(mock *mockSignozClient) *Session {
	return &Session{
		client:       mock,
		instanceKey:  "test",
		instanceName: "Test Instance",
		anthropicKey: "test-key",
		maxHistory:   20,
		stdout:       &bytes.Buffer{},
	}
}

// ──────────────────────────────────────────────
// gatherContext Tests
// ──────────────────────────────────────────────

func TestGatherContext(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
				{Name: "web", NumCalls: 500, NumErrors: 0, ErrorRate: 0},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{
				Logs: []types.LogEntry{
					{Body: "connection refused", SeverityText: "ERROR", ServiceName: "api", Timestamp: time.Now()},
				},
			}, nil
		},
	}

	s := newTestSession(mock)
	result := s.gatherContext(context.Background())

	if !strings.Contains(result, "api") {
		t.Error("context should contain service name 'api'")
	}
	if !strings.Contains(result, "1000 calls") {
		t.Error("context should contain call count")
	}
	if !strings.Contains(result, "connection refused") {
		t.Error("context should contain error log body")
	}
	if !strings.Contains(result, "## Services") {
		t.Error("context should have Services section")
	}
	if !strings.Contains(result, "## Recent Error Logs") {
		t.Error("context should have Recent Error Logs section")
	}
}

func TestGatherContextNoData(t *testing.T) {
	mock := &mockSignozClient{}
	s := newTestSession(mock)
	result := s.gatherContext(context.Background())

	if result != "" {
		t.Errorf("expected empty context when no data, got: %q", result)
	}
}

// ──────────────────────────────────────────────
// handleCommand Tests
// ──────────────────────────────────────────────

func TestHandleCommandClear(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.history = []ai.Message{
		{Role: "user", Content: "test"},
		{Role: "assistant", Content: "response"},
	}

	out := &bytes.Buffer{}
	s.stdout = out

	handled := s.handleCommand("/clear")
	if !handled {
		t.Error("/clear should be handled")
	}
	if len(s.history) != 0 {
		t.Error("history should be empty after /clear")
	}
	if !strings.Contains(out.String(), "cleared") {
		t.Error("output should confirm history was cleared")
	}
}

func TestHandleCommandHelp(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out

	handled := s.handleCommand("/help")
	if !handled {
		t.Error("/help should be handled")
	}
	if !strings.Contains(out.String(), "/clear") {
		t.Error("help should mention /clear command")
	}
	if !strings.Contains(out.String(), "/history") {
		t.Error("help should mention /history command")
	}
	if !strings.Contains(out.String(), "exit") {
		t.Error("help should mention exit command")
	}
}

func TestHandleCommandHistory(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
	}

	out := &bytes.Buffer{}
	s.stdout = out

	handled := s.handleCommand("/history")
	if !handled {
		t.Error("/history should be handled")
	}
	if !strings.Contains(out.String(), "2 turns") {
		t.Error("should report 2 turns")
	}
	if !strings.Contains(out.String(), "4 messages") {
		t.Error("should report 4 messages")
	}
}

func TestHandleCommandRegularInput(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	if s.handleCommand("check my services") {
		t.Error("regular input should not be handled as a command")
	}
}

// ──────────────────────────────────────────────
// trimHistory Tests
// ──────────────────────────────────────────────

func TestTrimHistory(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.maxHistory = 4

	// Add 6 messages (3 turns), exceeding maxHistory of 4
	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}

	s.trimHistory()

	if len(s.history) > s.maxHistory {
		t.Errorf("history should be trimmed to %d, got %d", s.maxHistory, len(s.history))
	}
	// Should keep the most recent messages
	if s.history[len(s.history)-1].Content != "a3" {
		t.Error("should keep the most recent messages")
	}
	if s.history[0].Content == "q1" {
		t.Error("oldest messages should be dropped")
	}
}

func TestTrimHistoryUnderLimit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	s.maxHistory = 20

	s.history = []ai.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
	}

	s.trimHistory()

	if len(s.history) != 2 {
		t.Error("should not trim when under limit")
	}
}

// ──────────────────────────────────────────────
// Run Tests
// ──────────────────────────────────────────────

func TestRunExit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("exit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Session ended") {
		t.Error("should print session ended message")
	}
}

func TestRunQuit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("quit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Session ended") {
		t.Error("should print session ended message")
	}
}

func TestRunEOF(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("") // EOF

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Session ended") {
		t.Error("should print session ended on EOF")
	}
}

func TestRunEmptyInput(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("\n\nexit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCommandThenExit(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("/help\n/clear\nexit\n")

	err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Available commands") {
		t.Error("should show help output")
	}
	if !strings.Contains(output, "cleared") {
		t.Error("should show clear confirmation")
	}
}

func TestRunBannerShowsInstance(t *testing.T) {
	s := newTestSession(&mockSignozClient{})
	out := &bytes.Buffer{}
	s.stdout = out
	s.stdin = strings.NewReader("exit\n")

	_ = s.Run(context.Background())

	output := out.String()
	if !strings.Contains(output, "Test Instance") {
		t.Error("banner should show instance name")
	}
	if !strings.Contains(output, "Interactive Mode") {
		t.Error("banner should show Interactive Mode")
	}
}

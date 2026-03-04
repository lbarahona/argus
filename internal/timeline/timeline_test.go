package timeline

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services []types.Service
	logs     *types.QueryResult
	traces   *types.QueryResult
	metrics  *types.QueryResult
	healthOk bool
}

func (m *mockQuerier) Health(ctx context.Context) (bool, time.Duration, error) {
	return m.healthOk, time.Millisecond, nil
}

func (m *mockQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	if m.logs != nil {
		return m.logs, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	if m.traces != nil {
		return m.traces, nil
	}
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	if m.metrics != nil {
		return m.metrics, nil
	}
	return &types.QueryResult{}, nil
}

func TestGenerateEmptyTimeline(t *testing.T) {
	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 0},
		},
	}

	tl, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tl.Events) != 0 {
		t.Errorf("expected 0 events for healthy service, got %d", len(tl.Events))
	}
}

func TestGenerateWithErrorSpike(t *testing.T) {
	now := time.Now()
	// Create a spike: 20 errors in a 5-min bucket
	var logs []types.LogEntry
	for i := 0; i < 20; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp:    now.Add(-10 * time.Minute).Add(time.Duration(i) * time.Second),
			Body:         "connection refused: database unreachable",
			SeverityText: "ERROR",
			ServiceName:  "api-service",
		})
	}

	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{
			{Name: "api-service", NumCalls: 1000, NumErrors: 20},
		},
		logs: &types.QueryResult{Logs: logs},
	}

	tl, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tl.Events) == 0 {
		t.Fatal("expected events for error spike, got 0")
	}

	hasErrorSpike := false
	for _, e := range tl.Events {
		if e.Type == EventErrorSpike || e.Type == EventNewError {
			hasErrorSpike = true
			break
		}
	}
	if !hasErrorSpike {
		t.Error("expected an error spike or new error event")
	}
}

func TestGenerateWithHighErrorRate(t *testing.T) {
	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{
			{Name: "payment-service", NumCalls: 100, NumErrors: 60},
		},
	}

	tl, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasServiceDown := false
	for _, e := range tl.Events {
		if e.Type == EventServiceDown && e.Service == "payment-service" {
			hasServiceDown = true
			if e.Severity != "critical" {
				t.Errorf("expected critical severity for 60%% error rate, got %s", e.Severity)
			}
			break
		}
	}
	if !hasServiceDown {
		t.Error("expected service down event for 60% error rate")
	}
}

func TestGenerateWithLatencySpike(t *testing.T) {
	now := time.Now()
	var traces []types.TraceEntry

	// Normal traces (P50 ~50ms)
	for i := 0; i < 95; i++ {
		traces = append(traces, types.TraceEntry{
			Timestamp:     now.Add(-time.Duration(i) * time.Minute),
			ServiceName:   "api",
			OperationName: "GET /users",
			DurationNano:  50_000_000, // 50ms
			TraceID:       "normal-trace",
		})
	}

	// Slow traces (P99 ~5000ms)
	for i := 0; i < 5; i++ {
		traces = append(traces, types.TraceEntry{
			Timestamp:     now.Add(-5 * time.Minute),
			ServiceName:   "api",
			OperationName: "GET /users",
			DurationNano:  5_000_000_000, // 5000ms
			TraceID:       "slow-trace",
		})
	}

	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{},
		traces:   &types.QueryResult{Traces: traces},
	}

	tl, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasLatencySpike := false
	for _, e := range tl.Events {
		if e.Type == EventLatencySpike {
			hasLatencySpike = true
			break
		}
	}
	if !hasLatencySpike {
		t.Error("expected latency spike event")
	}
}

func TestDetectErrorSpikesMultipleServices(t *testing.T) {
	now := time.Now()
	var logs []types.LogEntry

	// Service A: consistent errors (5 per bucket)
	for i := 0; i < 30; i++ {
		bucket := i / 5
		logs = append(logs, types.LogEntry{
			Timestamp:   now.Add(-time.Duration(bucket*5+1) * time.Minute),
			Body:        "timeout",
			ServiceName: "svc-a",
		})
	}

	// Service B: spike in one bucket (20 errors)
	for i := 0; i < 20; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp:   now.Add(-10 * time.Minute),
			Body:        "crash",
			ServiceName: "svc-b",
		})
	}
	// Service B: normal in other buckets (2 errors)
	for i := 0; i < 4; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp:   now.Add(-time.Duration(20+i*5) * time.Minute),
			Body:        "crash",
			ServiceName: "svc-b",
		})
	}

	events := detectErrorSpikes(logs, 60)

	hasSvcBSpike := false
	for _, e := range events {
		if e.Service == "svc-b" && e.Type == EventErrorSpike {
			hasSvcBSpike = true
		}
	}
	if !hasSvcBSpike {
		t.Error("expected error spike for svc-b")
	}
}

func TestDetectNewErrors(t *testing.T) {
	now := time.Now()
	logs := []types.LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Body: "connection refused", ServiceName: "api"},
		{Timestamp: now.Add(-29 * time.Minute), Body: "connection refused", ServiceName: "api"},
		{Timestamp: now.Add(-28 * time.Minute), Body: "connection refused", ServiceName: "api"},
	}

	events := detectNewErrors(logs)
	if len(events) == 0 {
		t.Error("expected at least one new error event")
	}
	if len(events) > 0 && events[0].Type != EventNewError {
		t.Errorf("expected EventNewError, got %s", events[0].Type)
	}
}

func TestDetectServiceHealth(t *testing.T) {
	tests := []struct {
		name     string
		services []types.Service
		wantLen  int
		severity string
	}{
		{
			name:     "healthy service",
			services: []types.Service{{Name: "api", NumCalls: 100, NumErrors: 1}},
			wantLen:  0,
		},
		{
			name:     "critical error rate",
			services: []types.Service{{Name: "api", NumCalls: 100, NumErrors: 60}},
			wantLen:  1,
			severity: "critical",
		},
		{
			name:     "warning error rate",
			services: []types.Service{{Name: "api", NumCalls: 100, NumErrors: 15}},
			wantLen:  1,
			severity: "warning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := detectServiceHealth(tt.services)
			if len(events) != tt.wantLen {
				t.Errorf("expected %d events, got %d", tt.wantLen, len(events))
			}
			if tt.wantLen > 0 && events[0].Severity != tt.severity {
				t.Errorf("expected severity %s, got %s", tt.severity, events[0].Severity)
			}
		})
	}
}

func TestRenderTerminalEmpty(t *testing.T) {
	tl := &Timeline{
		StartTime: time.Now().Add(-1 * time.Hour),
		EndTime:   time.Now(),
		Duration:  1 * time.Hour,
		Instance:  "prod",
	}

	var buf bytes.Buffer
	tl.RenderTerminal(&buf)

	if !strings.Contains(buf.String(), "No incidents detected") {
		t.Error("expected 'No incidents detected' for empty timeline")
	}
}

func TestRenderTerminalWithEvents(t *testing.T) {
	now := time.Now()
	tl := &Timeline{
		StartTime: now.Add(-1 * time.Hour),
		EndTime:   now,
		Duration:  1 * time.Hour,
		Instance:  "prod",
		Services:  []string{"api", "db"},
		Events: []Event{
			{
				Timestamp:   now.Add(-45 * time.Minute),
				Type:        EventErrorSpike,
				Service:     "api",
				Description: "Error spike: 50 errors",
				Severity:    "critical",
				Details:     map[string]string{"count": "50"},
			},
			{
				Timestamp:   now.Add(-30 * time.Minute),
				Type:        EventLatencySpike,
				Service:     "db",
				Description: "P99=5000ms",
				Severity:    "warning",
				Details:     map[string]string{"trace_id": "abc123"},
			},
		},
	}

	var buf bytes.Buffer
	tl.RenderTerminal(&buf)

	output := buf.String()
	if !strings.Contains(output, "INCIDENT TIMELINE") {
		t.Error("expected header")
	}
	if !strings.Contains(output, "api") {
		t.Error("expected api service in output")
	}
	if !strings.Contains(output, "abc123") {
		t.Error("expected trace ID in output")
	}
	if !strings.Contains(output, "🔴") {
		t.Error("expected critical icon")
	}
}

func TestRenderMarkdown(t *testing.T) {
	now := time.Now()
	tl := &Timeline{
		StartTime: now.Add(-1 * time.Hour),
		EndTime:   now,
		Duration:  1 * time.Hour,
		Instance:  "prod",
		Services:  []string{"api"},
		Events: []Event{
			{
				Timestamp:   now.Add(-30 * time.Minute),
				Type:        EventErrorSpike,
				Service:     "api",
				Description: "Error spike",
				Severity:    "critical",
			},
		},
	}

	var buf bytes.Buffer
	tl.RenderMarkdown(&buf)

	output := buf.String()
	if !strings.Contains(output, "# 🔭 Incident Timeline") {
		t.Error("expected markdown header")
	}
	if !strings.Contains(output, "| Time |") {
		t.Error("expected markdown table")
	}
}

func TestTimelineServiceFilter(t *testing.T) {
	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 60},
			{Name: "db", NumCalls: 100, NumErrors: 60},
		},
	}

	// With service filter, the mock still returns all services in ListServices
	// but the command filters logs/traces by service
	tl, err := Generate(context.Background(), mock, "test", Options{
		Duration: 60,
		Service:  "api",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still have events from service health (all services returned)
	if tl == nil {
		t.Fatal("expected non-nil timeline")
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("truncateStr", func(t *testing.T) {
		if got := truncateStr("hello", 10); got != "hello" {
			t.Errorf("expected 'hello', got '%s'", got)
		}
		if got := truncateStr("hello world", 6); got != "hello…" {
			t.Errorf("expected 'hello…', got '%s'", got)
		}
	})

	t.Run("severityFromCount", func(t *testing.T) {
		if got := severityFromCount(100); got != "critical" {
			t.Errorf("expected critical, got %s", got)
		}
		if got := severityFromCount(20); got != "warning" {
			t.Errorf("expected warning, got %s", got)
		}
		if got := severityFromCount(3); got != "info" {
			t.Errorf("expected info, got %s", got)
		}
	})

	t.Run("formatDuration", func(t *testing.T) {
		if got := formatDuration(30 * time.Second); got != "30s" {
			t.Errorf("expected '30s', got '%s'", got)
		}
		if got := formatDuration(5 * time.Minute); got != "5m" {
			t.Errorf("expected '5m', got '%s'", got)
		}
		if got := formatDuration(90 * time.Minute); got != "1h30m" {
			t.Errorf("expected '1h30m', got '%s'", got)
		}
	})

	t.Run("severityIcon", func(t *testing.T) {
		if got := severityIcon("critical"); got != "🔴" {
			t.Errorf("expected 🔴, got %s", got)
		}
		if got := severityIcon("warning"); got != "🟡" {
			t.Errorf("expected 🟡, got %s", got)
		}
		if got := severityIcon("info"); got != "🔵" {
			t.Errorf("expected 🔵, got %s", got)
		}
	})

	t.Run("countSeverities", func(t *testing.T) {
		events := []Event{
			{Severity: "critical"},
			{Severity: "critical"},
			{Severity: "warning"},
			{Severity: "info"},
		}
		c, w, i := countSeverities(events)
		if c != 2 || w != 1 || i != 1 {
			t.Errorf("expected 2,1,1 got %d,%d,%d", c, w, i)
		}
	})

	t.Run("wrapText", func(t *testing.T) {
		lines := wrapText("abcdefghij", 5)
		if len(lines) != 2 {
			t.Errorf("expected 2 lines, got %d", len(lines))
		}
	})
}

func TestEventsSortedChronologically(t *testing.T) {
	var logs []types.LogEntry
	now := time.Now()
	// Errors at different times
	for i := 0; i < 10; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp:   now.Add(-50 * time.Minute),
			Body:        "early error",
			ServiceName: "early-svc",
		})
	}
	for i := 0; i < 10; i++ {
		logs = append(logs, types.LogEntry{
			Timestamp:   now.Add(-10 * time.Minute),
			Body:        "late error",
			ServiceName: "late-svc",
		})
	}

	mock := &mockQuerier{
		healthOk: true,
		services: []types.Service{},
		logs:     &types.QueryResult{Logs: logs},
	}

	tl, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify chronological order
	for i := 1; i < len(tl.Events); i++ {
		if tl.Events[i].Timestamp.Before(tl.Events[i-1].Timestamp) {
			t.Errorf("events not sorted chronologically: event %d (%s) before event %d (%s)",
				i, tl.Events[i].Timestamp, i-1, tl.Events[i-1].Timestamp)
		}
	}
}

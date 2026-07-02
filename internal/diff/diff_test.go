package diff

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// windowedDiffMock implements SignozQuerier + signoz.WindowedQuerier.
type windowedDiffMock struct {
	mockSignozClient
	rangeCalls []struct{ Start, End time.Time }
	rangeLogs  []types.LogEntry
}

func (m *windowedDiffMock) QueryLogsRange(ctx context.Context, service string, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error) {
	m.rangeCalls = append(m.rangeCalls, struct{ Start, End time.Time }{start, end})
	return &types.QueryResult{Logs: m.rangeLogs}, nil
}

func (m *windowedDiffMock) QueryTracesRange(ctx context.Context, service string, start, end time.Time, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func (m *windowedDiffMock) ListServicesRange(ctx context.Context, start, end time.Time) ([]types.Service, error) {
	return nil, nil
}

// ──────────────────────────────────────────────
// Compare Tests (mock-based)
// ──────────────────────────────────────────────

func TestCompareWithMockClient(t *testing.T) {
	now := time.Now()
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 10},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			if durationMinutes == 60 {
				// Recent window
				return &types.QueryResult{
					Logs: []types.LogEntry{
						{ServiceName: "api", Timestamp: now.Add(-10 * time.Minute)},
						{ServiceName: "api", Timestamp: now.Add(-20 * time.Minute)},
					},
				}, nil
			}
			// Full window (includes previous)
			return &types.QueryResult{
				Logs: []types.LogEntry{
					{ServiceName: "api", Timestamp: now.Add(-10 * time.Minute)},
					{ServiceName: "api", Timestamp: now.Add(-20 * time.Minute)},
					{ServiceName: "api", Timestamp: now.Add(-70 * time.Minute)},
				},
			}, nil
		},
	}

	result, err := Compare(context.Background(), mock, "test-instance", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Instance != "test-instance" {
		t.Errorf("expected instance=test-instance, got %s", result.Instance)
	}
	if result.DurationMin != 60 {
		t.Errorf("expected duration=60, got %d", result.DurationMin)
	}
}

func TestCountByService(t *testing.T) {
	now := time.Now()
	logs := []types.LogEntry{
		{ServiceName: "api", Timestamp: now.Add(-5 * time.Minute)},
		{ServiceName: "api", Timestamp: now.Add(-10 * time.Minute)},
		{ServiceName: "web", Timestamp: now.Add(-15 * time.Minute)},
		{ServiceName: "api", Timestamp: now.Add(-90 * time.Minute)}, // outside window
	}

	counts := countByService(logs, now.Add(-60*time.Minute), now)
	if counts["api"] != 2 {
		t.Errorf("expected 2 api errors in window, got %d", counts["api"])
	}
	if counts["web"] != 1 {
		t.Errorf("expected 1 web error, got %d", counts["web"])
	}
}

func TestRenderTerminal(t *testing.T) {
	r := &DiffResult{
		Instance:    "prod",
		WindowA:     "120-60 min ago",
		WindowB:     "0-60 min ago",
		DurationMin: 60,
		GeneratedAt: time.Now(),
		Services: []ServiceDiff{
			{Name: "api", ErrorsBefore: 10, ErrorsAfter: 25, ErrorsChange: 150, Status: "degraded"},
			{Name: "auth", ErrorsBefore: 20, ErrorsAfter: 5, ErrorsChange: -75, Status: "improved"},
		},
		Summary: DiffSummary{
			TotalErrorsBefore: 30,
			TotalErrorsAfter:  30,
			Degraded:          1,
			Improved:          1,
		},
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("ARGUS SERVICE DIFF")) {
		t.Error("expected diff header")
	}
	if !bytes.Contains([]byte(output), []byte("degraded")) {
		t.Error("expected degraded status")
	}
}

func TestStatusEmoji(t *testing.T) {
	if statusEmoji("degraded") != "🔴" {
		t.Error("wrong emoji for degraded")
	}
	if statusEmoji("improved") != "🟢" {
		t.Error("wrong emoji for improved")
	}
}

func TestTruncate_MultibyteServiceName_RuneSafe(t *testing.T) {
	// Multibyte (3-byte-per-rune) service name well over the byte-index
	// truncation point but under the rune-index one; a byte-slice truncate
	// would split a rune mid-sequence and produce invalid UTF-8.
	name := strings.Repeat("服", 20) // 20 runes, 60 bytes
	n := 10

	got := truncate(name, n)

	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if count := utf8.RuneCountInString(got); count > n {
		t.Errorf("truncated rune count = %d, want <= %d", count, n)
	}
	want := string([]rune(name)[:n-1]) + "…"
	if got != want {
		t.Errorf("truncate(%q, %d) = %q, want %q", name, n, got, want)
	}
}

func TestTruncate_ShortStringUnchanged(t *testing.T) {
	if got := truncate("api", 30); got != "api" {
		t.Errorf("truncate(\"api\", 30) = %q, want %q", got, "api")
	}
}

func TestTruncate_ExactLengthUnchanged(t *testing.T) {
	s := strings.Repeat("a", 30)
	if got := truncate(s, 30); got != s {
		t.Errorf("truncate at exact length should be unchanged, got %q", got)
	}
}

// ──────────────────────────────────────────────
// Windowed previous-window & truncation tests
// ──────────────────────────────────────────────

func TestCompareUsesWindowedQueryForPreviousWindow(t *testing.T) {
	oldTS := time.Now().Add(-90 * time.Minute)
	mock := &windowedDiffMock{rangeLogs: []types.LogEntry{{ServiceName: "api", Timestamp: oldTS}}}
	// Recent window: plain QueryLogs returns one recent error for api.
	mock.queryLogsFunc = func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
		return &types.QueryResult{Logs: []types.LogEntry{{ServiceName: "api", Timestamp: time.Now().Add(-5 * time.Minute)}}}, nil
	}

	result, err := Compare(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.rangeCalls) != 1 {
		t.Fatalf("expected 1 windowed query for the previous window, got %d", len(mock.rangeCalls))
	}
	// Previous window: [now-2d, now-d]
	span := mock.rangeCalls[0].End.Sub(mock.rangeCalls[0].Start)
	if span != 60*time.Minute {
		t.Errorf("previous window span = %v, want 60m", span)
	}
	// api: 1 before, 1 after → stable, not "new"
	for _, d := range result.Services {
		if d.Name == "api" && d.Status == "new" {
			t.Errorf("api had errors in the previous window; must not be classified new")
		}
	}
}

func TestCompareSetsTruncationCaveat(t *testing.T) {
	logs := make([]types.LogEntry, 500) // == the fetch limit
	for i := range logs {
		logs[i] = types.LogEntry{ServiceName: "api", Timestamp: time.Now().Add(-time.Minute)}
	}
	mock := &mockSignozClient{ // plain, non-windowed mock
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{Logs: logs}, nil
		},
	}

	result, err := Compare(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated || result.DataCaveat == "" {
		t.Errorf("fetch at limit must set Truncated + DataCaveat, got %+v / %q", result.Truncated, result.DataCaveat)
	}
}

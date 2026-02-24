package report

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock
// ──────────────────────────────────────────────

type mockSignozClient struct {
	healthFunc       func(ctx context.Context) (bool, time.Duration, error)
	listServicesFunc func(ctx context.Context) ([]types.Service, error)
	queryLogsFunc    func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
}

func (m *mockSignozClient) Health(ctx context.Context) (bool, time.Duration, error) {
	if m.healthFunc != nil {
		return m.healthFunc(ctx)
	}
	return true, 10 * time.Millisecond, nil
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
// Generate Tests (mock-based)
// ──────────────────────────────────────────────

func TestGenerateWithMockClient(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
				{Name: "web", NumCalls: 500, NumErrors: 0},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			if severityFilter == "ERROR" {
				return &types.QueryResult{
					Logs: []types.LogEntry{
						{Body: "connection refused", SeverityText: "ERROR", ServiceName: "api"},
					},
				}, nil
			}
			return &types.QueryResult{}, nil
		},
	}

	r, err := Generate(context.Background(), mock, "test-instance", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Instance != "test-instance" {
		t.Errorf("expected instance=test-instance, got %s", r.Instance)
	}
	if len(r.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(r.Services))
	}
	if r.TotalCalls != 1500 {
		t.Errorf("expected 1500 total calls, got %d", r.TotalCalls)
	}
	if r.TotalErrors != 50 {
		t.Errorf("expected 50 total errors, got %d", r.TotalErrors)
	}
	if len(r.ErrorLogs) != 1 {
		t.Errorf("expected 1 error log, got %d", len(r.ErrorLogs))
	}
}

func TestGenerateUnhealthyInstance(t *testing.T) {
	mock := &mockSignozClient{
		healthFunc: func(ctx context.Context) (bool, time.Duration, error) {
			return false, 0, nil
		},
	}

	r, err := Generate(context.Background(), mock, "down-instance", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(r.Health) != 1 {
		t.Fatalf("expected 1 health status")
	}
	if r.Health[0].Healthy {
		t.Error("expected unhealthy")
	}
}

func TestComputeTopErrors(t *testing.T) {
	services := []types.Service{
		{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
		{Name: "web", NumCalls: 500, NumErrors: 0, ErrorRate: 0},
		{Name: "auth", NumCalls: 200, NumErrors: 100, ErrorRate: 50.0},
	}

	top := computeTopErrors(services)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].Service != "auth" {
		t.Errorf("expected auth first (most errors), got %s", top[0].Service)
	}
}

func TestDetectPatterns(t *testing.T) {
	logs := []types.LogEntry{
		{Body: "connection refused to database", ServiceName: "api", Timestamp: time.Now()},
		{Body: "connection refused to database", ServiceName: "api", Timestamp: time.Now()},
		{Body: "connection refused to database", ServiceName: "api", Timestamp: time.Now()},
		{Body: "timeout waiting for response", ServiceName: "web", Timestamp: time.Now()},
	}

	patterns := detectPatterns(logs)
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}
	if patterns[0].Count != 3 {
		t.Errorf("expected top pattern count 3, got %d", patterns[0].Count)
	}
}

func TestRenderTerminal(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    60,
		Instance:    "production",
		Health: []types.HealthStatus{
			{InstanceName: "prod", InstanceKey: "production", URL: "https://signoz.example.com", Healthy: true, Latency: 50 * time.Millisecond},
		},
		Services:    []types.Service{{Name: "api", NumCalls: 100, NumErrors: 5, ErrorRate: 5.0}},
		TotalCalls:  100,
		TotalErrors: 5,
		TopErrors:   []ServiceError{{Service: "api", Errors: 5, ErrorRate: 5.0}},
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("ARGUS HEALTH REPORT")) {
		t.Error("expected report header")
	}
	if !bytes.Contains([]byte(output), []byte("production")) {
		t.Error("expected instance name")
	}
}

func TestRenderMarkdown(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    60,
		Instance:    "staging",
		Health: []types.HealthStatus{
			{InstanceName: "staging", Healthy: true, Latency: 30 * time.Millisecond},
		},
		TotalCalls:  200,
		TotalErrors: 10,
	}

	var buf bytes.Buffer
	r.RenderMarkdown(&buf)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("# 🔭 Argus Health Report")) {
		t.Error("expected markdown header")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	if truncate("hello world this is long", 10) != "hello worl..." {
		t.Errorf("got %q", truncate("hello world this is long", 10))
	}
}

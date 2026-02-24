package explain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Mock
// ──────────────────────────────────────────────

type mockSignozClient struct {
	listServicesFunc func(ctx context.Context) ([]types.Service, error)
	queryLogsFunc    func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error)
	queryTracesFunc  func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error)
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
	if m.queryTracesFunc != nil {
		return m.queryTracesFunc(ctx, service, durationMinutes, limit)
	}
	return &types.QueryResult{}, nil
}

func (m *mockSignozClient) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

// ──────────────────────────────────────────────
// Collect Tests
// ──────────────────────────────────────────────

func TestCollectSuccess(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 10},
				{Name: "web", NumCalls: 500, NumErrors: 0},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			if severityFilter == "error" {
				return &types.QueryResult{
					Logs: []types.LogEntry{
						{Body: "connection refused", SeverityText: "ERROR", ServiceName: "api"},
					},
				}, nil
			}
			return &types.QueryResult{
				Logs: []types.LogEntry{
					{Body: "request started", SeverityText: "INFO", ServiceName: "api"},
				},
			}, nil
		},
		queryTracesFunc: func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
			return &types.QueryResult{
				Traces: []types.TraceEntry{
					{TraceID: "abc123", ServiceName: "api", OperationName: "GET /users", DurationNano: 15_000_000},
				},
			}, nil
		},
	}

	data, err := Collect(context.Background(), mock, "test-instance", Options{
		Service:  "api",
		Duration: 60,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.Service != "api" {
		t.Errorf("expected service=api, got %s", data.Service)
	}
	if data.Instance != "test-instance" {
		t.Errorf("expected instance=test-instance, got %s", data.Instance)
	}
	if len(data.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(data.Services))
	}
	if len(data.ErrorLogs) != 1 {
		t.Errorf("expected 1 error log, got %d", len(data.ErrorLogs))
	}
	if len(data.RecentLogs) != 1 {
		t.Errorf("expected 1 recent log, got %d", len(data.RecentLogs))
	}
	if len(data.Traces) != 1 {
		t.Errorf("expected 1 trace, got %d", len(data.Traces))
	}
}

func TestCollectServiceNotFound(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "web", NumCalls: 100},
			}, nil
		},
	}

	_, err := Collect(context.Background(), mock, "test", Options{
		Service:  "nonexistent",
		Duration: 60,
	})
	if err == nil {
		t.Error("expected error for missing service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestCollectListServicesError(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	_, err := Collect(context.Background(), mock, "test", Options{
		Service:  "api",
		Duration: 60,
	})
	if err == nil {
		t.Error("expected error when ListServices fails")
	}
}

// ──────────────────────────────────────────────
// BuildPrompt Tests
// ──────────────────────────────────────────────

func TestBuildPromptWithAllData(t *testing.T) {
	data := &CorrelatedData{
		Service:  "api",
		Instance: "prod",
		Services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 50},
		},
		ErrorLogs: []types.LogEntry{
			{Body: "connection refused", SeverityText: "ERROR", Timestamp: time.Now()},
		},
		RecentLogs: []types.LogEntry{
			{Body: "request started", SeverityText: "INFO", Timestamp: time.Now()},
		},
		Traces: []types.TraceEntry{
			{TraceID: "abc", ServiceName: "api", OperationName: "GET /users", DurationNano: 15_000_000, StatusCode: "OK"},
		},
	}

	prompt := BuildPrompt(data)

	if !strings.Contains(prompt, "api") {
		t.Error("prompt should mention the service")
	}
	if !strings.Contains(prompt, "prod") {
		t.Error("prompt should mention the instance")
	}
	if !strings.Contains(prompt, "connection refused") {
		t.Error("prompt should include error log body")
	}
	if !strings.Contains(prompt, "Error Logs") {
		t.Error("prompt should have Error Logs section")
	}
	if !strings.Contains(prompt, "Recent Logs") {
		t.Error("prompt should have Recent Logs section")
	}
	if !strings.Contains(prompt, "Traces") {
		t.Error("prompt should have Traces section")
	}
	if !strings.Contains(prompt, "Root Cause") {
		t.Error("prompt should ask for root cause analysis")
	}
}

func TestBuildPromptNoErrors(t *testing.T) {
	data := &CorrelatedData{
		Service:  "api",
		Instance: "prod",
		Services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 0},
		},
	}

	prompt := BuildPrompt(data)
	if !strings.Contains(prompt, "No error logs found") {
		t.Error("prompt should mention no error logs")
	}
}

func TestBuildPromptSlowTraces(t *testing.T) {
	data := &CorrelatedData{
		Service:  "api",
		Instance: "prod",
		Services: []types.Service{
			{Name: "api", NumCalls: 100},
		},
		Traces: []types.TraceEntry{
			{ServiceName: "api", OperationName: "GET /slow", DurationNano: 2_000_000_000, StatusCode: "OK", Timestamp: time.Now()}, // 2s
		},
	}

	prompt := BuildPrompt(data)
	if !strings.Contains(prompt, "Slow Traces") {
		t.Error("prompt should have Slow Traces section for >1s traces")
	}
}

func TestBuildPromptErrorTraces(t *testing.T) {
	data := &CorrelatedData{
		Service:  "api",
		Instance: "prod",
		Services: []types.Service{
			{Name: "api", NumCalls: 100},
		},
		Traces: []types.TraceEntry{
			{ServiceName: "api", OperationName: "POST /order", DurationNano: 100_000_000, StatusCode: "ERROR", Timestamp: time.Now()},
		},
	}

	prompt := BuildPrompt(data)
	if !strings.Contains(prompt, "Error Traces") {
		t.Error("prompt should have Error Traces section")
	}
}

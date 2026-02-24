package top

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
// Run Tests (mock-based)
// ──────────────────────────────────────────────

func TestRunWithMockClient(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
				{Name: "web", NumCalls: 500, NumErrors: 0, ErrorRate: 0},
				{Name: "auth", NumCalls: 200, NumErrors: 100, ErrorRate: 50.0},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{
				Logs: []types.LogEntry{
					{ServiceName: "api"},
					{ServiceName: "auth"},
					{ServiceName: "auth"},
				},
			}, nil
		},
	}

	result, err := Run(context.Background(), mock, "test-instance", Options{
		Limit:    10,
		SortBy:   SortByErrors,
		Duration: 60,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Instance != "test-instance" {
		t.Errorf("expected instance=test-instance, got %s", result.Instance)
	}
	if len(result.Services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(result.Services))
	}
	// Sorted by errors: auth(100) > api(50) > web(0)
	if result.Services[0].Name != "auth" {
		t.Errorf("expected auth first when sorted by errors, got %s", result.Services[0].Name)
	}
}

func TestRunSortByErrorRate(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
				{Name: "auth", NumCalls: 200, NumErrors: 100, ErrorRate: 50.0},
			}, nil
		},
	}

	result, err := Run(context.Background(), mock, "test", Options{
		SortBy: SortByErrorRate,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Services[0].Name != "auth" {
		t.Errorf("expected auth first when sorted by error rate, got %s", result.Services[0].Name)
	}
}

func TestRunSortByName(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "zebra", NumCalls: 100},
				{Name: "alpha", NumCalls: 100},
			}, nil
		},
	}

	result, err := Run(context.Background(), mock, "test", Options{
		SortBy: SortByName,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Services[0].Name != "alpha" {
		t.Errorf("expected alpha first when sorted by name, got %s", result.Services[0].Name)
	}
}

func TestRunLimit(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			svcs := make([]types.Service, 30)
			for i := range svcs {
				svcs[i] = types.Service{Name: "svc", NumCalls: 100}
			}
			return svcs, nil
		},
	}

	result, err := Run(context.Background(), mock, "test", Options{Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Services) != 5 {
		t.Errorf("expected 5 services with limit=5, got %d", len(result.Services))
	}
}

func TestRenderTerminal(t *testing.T) {
	r := &Result{
		Services: []ServiceInfo{
			{Name: "api-gateway", Calls: 10000, Errors: 500, ErrorRate: 5.0, RecentErrors: 12, Severity: "critical"},
			{Name: "auth-service", Calls: 5000, Errors: 10, ErrorRate: 0.2, RecentErrors: 1, Severity: "healthy"},
		},
		GeneratedAt: time.Now(),
		Instance:    "production",
		Duration:    60,
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("ARGUS TOP")) {
		t.Error("expected top header")
	}
	if !bytes.Contains([]byte(output), []byte("api-gateway")) {
		t.Error("expected service name")
	}
}

func TestErrorBar(t *testing.T) {
	bar := errorBar(10.0) // 5 filled + 20 empty = 25 runes
	runes := []rune(bar)
	if len(runes) != 25 {
		t.Errorf("expected 25 runes, got %d", len(runes))
	}

	bar0 := errorBar(0)
	runes0 := []rune(bar0)
	if len(runes0) != 25 {
		t.Errorf("expected 25 runes for 0%%, got %d", len(runes0))
	}
}

func TestSeverityIcon(t *testing.T) {
	if severityIcon("critical") != "🔴" {
		t.Error("expected red for critical")
	}
	if severityIcon("healthy") != "🟢" {
		t.Error("expected green for healthy")
	}
}

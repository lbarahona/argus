package correlate

import (
	"context"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services []types.Service
	logs     map[string][]types.LogEntry  // service → logs
	traces   map[string][]types.TraceEntry // service → traces
}

func (m *mockQuerier) Health(ctx context.Context) (bool, time.Duration, error) {
	return true, time.Millisecond, nil
}

func (m *mockQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	logs := m.logs[service]
	if severityFilter != "" {
		var filtered []types.LogEntry
		for _, l := range logs {
			if l.SeverityText == severityFilter {
				filtered = append(filtered, l)
			}
		}
		logs = filtered
	}
	return &types.QueryResult{Logs: logs}, nil
}

func (m *mockQuerier) QueryTraces(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{Traces: m.traces[service]}, nil
}

func (m *mockQuerier) QueryMetrics(ctx context.Context, metricName string, durationMinutes int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func baseTime() time.Time {
	return time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
}

func TestRunCollectsSignals(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api-gateway", NumCalls: 1000, NumErrors: 50},
			{Name: "user-service", NumCalls: 500, NumErrors: 10},
		},
		logs: map[string][]types.LogEntry{
			"api-gateway": {
				{Timestamp: baseTime(), Body: "connection refused to user-service", SeverityText: "error", ServiceName: "api-gateway"},
			},
			"user-service": {
				{Timestamp: baseTime().Add(2 * time.Second), Body: "database timeout", SeverityText: "error", ServiceName: "user-service"},
			},
		},
		traces: map[string][]types.TraceEntry{
			"api-gateway": {
				{Timestamp: baseTime().Add(1 * time.Second), ServiceName: "api-gateway", OperationName: "GET /users", DurationNano: 5_000_000_000, StatusCode: "ERROR"},
			},
		},
	}

	result, err := Run(context.Background(), mock, "test", Options{Duration: 30, BucketSize: 60, MinEvents: 2})
	require.NoError(t, err)

	assert.Equal(t, 2, len(result.Services))
	assert.GreaterOrEqual(t, len(result.Signals), 3)
}

func TestFindClusters(t *testing.T) {
	base := baseTime()

	// Signals clustered together
	signals := []Signal{
		{Timestamp: base, Service: "svc-a", IsError: true, Source: "logs", Summary: "err1"},
		{Timestamp: base.Add(5 * time.Second), Service: "svc-b", IsError: true, Source: "logs", Summary: "err2"},
		{Timestamp: base.Add(10 * time.Second), Service: "svc-a", IsError: false, Source: "traces", Summary: "slow"},
		{Timestamp: base.Add(15 * time.Second), Service: "svc-c", IsError: true, Source: "logs", Summary: "err3"},
		// Gap
		{Timestamp: base.Add(5 * time.Minute), Service: "svc-a", IsError: false, Source: "logs", Summary: "ok"},
	}

	clusters := findClusters(signals, 60, 3)
	require.Len(t, clusters, 1)

	c := clusters[0]
	assert.Len(t, c.Signals, 4)
	assert.Equal(t, 3, len(c.Services))
	assert.Equal(t, 3, c.Errors) // svc-a err + svc-b err + svc-c err
}

func TestFindClustersNoCluster(t *testing.T) {
	base := baseTime()
	signals := []Signal{
		{Timestamp: base, Service: "svc-a", IsError: true},
		{Timestamp: base.Add(5 * time.Minute), Service: "svc-b", IsError: true},
	}

	clusters := findClusters(signals, 60, 3)
	assert.Len(t, clusters, 0)
}

func TestDetectPropagation(t *testing.T) {
	base := baseTime()
	signals := []Signal{
		{Timestamp: base, Service: "db", IsError: true},
		{Timestamp: base.Add(100 * time.Millisecond), Service: "api", IsError: true},
		{Timestamp: base.Add(1 * time.Second), Service: "db", IsError: true},
		{Timestamp: base.Add(1100 * time.Millisecond), Service: "api", IsError: true},
		{Timestamp: base.Add(2 * time.Second), Service: "db", IsError: true},
		{Timestamp: base.Add(2100 * time.Millisecond), Service: "api", IsError: true},
	}

	edges := detectPropagation(signals, 60)
	require.NotEmpty(t, edges)

	// Should find db → api propagation
	found := false
	for _, e := range edges {
		if e.From == "db" && e.To == "api" {
			found = true
			assert.GreaterOrEqual(t, e.Count, 2)
			break
		}
	}
	assert.True(t, found, "expected db → api propagation edge")
}

func TestDetectPropagationIgnoresSameService(t *testing.T) {
	base := baseTime()
	signals := []Signal{
		{Timestamp: base, Service: "api", IsError: true},
		{Timestamp: base.Add(100 * time.Millisecond), Service: "api", IsError: true},
	}

	edges := detectPropagation(signals, 60)
	assert.Empty(t, edges)
}

func TestRenderMarkdown(t *testing.T) {
	r := &Result{
		TimeRange: 30 * time.Minute,
		Services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 5},
		},
		Signals: []Signal{
			{Timestamp: baseTime(), Service: "api", IsError: true, Source: "logs", Summary: "timeout"},
		},
		Clusters: []Cluster{
			{
				Start:    baseTime(),
				End:      baseTime().Add(10 * time.Second),
				Signals:  []Signal{{Service: "api", IsError: true}},
				Services: map[string]int{"api": 1},
				Errors:   1,
				Score:    45,
			},
		},
		Propagation: []PropagationEdge{
			{From: "db", To: "api", Count: 5, DelayMs: 150},
		},
	}

	md := RenderMarkdown(r)
	assert.Contains(t, md, "Cross-Signal Correlation")
	assert.Contains(t, md, "api")
	assert.Contains(t, md, "mermaid")
	assert.Contains(t, md, "db")
}

func TestRunServiceNotFound(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{{Name: "existing"}},
		logs:     map[string][]types.LogEntry{},
		traces:   map[string][]types.TraceEntry{},
	}

	_, err := Run(context.Background(), mock, "test", Options{Service: "nonexistent", Duration: 30})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestClusterScoring(t *testing.T) {
	base := baseTime()

	// High severity: multiple services, all errors, many signals
	signals := make([]Signal, 20)
	for i := range signals {
		svc := "svc-a"
		if i%3 == 1 {
			svc = "svc-b"
		} else if i%3 == 2 {
			svc = "svc-c"
		}
		signals[i] = Signal{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Service:   svc,
			IsError:   true,
			Source:     "logs",
		}
	}

	clusters := findClusters(signals, 60, 3)
	require.Len(t, clusters, 1)
	assert.Greater(t, clusters[0].Score, 80.0, "multi-service all-error cluster should score high")
}

func TestBuildAIPrompt(t *testing.T) {
	r := &Result{
		TimeRange: 30 * time.Minute,
		Services:  []types.Service{{Name: "api", NumCalls: 100}},
		Signals: []Signal{
			{Timestamp: baseTime(), Service: "api", Source: "logs", Summary: "error", IsError: true},
		},
	}

	prompt := BuildAIPrompt(r)
	assert.Contains(t, prompt, "cross-signal correlation")
	assert.Contains(t, prompt, "Root Cause Chain")
	assert.Contains(t, prompt, "Signal Timeline")
}

func TestSanitizeMermaid(t *testing.T) {
	assert.Equal(t, "my_service_v1", sanitizeMermaid("my-service.v1"))
}

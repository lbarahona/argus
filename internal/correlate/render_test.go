package correlate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ──────────────────────────────────────────────
// Tests: Render (terminal output)
// ──────────────────────────────────────────────

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestRender_WithClusters(t *testing.T) {
	base := baseTime()
	r := &Result{
		TimeRange: 30 * time.Minute,
		Services: []types.Service{
			{Name: "api-gateway", NumCalls: 1000, NumErrors: 50, ErrorRate: 6.0},
			{Name: "user-service", NumCalls: 500, NumErrors: 3, ErrorRate: 0.6},
			{Name: "db-service", NumCalls: 200, NumErrors: 0, ErrorRate: 0},
		},
		Signals: []Signal{
			{Timestamp: base, Service: "api-gateway", Source: "logs", Summary: "connection refused", IsError: true},
			{Timestamp: base.Add(1 * time.Second), Service: "api-gateway", Source: "traces", Summary: "GET /users [status:ERROR] [5000ms]", IsError: true, DurationMs: 5000},
			{Timestamp: base.Add(2 * time.Second), Service: "user-service", Source: "logs", Summary: "database timeout", IsError: true},
			{Timestamp: base.Add(3 * time.Second), Service: "db-service", Source: "traces", Summary: "slow query", IsError: false, DurationMs: 3000},
		},
		Clusters: []Cluster{
			{
				Start: base,
				End:   base.Add(3 * time.Second),
				Signals: []Signal{
					{Timestamp: base, Service: "api-gateway", Source: "logs", Summary: "connection refused", IsError: true},
					{Timestamp: base.Add(1 * time.Second), Service: "api-gateway", Source: "traces", Summary: "GET /users [ERROR]", IsError: true},
					{Timestamp: base.Add(2 * time.Second), Service: "user-service", Source: "logs", Summary: "database timeout", IsError: true},
					{Timestamp: base.Add(3 * time.Second), Service: "db-service", Source: "traces", Summary: "slow query", IsError: false},
				},
				Services: map[string]int{"api-gateway": 2, "user-service": 1, "db-service": 1},
				Errors:   3,
				Score:    75.0,
			},
		},
		Propagation: []PropagationEdge{
			{From: "api-gateway", To: "user-service", Count: 5, DelayMs: 200},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "Cross-Signal Correlation")
	assert.Contains(t, output, "Service Health")
	assert.Contains(t, output, "api-gateway")
	assert.Contains(t, output, "🔴") // high error rate
	assert.Contains(t, output, "Event Clusters")
	assert.Contains(t, output, "CRITICAL") // score >= 60
	assert.Contains(t, output, "Error Propagation")
	assert.Contains(t, output, "api-gateway → user-service")
}

func TestRender_NoClusters(t *testing.T) {
	r := &Result{
		TimeRange: 15 * time.Minute,
		Services: []types.Service{
			{Name: "healthy-service", NumCalls: 500, NumErrors: 0, ErrorRate: 0},
		},
		Signals:  nil,
		Clusters: nil,
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "Cross-Signal Correlation")
	assert.Contains(t, output, "system looks quiet")
	assert.Contains(t, output, "✅")
}

func TestRender_MediumScore(t *testing.T) {
	base := baseTime()
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services:  []types.Service{{Name: "svc", NumCalls: 100, NumErrors: 2, ErrorRate: 2.0}},
		Signals:   []Signal{{Timestamp: base, Service: "svc"}},
		Clusters: []Cluster{
			{
				Start:    base,
				End:      base.Add(5 * time.Second),
				Signals:  []Signal{{Timestamp: base, Service: "svc", Source: "logs", Summary: "err", IsError: true}},
				Services: map[string]int{"svc": 1},
				Errors:   1,
				Score:    45.0,
			},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "MEDIUM")
}

func TestRender_LowScore(t *testing.T) {
	base := baseTime()
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services:  []types.Service{{Name: "svc", NumCalls: 100, NumErrors: 1, ErrorRate: 1.0}},
		Signals:   []Signal{{Timestamp: base, Service: "svc"}},
		Clusters: []Cluster{
			{
				Start:    base,
				End:      base.Add(5 * time.Second),
				Signals:  []Signal{{Timestamp: base, Service: "svc", Source: "logs", Summary: "minor", IsError: false}},
				Services: map[string]int{"svc": 1},
				Errors:   0,
				Score:    15.0,
			},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "LOW")
}

func TestRender_ManySignalsInCluster(t *testing.T) {
	base := baseTime()
	signals := make([]Signal, 10)
	for i := range signals {
		signals[i] = Signal{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Service:   fmt.Sprintf("svc-%d", i%3),
			Source:    "logs",
			Summary:   fmt.Sprintf("error %d", i),
			IsError:   true,
		}
	}

	r := &Result{
		TimeRange: 10 * time.Minute,
		Services:  []types.Service{{Name: "svc-0"}, {Name: "svc-1"}, {Name: "svc-2"}},
		Signals:   signals,
		Clusters: []Cluster{
			{
				Start:    base,
				End:      base.Add(9 * time.Second),
				Signals:  signals,
				Services: map[string]int{"svc-0": 4, "svc-1": 3, "svc-2": 3},
				Errors:   10,
				Score:    80.0,
			},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	// Should show "and N more signals" for clusters with >5 signals
	assert.Contains(t, output, "and 5 more signals")
}

func TestRender_ServiceHealthIcons(t *testing.T) {
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services: []types.Service{
			{Name: "healthy", NumCalls: 100, NumErrors: 0, ErrorRate: 0},
			{Name: "degraded", NumCalls: 100, NumErrors: 2, ErrorRate: 2.0},
			{Name: "critical", NumCalls: 100, NumErrors: 10, ErrorRate: 10.0},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "✅") // healthy
	assert.Contains(t, output, "🟡") // degraded (>1%)
	assert.Contains(t, output, "🔴") // critical (>5%)
}

func TestRender_ErrorRateFromCounts(t *testing.T) {
	// When ErrorRate is 0 but there are errors, it should calculate from counts
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services: []types.Service{
			{Name: "svc", NumCalls: 100, NumErrors: 10, ErrorRate: 0},
		},
	}

	output := captureStdout(func() {
		Render(r)
	})

	assert.Contains(t, output, "10.00%")
}

// ──────────────────────────────────────────────
// Tests: RenderMarkdown edge cases
// ──────────────────────────────────────────────

func TestRenderMarkdown_NoClusters(t *testing.T) {
	r := &Result{
		TimeRange: 15 * time.Minute,
		Services:  []types.Service{{Name: "api", NumCalls: 100, NumErrors: 0}},
	}

	md := RenderMarkdown(r)
	assert.Contains(t, md, "Cross-Signal Correlation")
	assert.Contains(t, md, "✅ Healthy")
	assert.NotContains(t, md, "Event Clusters")
}

func TestRenderMarkdown_AllSeverities(t *testing.T) {
	base := baseTime()
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services:  []types.Service{{Name: "svc"}},
		Clusters: []Cluster{
			{Start: base, End: base.Add(time.Second), Signals: []Signal{{}}, Services: map[string]int{"a": 1}, Score: 80},
			{Start: base, End: base.Add(time.Second), Signals: []Signal{{}}, Services: map[string]int{"b": 1}, Score: 40},
			{Start: base, End: base.Add(time.Second), Signals: []Signal{{}}, Services: map[string]int{"c": 1}, Score: 10},
		},
	}

	md := RenderMarkdown(r)
	assert.Contains(t, md, "🔴 CRITICAL")
	assert.Contains(t, md, "🟡 MEDIUM")
	assert.Contains(t, md, "🟢 LOW")
}

func TestRenderMarkdown_DegradedService(t *testing.T) {
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 3, ErrorRate: 3.0},
		},
	}

	md := RenderMarkdown(r)
	assert.Contains(t, md, "🟡 Degraded")
}

func TestRenderMarkdown_CriticalService(t *testing.T) {
	r := &Result{
		TimeRange: 10 * time.Minute,
		Services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 10, ErrorRate: 10.0},
		},
	}

	md := RenderMarkdown(r)
	assert.Contains(t, md, "🔴 Critical")
}

// ──────────────────────────────────────────────
// Tests: RunWithAI
// ──────────────────────────────────────────────

type failQuerier struct {
	mockQuerier
}

func (f *failQuerier) ListServices(ctx context.Context) ([]types.Service, error) {
	return nil, fmt.Errorf("connection refused")
}

func TestRunWithAI_ListServicesFails(t *testing.T) {
	q := &failQuerier{}
	opts := Options{Duration: 30}

	err := RunWithAI(context.Background(), q, "test", opts, os.Stdout)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing services")
}

func TestRunWithAI_NoSignals(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "healthy-svc", NumCalls: 100, NumErrors: 0},
		},
		logs:   map[string][]types.LogEntry{},
		traces: map[string][]types.TraceEntry{},
	}

	output := captureStdout(func() {
		err := RunWithAI(context.Background(), mock, "test", Options{Duration: 30}, os.Stdout)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "No signals to analyze")
}

// ──────────────────────────────────────────────
// Tests: findClusters edge cases
// ──────────────────────────────────────────────

func TestFindClustersEmpty(t *testing.T) {
	clusters := findClusters(nil, 60, 3)
	assert.Nil(t, clusters)
}

func TestFindClustersMultipleBuckets(t *testing.T) {
	base := baseTime()
	// Two separate clusters
	signals := []Signal{
		{Timestamp: base, Service: "a", IsError: true},
		{Timestamp: base.Add(5 * time.Second), Service: "b", IsError: true},
		{Timestamp: base.Add(10 * time.Second), Service: "c", IsError: true},
		{Timestamp: base.Add(15 * time.Second), Service: "d", IsError: false},
		// gap
		{Timestamp: base.Add(5 * time.Minute), Service: "x", IsError: true},
		{Timestamp: base.Add(5*time.Minute + 5*time.Second), Service: "y", IsError: true},
		{Timestamp: base.Add(5*time.Minute + 10*time.Second), Service: "z", IsError: true},
	}

	clusters := findClusters(signals, 60, 3)
	assert.Len(t, clusters, 2)
	// Should be sorted by score descending
	assert.GreaterOrEqual(t, clusters[0].Score, clusters[1].Score)
}

// ──────────────────────────────────────────────
// Tests: detectPropagation edge cases
// ──────────────────────────────────────────────

func TestDetectPropagationEmpty(t *testing.T) {
	assert.Nil(t, detectPropagation(nil, 60))
	assert.Nil(t, detectPropagation([]Signal{{IsError: true}}, 60))
}

func TestDetectPropagationFiltersLowCount(t *testing.T) {
	base := baseTime()
	// Only 1 correlated event — should be filtered out
	signals := []Signal{
		{Timestamp: base, Service: "a", IsError: true},
		{Timestamp: base.Add(100 * time.Millisecond), Service: "b", IsError: true},
	}

	edges := detectPropagation(signals, 60)
	assert.Empty(t, edges)
}

// ──────────────────────────────────────────────
// Tests: Run edge cases
// ──────────────────────────────────────────────

func TestRunDefaultBucketAndMinEvents(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{{Name: "svc", NumCalls: 100}},
		logs:     map[string][]types.LogEntry{},
		traces:   map[string][]types.TraceEntry{},
	}

	// BucketSize and MinEvents should default to 60 and 3
	result, err := Run(context.Background(), mock, "test", Options{Duration: 15})
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestRunWithServiceFilter(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 100},
			{Name: "web", NumCalls: 50},
		},
		logs:   map[string][]types.LogEntry{},
		traces: map[string][]types.TraceEntry{},
	}

	result, err := Run(context.Background(), mock, "test", Options{Duration: 15, Service: "api"})
	require.NoError(t, err)
	// Should only collect from api, not web
	assert.Equal(t, 2, len(result.Services)) // all services listed
}

func TestRunTracesSlowNotError(t *testing.T) {
	mock := &mockQuerier{
		services: []types.Service{{Name: "api", NumCalls: 100}},
		logs:     map[string][]types.LogEntry{},
		traces: map[string][]types.TraceEntry{
			"api": {
				// Slow but not error
				{Timestamp: baseTime(), ServiceName: "api", OperationName: "GET /slow", DurationNano: 2_000_000_000, StatusCode: "OK"},
				// Fast and OK — should be skipped
				{Timestamp: baseTime().Add(time.Second), ServiceName: "api", OperationName: "GET /fast", DurationNano: 50_000_000, StatusCode: "OK"},
				// Error but fast
				{Timestamp: baseTime().Add(2 * time.Second), ServiceName: "api", OperationName: "POST /fail", DurationNano: 50_000_000, StatusCode: "ERROR"},
			},
		},
	}

	result, err := Run(context.Background(), mock, "test", Options{Duration: 15, BucketSize: 60, MinEvents: 1})
	require.NoError(t, err)

	// Should have 2 signals: slow trace and error trace (fast OK skipped)
	traceSignals := 0
	for _, s := range result.Signals {
		if s.Source == "traces" {
			traceSignals++
		}
	}
	assert.Equal(t, 2, traceSignals)
}

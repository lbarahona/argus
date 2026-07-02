package anomaly

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
	logs     *types.QueryResult
	traces   *types.QueryResult
	metrics  *types.QueryResult
}

func (m *mockQuerier) Health(_ context.Context) (bool, time.Duration, error) {
	return true, time.Millisecond, nil
}

func (m *mockQuerier) ListServices(_ context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(_ context.Context, _ string, _, _ int, _ string) (*types.QueryResult, error) {
	return m.logs, nil
}

func (m *mockQuerier) QueryTraces(_ context.Context, _ string, _, _ int) (*types.QueryResult, error) {
	return m.traces, nil
}

func (m *mockQuerier) QueryMetrics(_ context.Context, _ string, _ int) (*types.QueryResult, error) {
	return m.metrics, nil
}

func TestDetect_NoServices(t *testing.T) {
	q := &mockQuerier{}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60})
	require.NoError(t, err)
	assert.Equal(t, 0, len(result.Anomalies))
	assert.Equal(t, 0, result.TotalScanned)
}

func TestDetect_HealthyService(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 5, ErrorRate: 0.5},
		},
		logs:   &types.QueryResult{},
		traces: &types.QueryResult{},
	}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalScanned)
	assert.Equal(t, 0, len(result.Anomalies))
}

func TestDetect_HighErrorRate(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "payments", NumCalls: 100, NumErrors: 15, ErrorRate: 15.0},
		},
		logs:   &types.QueryResult{},
		traces: &types.QueryResult{},
	}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60})
	require.NoError(t, err)
	require.True(t, len(result.Anomalies) > 0)
	assert.Equal(t, SeverityCritical, result.Anomalies[0].Severity)
	assert.Equal(t, TypeErrorRate, result.Anomalies[0].Type)
	assert.Equal(t, "payments", result.Anomalies[0].Service)
}

func TestDetect_ModerateErrorRate(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "auth", NumCalls: 200, NumErrors: 12, ErrorRate: 6.0},
		},
		logs:   &types.QueryResult{},
		traces: &types.QueryResult{},
	}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60})
	require.NoError(t, err)
	require.True(t, len(result.Anomalies) > 0)
	assert.Equal(t, SeverityWarning, result.Anomalies[0].Severity)
}

func TestDetect_ServiceFilter(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 1, ErrorRate: 1.0},
			{Name: "payments", NumCalls: 100, NumErrors: 20, ErrorRate: 20.0},
		},
		logs:   &types.QueryResult{},
		traces: &types.QueryResult{},
	}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60, Service: "api"})
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalScanned)
}

func TestDetect_ServiceNotFound(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "api", NumCalls: 100, NumErrors: 1},
		},
	}
	_, err := Detect(context.Background(), q, "test", Options{Duration: 60, Service: "nonexistent"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDetectLogAnomalies_Spike(t *testing.T) {
	now := time.Now()
	var logs []types.LogEntry

	// Normal buckets: 1-2 errors per 5min
	for i := 0; i < 6; i++ {
		ts := now.Add(-time.Duration(i*5+1) * time.Minute)
		logs = append(logs, types.LogEntry{Timestamp: ts, SeverityText: "ERROR", Body: "normal error"})
		logs = append(logs, types.LogEntry{Timestamp: ts, SeverityText: "INFO", Body: "ok"})
	}

	// Spike bucket: 20 errors in one 5min window
	spikeTime := now.Add(-31 * time.Minute)
	for i := 0; i < 20; i++ {
		logs = append(logs, types.LogEntry{Timestamp: spikeTime, SeverityText: "ERROR", Body: "spike error"})
	}

	anomalies := detectLogAnomalies("test-svc", logs, Options{Sensitivity: 2.0})
	found := false
	for _, a := range anomalies {
		if a.Type == TypeSpike {
			found = true
			break
		}
	}
	assert.True(t, found, "should detect error spike")
}

func TestDetectLatencyAnomalies_HighP99Ratio(t *testing.T) {
	var traces []types.TraceEntry

	// Normal traces: ~10ms
	for i := 0; i < 95; i++ {
		traces = append(traces, types.TraceEntry{
			OperationName: "GET /api/users",
			DurationNano:  10 * int64(time.Millisecond),
		})
	}

	// Outlier traces: 5000ms (p99 will be very high)
	for i := 0; i < 5; i++ {
		traces = append(traces, types.TraceEntry{
			OperationName: "GET /api/users",
			DurationNano:  5000 * int64(time.Millisecond),
		})
	}

	anomalies := detectLatencyAnomalies("test-svc", traces, Options{Sensitivity: 2.0})
	assert.True(t, len(anomalies) > 0, "should detect latency anomalies")
}

func TestMeanStdDev(t *testing.T) {
	values := []float64{2, 4, 4, 4, 5, 5, 7, 9}
	mean, stddev := meanStdDev(values)
	assert.InDelta(t, 5.0, mean, 0.01)
	assert.InDelta(t, 2.0, stddev, 0.01)
}

func TestMeanStdDev_Empty(t *testing.T) {
	mean, stddev := meanStdDev(nil)
	assert.Equal(t, 0.0, mean)
	assert.Equal(t, 0.0, stddev)
}

func TestPercentiles(t *testing.T) {
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i + 1)
	}
	p50, p95, p99 := percentiles(values)
	assert.Equal(t, 51.0, p50)
	assert.Equal(t, 96.0, p95)
	assert.Equal(t, 100.0, p99)
}

func TestDetectTrend_Increasing(t *testing.T) {
	values := make([]float64, 20)
	for i := range values {
		if i < 10 {
			values[i] = 10
		} else {
			values[i] = 20
		}
	}
	trend := detectTrend(values)
	assert.True(t, trend > 0, "should detect increasing trend")
}

func TestDetectTrend_Stable(t *testing.T) {
	values := make([]float64, 20)
	for i := range values {
		values[i] = 10
	}
	trend := detectTrend(values)
	assert.Equal(t, 0.0, trend)
}

func TestDetectTrend_TooFew(t *testing.T) {
	values := []float64{1, 2, 3}
	trend := detectTrend(values)
	assert.Equal(t, 0.0, trend)
}

func TestNormalizeLogBody(t *testing.T) {
	input := "Error processing order 12345 for user abc-def"
	result := normalizeLogBody(input)
	assert.NotContains(t, result, "12345")
	assert.Contains(t, result, "#####")
}

func TestSeverityRank(t *testing.T) {
	assert.True(t, severityRank(SeverityCritical) > severityRank(SeverityWarning))
	assert.True(t, severityRank(SeverityWarning) > severityRank(SeverityInfo))
	assert.True(t, severityRank(SeverityInfo) > severityRank(""))
}

// TestDetect_EqualSeverityAnomaliesSortByService pins the tiebreaker used
// when sorting anomalies of equal severity: they must be ordered by
// service name so output is stable across runs, not left in whatever
// order the (non-deterministic) detection passes happened to append them.
func TestDetect_EqualSeverityAnomaliesSortByService(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "zebra", NumCalls: 100, NumErrors: 15, ErrorRate: 15.0},
			{Name: "alpha", NumCalls: 100, NumErrors: 15, ErrorRate: 15.0},
			{Name: "mike", NumCalls: 100, NumErrors: 15, ErrorRate: 15.0},
		},
		logs:   &types.QueryResult{},
		traces: &types.QueryResult{},
	}
	result, err := Detect(context.Background(), q, "test", Options{Duration: 60})
	require.NoError(t, err)
	require.Len(t, result.Anomalies, 3)

	for _, a := range result.Anomalies {
		assert.Equal(t, SeverityCritical, a.Severity, "all anomalies in this fixture should be critical")
	}

	var services []string
	for _, a := range result.Anomalies {
		services = append(services, a.Service)
	}
	assert.Equal(t, []string{"alpha", "mike", "zebra"}, services, "equal-severity anomalies should be sorted by service name")
}

func TestHighestSeverity(t *testing.T) {
	anomalies := []Anomaly{
		{Service: "api", Severity: SeverityInfo},
		{Service: "api", Severity: SeverityCritical},
		{Service: "api", Severity: SeverityWarning},
		{Service: "other", Severity: SeverityCritical},
	}
	assert.Equal(t, SeverityCritical, highestSeverity(anomalies, "api"))
	assert.Equal(t, SeverityCritical, highestSeverity(anomalies, "other"))
	assert.Equal(t, "", highestSeverity(anomalies, "missing"))
}

package correlate

import (
	"context"
	"fmt"
	"testing"
	"time"

	amlib "github.com/lbarahona/argus/internal/alertmanager"
	grafanalib "github.com/lbarahona/argus/internal/grafana"
	lokilib "github.com/lbarahona/argus/internal/loki"
	promlib "github.com/lbarahona/argus/internal/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubAMClient struct {
	alerts []amlib.Alert
	err    error
}

func (s stubAMClient) ListAlerts(ctx context.Context, opts *amlib.AlertListOptions) ([]amlib.Alert, error) {
	return s.alerts, s.err
}

type stubPromClient struct {
	alerts *promlib.AlertsData
	err    error
}

func (s stubPromClient) Alerts(ctx context.Context) (*promlib.AlertsData, error) {
	return s.alerts, s.err
}

type stubGrafanaClient struct {
	alerts []grafanalib.GrafanaAlertInstance
	err    error
}

func (s stubGrafanaClient) AlertInstances(ctx context.Context) ([]grafanalib.GrafanaAlertInstance, error) {
	return s.alerts, s.err
}

type stubLokiClient struct {
	results map[string]*lokilib.QueryResult
	errFor  map[string]error
	queries []string
}

func (s *stubLokiClient) QueryRange(ctx context.Context, logQL string, start, end time.Time, limit int) (*lokilib.QueryResult, error) {
	s.queries = append(s.queries, logQL)
	if err := s.errFor[logQL]; err != nil {
		return nil, err
	}
	if result, ok := s.results[logQL]; ok {
		return result, nil
	}
	return &lokilib.QueryResult{Status: "success"}, nil
}

func TestRunStackCorrelatesAcrossTools(t *testing.T) {
	now := baseTime()
	loki := &stubLokiClient{
		results: map[string]*lokilib.QueryResult{
			`{service_name="payments"} |~ "(?i)(error|exception|timeout|fail|panic)"`: {
				Status: "success",
				Data: lokilib.ResultData{
					ResultType: "streams",
					Streams: []lokilib.Stream{{
						Labels: map[string]string{"service_name": "payments"},
						Values: [][]string{{fmt.Sprintf("%d", now.Add(-time.Minute).UnixNano()), "timeout talking to postgres"}},
					}},
				},
			},
		},
		errFor: map[string]error{},
	}

	result, err := RunStack(
		context.Background(),
		stubAMClient{alerts: []amlib.Alert{{
			Labels:      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "critical"},
			Annotations: map[string]string{"summary": "Payments are timing out"},
			Status:      amlib.AlertStatus{State: "active"},
			StartsAt:    now.Add(-5 * time.Minute),
		}}},
		stubPromClient{alerts: &promlib.AlertsData{Alerts: []promlib.PromAlert{{
			Labels:      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "critical"},
			Annotations: map[string]string{"summary": "Latency elevated"},
			State:       "firing",
			ActiveAt:    now.Add(-4 * time.Minute),
		}}}},
		stubGrafanaClient{alerts: []grafanalib.GrafanaAlertInstance{{
			Labels:      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "warning"},
			Annotations: map[string]string{"summary": "Grafana sees the same mess"},
			Status:      grafanalib.AlertInstanceStatus{State: "active"},
			StartsAt:    now.Add(-3 * time.Minute),
		}}},
		loki,
		StackOptions{Duration: 60, LogLimit: 20, SampleLimit: 2},
	)
	require.NoError(t, err)
	require.Len(t, result.Groups, 1)

	group := result.Groups[0]
	assert.Equal(t, "payments", group.Service)
	assert.Equal(t, "critical", group.Severity)
	assert.Equal(t, []string{"PaymentsHighLatency"}, group.AlertNames)
	assert.Equal(t, []string{"alertmanager", "grafana", "prometheus"}, group.Sources)
	require.Len(t, group.Logs, 1)
	assert.Contains(t, group.Logs[0].Line, "timeout")
	assert.NotZero(t, group.Score)
	assert.NotEmpty(t, loki.queries)
}

func TestRunStackFallsBackAcrossLokiLabelKeys(t *testing.T) {
	loki := &stubLokiClient{
		results: map[string]*lokilib.QueryResult{
			`{service="api"} |~ "(?i)(error|exception|timeout|fail|panic)"`: {
				Status: "success",
				Data: lokilib.ResultData{
					ResultType: "streams",
					Streams: []lokilib.Stream{{
						Labels: map[string]string{"service": "api"},
						Values: [][]string{{fmt.Sprintf("%d", baseTime().UnixNano()), "panic: bad things happened"}},
					}},
				},
			},
		},
		errFor: map[string]error{
			`{service_name="api"} |~ "(?i)(error|exception|timeout|fail|panic)"`: assert.AnError,
		},
	}

	result, err := RunStack(
		context.Background(),
		stubAMClient{alerts: []amlib.Alert{{
			Labels:      map[string]string{"alertname": "APIErrors", "service": "api", "severity": "warning"},
			Annotations: map[string]string{"summary": "API errors elevated"},
			Status:      amlib.AlertStatus{State: "active"},
		}}},
		stubPromClient{alerts: &promlib.AlertsData{}},
		stubGrafanaClient{},
		loki,
		StackOptions{Duration: 30, SampleLimit: 1},
	)
	require.NoError(t, err)
	require.Len(t, result.Groups, 1)
	require.Len(t, result.Groups[0].Logs, 1)
	assert.Len(t, loki.queries, 2)
	assert.Contains(t, loki.queries[1], `{service="api"}`)
}

func TestRunStackReturnsSourceErrors(t *testing.T) {
	_, err := RunStack(
		context.Background(),
		stubAMClient{err: assert.AnError},
		stubPromClient{alerts: &promlib.AlertsData{}},
		stubGrafanaClient{},
		nil,
		StackOptions{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Alertmanager")
}

func TestGroupSignalsByServiceAndAlertName(t *testing.T) {
	groups := groupSignals([]CorrelatedSignal{
		{Source: "alertmanager", Service: "checkout", Name: "HighErrorRate", Severity: "warning"},
		{Source: "prometheus", Service: "checkout", Name: "HighErrorRate", Severity: "critical"},
		{Source: "grafana", Service: "billing", Name: "HighErrorRate", Severity: "warning"},
	})
	require.Len(t, groups, 2)
	assert.Equal(t, "critical", groups[0].Severity)
}

func TestRunStackWithoutLokiStillWorks(t *testing.T) {
	result, err := RunStack(
		context.Background(),
		stubAMClient{alerts: []amlib.Alert{{
			Labels:      map[string]string{"alertname": "DBDown", "service": "db", "severity": "critical"},
			Annotations: map[string]string{"summary": "database down"},
			Status:      amlib.AlertStatus{State: "active"},
		}}},
		stubPromClient{alerts: &promlib.AlertsData{}},
		stubGrafanaClient{},
		nil,
		StackOptions{},
	)
	require.NoError(t, err)
	require.Len(t, result.Groups, 1)
	assert.Empty(t, result.Groups[0].Logs)
}

func TestRenderStack(t *testing.T) {
	out := RenderStack(&StackResult{
		Duration: time.Hour,
		Groups: []CorrelatedGroup{{
			Service:    "payments",
			Severity:   "critical",
			Score:      99,
			Sources:    []string{"alertmanager", "prometheus"},
			AlertNames: []string{"PaymentsHighLatency"},
			Signals: []CorrelatedSignal{{
				Source:   "alertmanager",
				Name:     "PaymentsHighLatency",
				State:    "active",
				StartsAt: baseTime(),
				Summary:  "timeouts everywhere",
			}},
			Logs: []lokilib.LogEntry{{Timestamp: baseTime(), Line: "error: upstream timeout"}},
		}},
	})
	assert.Contains(t, out, "Cross-tool correlation")
	assert.Contains(t, out, "payments")
	assert.Contains(t, out, "Loki samples")
}

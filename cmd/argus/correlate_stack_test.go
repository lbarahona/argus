package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCorrelateStackCmd_JSON(t *testing.T) {
	now := time.Date(2026, 4, 17, 4, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/alerts":
			json.NewEncoder(w).Encode([]map[string]any{{
				"labels":      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "critical"},
				"annotations": map[string]string{"summary": "payments timing out"},
				"status":      map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}},
				"startsAt":    now.Add(-5 * time.Minute).Format(time.RFC3339),
			}})
		case "/api/v1/alerts":
			json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"alerts": []map[string]any{{
				"labels":      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "critical"},
				"annotations": map[string]string{"summary": "prom sees latency too"},
				"state":       "firing",
				"activeAt":    now.Add(-4 * time.Minute).Format(time.RFC3339),
			}}}})
		case "/api/alertmanager/grafana/api/v2/alerts":
			json.NewEncoder(w).Encode([]map[string]any{{
				"labels":      map[string]string{"alertname": "PaymentsHighLatency", "service": "payments", "severity": "warning"},
				"annotations": map[string]string{"summary": "grafana agrees"},
				"status":      map[string]any{"state": "active", "silencedBy": []string{}, "inhibitedBy": []string{}},
				"startsAt":    now.Add(-3 * time.Minute).Format(time.RFC3339),
			}})
		case "/loki/api/v1/query_range":
			json.NewEncoder(w).Encode(map[string]any{
				"status": "success",
				"data": map[string]any{
					"resultType": "streams",
					"result": []map[string]any{{
						"stream": map[string]string{"service_name": "payments"},
						"values": [][]string{{"1776401640000000000", "error: upstream timeout"}},
					}},
				},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	setupTestConfig(t, &types.Config{
		Alertmanager: types.AlertmanagerConfig{URL: server.URL},
		Prometheus:   types.PrometheusConfig{URL: server.URL},
		Grafana:      types.GrafanaConfig{URL: server.URL},
		Loki:         types.LokiConfig{URL: server.URL},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	cmd := correlateStackCmd()
	cmd.SetArgs([]string{"--format", "json", "--samples", "1"})
	err := cmd.Execute()
	require.NoError(t, err)

	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "payments")
	assert.Contains(t, output, "alertmanager")
	assert.Contains(t, output, "upstream timeout")
}

func TestCorrelateStackCmd_RequiresLoki(t *testing.T) {
	setupTestConfig(t, &types.Config{
		Alertmanager: types.AlertmanagerConfig{URL: "http://example.com"},
		Prometheus:   types.PrometheusConfig{URL: "http://example.com"},
		Grafana:      types.GrafanaConfig{URL: "http://example.com"},
	})

	cmd := correlateStackCmd()
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loki is not configured")
}

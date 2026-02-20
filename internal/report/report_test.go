package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

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

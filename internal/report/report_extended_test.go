package report

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// ──────────────────────────────────────────────
// Tests: buildSummaryPrompt
// ──────────────────────────────────────────────

func TestBuildSummaryPrompt(t *testing.T) {
	r := &Report{
		Duration: 60,
		Health: []types.HealthStatus{
			{InstanceName: "prod", InstanceKey: "prod", URL: "https://signoz.example.com", Healthy: true, Latency: 50 * time.Millisecond},
		},
		Services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 50, ErrorRate: 5.0},
		},
		TotalCalls:  1000,
		TotalErrors: 50,
		TopErrors: []ServiceError{
			{Service: "api", Errors: 50, ErrorRate: 5.0},
		},
		ErrorPatterns: []ErrorPattern{
			{Pattern: "connection refused", Count: 10, Service: "api", Sample: "connection refused to database"},
		},
	}

	prompt := buildSummaryPrompt(r)

	if len(prompt) == 0 {
		t.Fatal("prompt should not be empty")
	}
	if !contains(prompt, "60 minutes") {
		t.Error("prompt should contain duration")
	}
	if !contains(prompt, "prod") {
		t.Error("prompt should contain instance name")
	}
	if !contains(prompt, "healthy") {
		t.Error("prompt should contain health status")
	}
	if !contains(prompt, "api") {
		t.Error("prompt should contain service name")
	}
	if !contains(prompt, "connection refused") {
		t.Error("prompt should contain error patterns")
	}
	if !contains(prompt, "actionable") {
		t.Error("prompt should ask for actionable recommendations")
	}
}

func TestBuildSummaryPromptUnhealthy(t *testing.T) {
	r := &Report{
		Duration: 30,
		Health: []types.HealthStatus{
			{InstanceName: "down", InstanceKey: "down", URL: "https://signoz.example.com", Healthy: false, Latency: 0, Message: "connection refused"},
		},
	}

	prompt := buildSummaryPrompt(r)
	if !contains(prompt, "unhealthy") {
		t.Error("prompt should indicate unhealthy status")
	}
	if !contains(prompt, "connection refused") {
		t.Error("prompt should include error message")
	}
}

func TestBuildSummaryPromptNoErrors(t *testing.T) {
	r := &Report{
		Duration: 15,
		Health: []types.HealthStatus{
			{InstanceName: "test", Healthy: true, Latency: 10 * time.Millisecond},
		},
		Services:    []types.Service{{Name: "web", NumCalls: 500}},
		TotalCalls:  500,
		TotalErrors: 0,
	}

	prompt := buildSummaryPrompt(r)
	if !contains(prompt, "15 minutes") {
		t.Error("should contain window")
	}
	// No top errors or patterns sections
	if contains(prompt, "Top error services") {
		t.Error("should not have top error services when none exist")
	}
}

// ──────────────────────────────────────────────
// Tests: RenderTerminal edge cases
// ──────────────────────────────────────────────

func TestRenderTerminal_WithAISummary(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    60,
		Instance:    "production",
		Health: []types.HealthStatus{
			{InstanceName: "prod", Healthy: true, Latency: 50 * time.Millisecond},
		},
		Services:    []types.Service{{Name: "api", NumCalls: 100, NumErrors: 5, ErrorRate: 5.0}},
		TotalCalls:  100,
		TotalErrors: 5,
		TopErrors:   []ServiceError{{Service: "api", Errors: 5, ErrorRate: 5.0}},
		ErrorPatterns: []ErrorPattern{
			{Pattern: "database timeout", Count: 3, Service: "api", Sample: "database timeout on connection pool"},
		},
		AISummary: "System is experiencing intermittent database timeouts. Recommend checking connection pool settings.",
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)
	output := buf.String()

	if !contains(output, "AI Assessment") {
		t.Error("should show AI Assessment section")
	}
	if !contains(output, "database timeouts") {
		t.Error("should contain AI summary text")
	}
	if !contains(output, "Error Patterns") {
		t.Error("should show Error Patterns section")
	}
}

func TestRenderTerminal_UnhealthyInstance(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    30,
		Instance:    "staging",
		Health: []types.HealthStatus{
			{InstanceName: "staging", URL: "https://signoz-staging.example.com", Healthy: false, Latency: 0},
		},
		TotalCalls:  0,
		TotalErrors: 0,
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)
	output := buf.String()

	if !contains(output, "🔴") {
		t.Error("unhealthy instance should show red icon")
	}
}

func TestRenderTerminal_ZeroCalls(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    15,
		Instance:    "empty",
		Health: []types.HealthStatus{
			{InstanceName: "empty", Healthy: true, Latency: 10 * time.Millisecond},
		},
		TotalCalls:  0,
		TotalErrors: 0,
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)
	output := buf.String()

	// Error rate should be 0% when no calls
	if !contains(output, "0.00%") {
		t.Error("should show 0% error rate")
	}
}

// ──────────────────────────────────────────────
// Tests: RenderMarkdown edge cases
// ──────────────────────────────────────────────

func TestRenderMarkdown_WithAISummary(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    60,
		Instance:    "production",
		Health: []types.HealthStatus{
			{InstanceName: "prod", Healthy: true, Latency: 50 * time.Millisecond},
		},
		TotalCalls:  1000,
		TotalErrors: 50,
		TopErrors: []ServiceError{
			{Service: "api", Errors: 50, ErrorRate: 5.0},
		},
		ErrorPatterns: []ErrorPattern{
			{Pattern: "timeout", Count: 10, Service: "api", Sample: "timeout waiting"},
		},
		AISummary: "The system is experiencing high error rates.",
	}

	var buf bytes.Buffer
	r.RenderMarkdown(&buf)
	output := buf.String()

	if !contains(output, "AI Assessment") {
		t.Error("should show AI Assessment")
	}
	if !contains(output, "Top Error Services") {
		t.Error("should show Top Error Services")
	}
	if !contains(output, "Error Patterns") {
		t.Error("should show Error Patterns")
	}
}

func TestRenderMarkdown_Unhealthy(t *testing.T) {
	r := &Report{
		GeneratedAt: time.Now(),
		Duration:    30,
		Instance:    "down",
		Health: []types.HealthStatus{
			{InstanceName: "down", Healthy: false, Latency: 0},
		},
	}

	var buf bytes.Buffer
	r.RenderMarkdown(&buf)
	output := buf.String()

	if !contains(output, "🔴") {
		t.Error("should show red icon for unhealthy")
	}
}

// ──────────────────────────────────────────────
// Tests: computeTopErrors edge cases
// ──────────────────────────────────────────────

func TestComputeTopErrors_MoreThan10(t *testing.T) {
	services := make([]types.Service, 15)
	for i := range services {
		services[i] = types.Service{
			Name:      "svc-" + string(rune('a'+i)),
			NumErrors: i + 1,
			ErrorRate: float64(i+1) * 0.5,
		}
	}

	top := computeTopErrors(services)
	if len(top) != 10 {
		t.Errorf("should cap at 10, got %d", len(top))
	}
	// Should be sorted by errors descending
	if top[0].Errors < top[1].Errors {
		t.Error("should be sorted by errors descending")
	}
}

func TestComputeTopErrors_AllZero(t *testing.T) {
	services := []types.Service{
		{Name: "a", NumErrors: 0},
		{Name: "b", NumErrors: 0},
	}

	top := computeTopErrors(services)
	if len(top) != 0 {
		t.Errorf("should return empty for zero-error services, got %d", len(top))
	}
}

// ──────────────────────────────────────────────
// Tests: detectPatterns edge cases
// ──────────────────────────────────────────────

func TestDetectPatterns_LongBody(t *testing.T) {
	longBody := ""
	for i := 0; i < 100; i++ {
		longBody += "error "
	}

	logs := []types.LogEntry{
		{Body: longBody, ServiceName: "api"},
	}

	patterns := detectPatterns(logs)
	if len(patterns) != 1 {
		t.Fatalf("expected 1 pattern, got %d", len(patterns))
	}
	// Pattern key is truncated to 80 chars
	if len(patterns[0].Pattern) > 80 {
		t.Error("pattern should be truncated to 80 chars")
	}
}

func TestDetectPatterns_EmptyBody(t *testing.T) {
	logs := []types.LogEntry{
		{Body: "", ServiceName: "api"},
		{Body: "   ", ServiceName: "api"},
	}

	patterns := detectPatterns(logs)
	if len(patterns) != 0 {
		t.Errorf("should skip empty bodies, got %d patterns", len(patterns))
	}
}

func TestDetectPatterns_MoreThan10(t *testing.T) {
	logs := make([]types.LogEntry, 15)
	for i := range logs {
		logs[i] = types.LogEntry{
			Body:        "unique error " + string(rune('a'+i)),
			ServiceName: "api",
		}
	}

	patterns := detectPatterns(logs)
	if len(patterns) > 10 {
		t.Errorf("should cap at 10, got %d", len(patterns))
	}
}

// ──────────────────────────────────────────────
// Tests: Generate edge cases
// ──────────────────────────────────────────────

func TestGenerate_HealthError(t *testing.T) {
	mock := &mockSignozClient{
		healthFunc: func(ctx context.Context) (bool, time.Duration, error) {
			return false, 0, nil
		},
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{
				{Name: "api", NumCalls: 100, NumErrors: 5, ErrorRate: 5.0},
			}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{}, nil
		},
	}

	r, err := Generate(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Health[0].Healthy {
		t.Error("should show unhealthy")
	}
	if r.TotalCalls != 100 {
		t.Errorf("expected 100 calls, got %d", r.TotalCalls)
	}
}

// ──────────────────────────────────────────────
// Helper
// ──────────────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && bytes.Contains([]byte(s), []byte(substr))
}

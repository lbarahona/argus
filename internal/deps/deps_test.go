package deps

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

// mockQuerier implements signoz.SignozQuerier for testing.
type mockQuerier struct {
	services []types.Service
	traces   map[string][]types.TraceEntry
}

func (m *mockQuerier) Health(_ context.Context) (bool, time.Duration, error) {
	return true, 0, nil
}

func (m *mockQuerier) ListServices(_ context.Context) ([]types.Service, error) {
	return m.services, nil
}

func (m *mockQuerier) QueryLogs(_ context.Context, _ string, _, _ int, _ string) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func (m *mockQuerier) QueryTraces(_ context.Context, service string, _, _ int) (*types.QueryResult, error) {
	traces, ok := m.traces[service]
	if !ok {
		return &types.QueryResult{}, nil
	}
	return &types.QueryResult{Traces: traces}, nil
}

func (m *mockQuerier) QueryMetrics(_ context.Context, _ string, _ int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func makeTrace(traceID, spanID, parentSpanID, service, op, status string, durNano int64) types.TraceEntry {
	return types.TraceEntry{
		Timestamp:     time.Now(),
		TraceID:       traceID,
		SpanID:        spanID,
		ParentSpanID:  parentSpanID,
		ServiceName:   service,
		OperationName: op,
		DurationNano:  durNano,
		StatusCode:    status,
	}
}

func TestGenerateBasicDeps(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "api-gateway"},
			{Name: "user-service"},
			{Name: "db-service"},
		},
		traces: map[string][]types.TraceEntry{
			"api-gateway": {
				makeTrace("t1", "s1", "", "api-gateway", "GET /users", "OK", 5000000),
				makeTrace("t1", "s2", "s1", "user-service", "GetUser", "OK", 3000000),
			},
			"user-service": {
				makeTrace("t1", "s2", "s1", "user-service", "GetUser", "OK", 3000000),
				makeTrace("t1", "s3", "s2", "db-service", "SELECT", "OK", 1000000),
			},
			"db-service": {},
		},
	}

	dm, err := Generate(context.Background(), Options{
		Querier:  q,
		Duration: 60,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dm.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes, got %d", len(dm.Nodes))
	}
	if len(dm.Edges) < 1 {
		t.Errorf("expected at least 1 edge, got %d", len(dm.Edges))
	}
}

func TestGenerateWithErrors(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "frontend"},
			{Name: "backend"},
		},
		traces: map[string][]types.TraceEntry{
			"frontend": {
				makeTrace("t1", "s1", "", "frontend", "GET /", "OK", 10000000),
				makeTrace("t1", "s2", "s1", "backend", "Process", "STATUS_CODE_ERROR", 5000000),
				makeTrace("t2", "s3", "", "frontend", "GET /", "OK", 8000000),
				makeTrace("t2", "s4", "s3", "backend", "Process", "OK", 4000000),
			},
			"backend": {},
		},
	}

	dm, err := Generate(context.Background(), Options{Querier: q, Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have edge from frontend to backend
	found := false
	for _, edge := range dm.Edges {
		if edge.From == "frontend" && edge.To == "backend" {
			found = true
			if edge.Calls != 2 {
				t.Errorf("expected 2 calls, got %d", edge.Calls)
			}
			if edge.Errors != 1 {
				t.Errorf("expected 1 error, got %d", edge.Errors)
			}
			if edge.ErrorRate != 50.0 {
				t.Errorf("expected 50%% error rate, got %.1f%%", edge.ErrorRate)
			}
		}
	}
	if !found {
		t.Error("expected edge from frontend to backend")
	}
}

func TestFilterForService(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "a"}, {Name: "b"}, {Name: "c"},
		},
		traces: map[string][]types.TraceEntry{
			"a": {
				makeTrace("t1", "s1", "", "a", "op", "OK", 1000000),
				makeTrace("t1", "s2", "s1", "b", "op", "OK", 1000000),
			},
			"b": {
				makeTrace("t2", "s3", "", "b", "op", "OK", 1000000),
				makeTrace("t2", "s4", "s3", "c", "op", "OK", 1000000),
			},
			"c": {},
		},
	}

	dm, err := Generate(context.Background(), Options{
		Querier:  q,
		Duration: 60,
		Service:  "b",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filtering for "b" should show a->b and b->c edges
	for _, edge := range dm.Edges {
		if edge.From != "b" && edge.To != "b" {
			t.Errorf("edge %s->%s doesn't involve service b", edge.From, edge.To)
		}
	}
}

func TestRootAndLeafDetection(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{
			{Name: "gateway"}, {Name: "api"}, {Name: "database"},
		},
		traces: map[string][]types.TraceEntry{
			"gateway": {
				makeTrace("t1", "s1", "", "gateway", "op", "OK", 1000000),
				makeTrace("t1", "s2", "s1", "api", "op", "OK", 1000000),
			},
			"api": {
				makeTrace("t1", "s2", "s1", "api", "op", "OK", 1000000),
				makeTrace("t1", "s3", "s2", "database", "op", "OK", 1000000),
			},
			"database": {},
		},
	}

	dm, err := Generate(context.Background(), Options{Querier: q, Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if node, ok := dm.Nodes["gateway"]; ok {
		if !node.IsRoot {
			t.Error("gateway should be a root node")
		}
	}
	if node, ok := dm.Nodes["database"]; ok {
		if !node.IsLeaf {
			t.Error("database should be a leaf node")
		}
	}
}

func TestEmptyTraces(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{{Name: "lonely-service"}},
		traces:   map[string][]types.TraceEntry{},
	}

	dm, err := Generate(context.Background(), Options{Querier: q, Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dm.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(dm.Edges))
	}
	// Isolated service should still appear as a node
	if _, ok := dm.Nodes["lonely-service"]; !ok {
		t.Error("lonely-service should appear as isolated node")
	}
}

func TestRenderTable(t *testing.T) {
	dm := &DependencyMap{
		GeneratedAt: time.Now(),
		Duration:    60,
		Nodes: map[string]*ServiceNode{
			"api":  {Name: "api", IsRoot: true, Downstream: []string{"db"}},
			"db":   {Name: "db", IsLeaf: true, Upstream: []string{"api"}},
		},
		Edges: []Edge{
			{From: "api", To: "db", Calls: 100, Errors: 5, ErrorRate: 5.0, AvgLatency: 12.3},
		},
	}

	var buf bytes.Buffer
	RenderTable(&buf, dm)
	out := buf.String()

	if !strings.Contains(out, "api") {
		t.Error("table should contain service name 'api'")
	}
	if !strings.Contains(out, "db") {
		t.Error("table should contain service name 'db'")
	}
	if !strings.Contains(out, "100") {
		t.Error("table should show call count")
	}
}

func TestRenderMarkdown(t *testing.T) {
	dm := &DependencyMap{
		GeneratedAt: time.Now(),
		Duration:    60,
		Nodes: map[string]*ServiceNode{
			"api": {Name: "api"},
			"db":  {Name: "db"},
		},
		Edges: []Edge{
			{From: "api", To: "db", Calls: 50, Errors: 2, ErrorRate: 4.0, AvgLatency: 8.5},
		},
	}

	var buf bytes.Buffer
	RenderMarkdown(&buf, dm)
	out := buf.String()

	if !strings.Contains(out, "mermaid") {
		t.Error("markdown should contain mermaid diagram")
	}
	if !strings.Contains(out, "graph LR") {
		t.Error("markdown should contain LR graph")
	}
	if !strings.Contains(out, "| api |") {
		t.Error("markdown should contain edge table")
	}
}

func TestSanitizeMermaidID(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"api-gateway", "api_gateway"},
		{"my.service", "my_service"},
		{"path/svc", "path_svc"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := sanitizeMermaidID(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeMermaidID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAppendUnique(t *testing.T) {
	s := appendUnique([]string{"a", "b"}, "b")
	if len(s) != 2 {
		t.Errorf("expected 2 items, got %d", len(s))
	}
	s = appendUnique(s, "c")
	if len(s) != 3 {
		t.Errorf("expected 3 items, got %d", len(s))
	}
}

func TestLatencyCalculation(t *testing.T) {
	q := &mockQuerier{
		services: []types.Service{{Name: "a"}, {Name: "b"}},
		traces: map[string][]types.TraceEntry{
			"a": {
				makeTrace("t1", "s1", "", "a", "op", "OK", 10000000),
				makeTrace("t1", "s2", "s1", "b", "op", "OK", 2000000), // 2ms
				makeTrace("t2", "s3", "", "a", "op", "OK", 10000000),
				makeTrace("t2", "s4", "s3", "b", "op", "OK", 8000000), // 8ms
			},
			"b": {},
		},
	}

	dm, err := Generate(context.Background(), Options{Querier: q, Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, edge := range dm.Edges {
		if edge.From == "a" && edge.To == "b" {
			// avg should be (2+8)/2 = 5ms
			if edge.AvgLatency < 4.9 || edge.AvgLatency > 5.1 {
				t.Errorf("expected avg latency ~5ms, got %.1fms", edge.AvgLatency)
			}
			if edge.P99Latency != 8.0 {
				t.Errorf("expected p99 latency 8ms, got %.1fms", edge.P99Latency)
			}
		}
	}
}

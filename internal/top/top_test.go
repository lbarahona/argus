package top

import (
	"bytes"
	"testing"
	"time"
)

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

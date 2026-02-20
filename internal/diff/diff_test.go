package diff

import (
	"bytes"
	"testing"
	"time"

	"github.com/lbarahona/argus/pkg/types"
)

func TestCountByService(t *testing.T) {
	now := time.Now()
	logs := []types.LogEntry{
		{ServiceName: "api", Timestamp: now.Add(-5 * time.Minute)},
		{ServiceName: "api", Timestamp: now.Add(-10 * time.Minute)},
		{ServiceName: "web", Timestamp: now.Add(-15 * time.Minute)},
		{ServiceName: "api", Timestamp: now.Add(-90 * time.Minute)}, // outside window
	}

	counts := countByService(logs, now.Add(-60*time.Minute), now)
	if counts["api"] != 2 {
		t.Errorf("expected 2 api errors in window, got %d", counts["api"])
	}
	if counts["web"] != 1 {
		t.Errorf("expected 1 web error, got %d", counts["web"])
	}
}

func TestRenderTerminal(t *testing.T) {
	r := &DiffResult{
		Instance:    "prod",
		WindowA:     "120-60 min ago",
		WindowB:     "0-60 min ago",
		DurationMin: 60,
		GeneratedAt: time.Now(),
		Services: []ServiceDiff{
			{Name: "api", ErrorsBefore: 10, ErrorsAfter: 25, ErrorsChange: 150, Status: "degraded"},
			{Name: "auth", ErrorsBefore: 20, ErrorsAfter: 5, ErrorsChange: -75, Status: "improved"},
		},
		Summary: DiffSummary{
			TotalErrorsBefore: 30,
			TotalErrorsAfter:  30,
			Degraded:          1,
			Improved:          1,
		},
	}

	var buf bytes.Buffer
	r.RenderTerminal(&buf)

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("ARGUS SERVICE DIFF")) {
		t.Error("expected diff header")
	}
	if !bytes.Contains([]byte(output), []byte("degraded")) {
		t.Error("expected degraded status")
	}
}

func TestStatusEmoji(t *testing.T) {
	if statusEmoji("degraded") != "🔴" {
		t.Error("wrong emoji for degraded")
	}
	if statusEmoji("improved") != "🟢" {
		t.Error("wrong emoji for improved")
	}
}

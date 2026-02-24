package output

import (
	"testing"
	"time"
)

func TestFormatSeverity(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"ERROR"},
		{"WARN"},
		{"INFO"},
		{"DEBUG"},
		{"UNKNOWN"},
		{""},
	}
	for _, tt := range tests {
		result := formatSeverity(tt.input)
		if result == "" {
			t.Errorf("formatSeverity(%q) should not be empty", tt.input)
		}
	}
}

func TestFormatTraceDuration(t *testing.T) {
	tests := []struct {
		ms       float64
		contains string
	}{
		{0.5, "µs"},
		{50, "ms"},
		{500, "ms"},
		{1500, "s"},
	}
	for _, tt := range tests {
		result := formatTraceDuration(tt.ms)
		if result == "" {
			t.Errorf("formatTraceDuration(%f) should not be empty", tt.ms)
		}
	}
}

func TestTruncateID(t *testing.T) {
	if truncateID("short") != "short" {
		t.Error("short ID should not be truncated")
	}
	long := "abcdefghijklmnop"
	result := truncateID(long)
	runes := []rune(result)
	if len(runes) > 13 { // 12 + "…"
		t.Errorf("long ID should be truncated, got %q (len %d runes)", result, len(runes))
	}
}

func TestMaskKey(t *testing.T) {
	if maskKey("short") != "****" {
		t.Errorf("short key should be masked as ****, got %q", maskKey("short"))
	}

	long := "abcdefghijklmnop"
	masked := maskKey(long)
	if masked != "abcd...mnop" {
		t.Errorf("expected abcd...mnop, got %q", masked)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		contains string
	}{
		{500 * time.Microsecond, "µs"},
		{50 * time.Millisecond, "ms"},
		{2 * time.Second, "ms"},
	}
	for _, tt := range tests {
		result := formatDuration(tt.d)
		if result == "" {
			t.Errorf("formatDuration(%v) should not be empty", tt.d)
		}
	}
}

func TestPrintLogsEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintLogs(nil)
}

func TestPrintServicesEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintServices(nil)
}

func TestPrintTracesEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintTraces(nil)
}

func TestPrintMetricsEmpty(t *testing.T) {
	// Should not panic with empty slice
	PrintMetrics(nil)
}

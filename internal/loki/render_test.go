package loki

import (
	"strings"
	"testing"
	"time"
)

func TestFormatJSON(t *testing.T) {
	result := FormatJSON(map[string]string{"key": "value"})
	if !strings.Contains(result, `"key"`) {
		t.Errorf("expected JSON output, got %s", result)
	}
}

func TestFormatJSON_Error(t *testing.T) {
	// channels can't be marshaled
	result := FormatJSON(make(chan int))
	if !strings.Contains(result, "error") {
		t.Errorf("expected error in output, got %s", result)
	}
}

func TestFormatLabels_Empty(t *testing.T) {
	result := FormatLabels(nil)
	if !strings.Contains(result, "no labels found") {
		t.Errorf("expected 'no labels found', got %s", result)
	}
}

func TestFormatLabels(t *testing.T) {
	result := FormatLabels([]string{"pod", "app", "namespace"})
	if !strings.Contains(result, "Labels") {
		t.Error("expected Labels header")
	}
	if !strings.Contains(result, "(3)") {
		t.Error("expected count (3)")
	}
	// Should be sorted
	appIdx := strings.Index(result, "app")
	nsIdx := strings.Index(result, "namespace")
	podIdx := strings.Index(result, "pod")
	if appIdx > nsIdx || nsIdx > podIdx {
		t.Error("expected sorted label order: app, namespace, pod")
	}
}

func TestFormatLabelValues_Empty(t *testing.T) {
	result := FormatLabelValues("app", nil)
	if !strings.Contains(result, "no values") {
		t.Errorf("expected 'no values', got %s", result)
	}
}

func TestFormatLabelValues(t *testing.T) {
	result := FormatLabelValues("app", []string{"nginx", "api", "worker"})
	if !strings.Contains(result, "Values for app") {
		t.Error("expected 'Values for app'")
	}
	if !strings.Contains(result, "(3)") {
		t.Error("expected count (3)")
	}
	if !strings.Contains(result, "nginx") {
		t.Error("expected nginx in output")
	}
}

func TestFormatLogEntries_Empty(t *testing.T) {
	result := FormatLogEntries(nil, false)
	if !strings.Contains(result, "no log entries found") {
		t.Errorf("expected 'no log entries found', got %s", result)
	}
}

func TestFormatLogEntries(t *testing.T) {
	entries := []LogEntry{
		{
			Timestamp: time.Unix(1700000001, 0),
			Line:      "normal log line",
			Labels:    map[string]string{"app": "test"},
		},
		{
			Timestamp: time.Unix(1700000002, 0),
			Line:      "ERROR: something failed",
			Labels:    map[string]string{"app": "test"},
		},
		{
			Timestamp: time.Unix(1700000003, 0),
			Line:      "WARNING: disk space low",
			Labels:    map[string]string{"app": "test"},
		},
	}

	result := FormatLogEntries(entries, false)
	if !strings.Contains(result, "Log Entries") {
		t.Error("expected Log Entries header")
	}
	if !strings.Contains(result, "(3)") {
		t.Error("expected count (3)")
	}
	if !strings.Contains(result, "something failed") {
		t.Error("expected error line in output")
	}
}

func TestFormatLogEntries_WithLabels(t *testing.T) {
	entries := []LogEntry{
		{
			Timestamp: time.Unix(1700000001, 0),
			Line:      "test",
			Labels:    map[string]string{"app": "nginx"},
		},
	}

	result := FormatLogEntries(entries, true)
	if !strings.Contains(result, "nginx") {
		t.Error("expected label in output when showLabels=true")
	}
}

func TestFormatLogEntries_TruncateLong(t *testing.T) {
	longLine := strings.Repeat("x", 300)
	entries := []LogEntry{
		{
			Timestamp: time.Unix(1700000001, 0),
			Line:      longLine,
			Labels:    map[string]string{},
		},
	}

	result := FormatLogEntries(entries, false)
	if strings.Contains(result, longLine) {
		t.Error("expected long line to be truncated")
	}
	if !strings.Contains(result, "...") {
		t.Error("expected truncation marker ...")
	}
}

func TestFormatSeries_Empty(t *testing.T) {
	result := FormatSeries(nil)
	if !strings.Contains(result, "no matching series") {
		t.Errorf("expected 'no matching series', got %s", result)
	}
}

func TestFormatSeries(t *testing.T) {
	series := []map[string]string{
		{"app": "nginx", "namespace": "prod"},
		{"app": "api", "namespace": "staging"},
	}
	result := FormatSeries(series)
	if !strings.Contains(result, "Series") {
		t.Error("expected Series header")
	}
	if !strings.Contains(result, "(2)") {
		t.Error("expected count (2)")
	}
}

func TestFormatSeries_Truncate(t *testing.T) {
	// Create > 50 series
	series := make([]map[string]string, 60)
	for i := range series {
		series[i] = map[string]string{"i": string(rune('a' + i%26))}
	}
	result := FormatSeries(series)
	if !strings.Contains(result, "and 10 more") {
		t.Error("expected truncation message for >50 series")
	}
}

func TestFormatStats_Nil(t *testing.T) {
	result := FormatStats(nil)
	if !strings.Contains(result, "no stats available") {
		t.Errorf("expected 'no stats available', got %s", result)
	}
}

func TestFormatStats(t *testing.T) {
	stats := &StatsResponse{
		Streams: 100,
		Chunks:  5000,
		Entries: 1000000,
		Bytes:   1073741824, // 1 GB
	}
	result := FormatStats(stats)
	if !strings.Contains(result, "Index Statistics") {
		t.Error("expected header")
	}
	if !strings.Contains(result, "100") {
		t.Error("expected streams count")
	}
	if !strings.Contains(result, "1.0 GB") {
		t.Error("expected human-readable bytes")
	}
}

func TestFormatStatus_Healthy(t *testing.T) {
	info := &BuildInfo{Version: "2.9.4", Branch: "main", GoVersion: "go1.21"}
	result := FormatStatus(true, 50*time.Millisecond, info)
	if !strings.Contains(result, "Healthy") {
		t.Error("expected Healthy")
	}
	if !strings.Contains(result, "2.9.4") {
		t.Error("expected version")
	}
	if !strings.Contains(result, "main") {
		t.Error("expected branch")
	}
}

func TestFormatStatus_Unhealthy(t *testing.T) {
	result := FormatStatus(false, 5*time.Second, nil)
	if !strings.Contains(result, "Unhealthy") {
		t.Error("expected Unhealthy")
	}
}

func TestFormatSummary(t *testing.T) {
	s := &Summary{
		Healthy: true,
		Latency: 30 * time.Millisecond,
		Version: "2.9.4",
		Labels:  5,
		Series:  20,
		Stats:   &StatsResponse{Streams: 100, Entries: 50000, Bytes: 1048576},
	}
	result := FormatSummary(s)
	if !strings.Contains(result, "Loki Summary") {
		t.Error("expected header")
	}
	if !strings.Contains(result, "Healthy") {
		t.Error("expected health status")
	}
	if !strings.Contains(result, "2.9.4") {
		t.Error("expected version")
	}
	if !strings.Contains(result, "1.0 MB") {
		t.Error("expected human bytes")
	}
}

func TestFormatSummary_NoStats(t *testing.T) {
	s := &Summary{Healthy: false, Latency: 1 * time.Second}
	result := FormatSummary(s)
	if !strings.Contains(result, "Unhealthy") {
		t.Error("expected Unhealthy")
	}
	// Should not panic with nil Stats
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		input uint64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
		{1099511627776, "1.0 TB"},
		{2684354560, "2.5 GB"},
	}
	for _, tt := range tests {
		got := humanBytes(tt.input)
		if got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatLabelSet(t *testing.T) {
	tests := []struct {
		name   string
		input  map[string]string
		want   string
	}{
		{"empty", map[string]string{}, "{}"},
		{"single", map[string]string{"app": "nginx"}, `{app="nginx"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatLabelSet(tt.input)
			if got != tt.want {
				t.Errorf("formatLabelSet() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatLabelSet_Sorted(t *testing.T) {
	labels := map[string]string{"z": "3", "a": "1", "m": "2"}
	result := formatLabelSet(labels)
	aIdx := strings.Index(result, "a=")
	mIdx := strings.Index(result, "m=")
	zIdx := strings.Index(result, "z=")
	if aIdx > mIdx || mIdx > zIdx {
		t.Errorf("expected sorted label set, got %s", result)
	}
}

func TestContainsError(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"ERROR: connection refused", true},
		{"err=timeout", true},
		{"FATAL: disk full", true},
		{"panic: runtime error", true},
		{"java.lang.NullPointerException", true},
		{"normal log line", false},
		{"info: started successfully", false},
	}
	for _, tt := range tests {
		if got := containsError(tt.line); got != tt.want {
			t.Errorf("containsError(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestContainsWarn(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"WARN: disk 80% full", true},
		{"WARNING: deprecated API", true},
		{"info: all good", false},
		{"error something", false},
	}
	for _, tt := range tests {
		if got := containsWarn(tt.line); got != tt.want {
			t.Errorf("containsWarn(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

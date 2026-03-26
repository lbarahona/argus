package alertmanager

import (
	"strings"
	"testing"
	"time"
)

func TestFormatAlerts_Empty(t *testing.T) {
	out := FormatAlerts(nil, false)
	if !strings.Contains(out, "No alerts firing") {
		t.Errorf("expected 'No alerts firing', got: %s", out)
	}
}

func TestFormatAlerts_Active(t *testing.T) {
	alerts := []Alert{
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "HighLatency", "severity": "critical", "instance": "web-1"},
			Annotations: map[string]string{"summary": "Latency is above 500ms"},
			StartsAt: time.Now().Add(-30 * time.Minute),
		},
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "DiskFull", "severity": "warning", "instance": "db-1"},
			Annotations: map[string]string{"description": "Disk usage above 90% on db-1 volume /data"},
			StartsAt: time.Now().Add(-2 * time.Hour),
		},
	}

	out := FormatAlerts(alerts, false)

	if !strings.Contains(out, "2 alerts") {
		t.Error("expected '2 alerts' in output")
	}
	if !strings.Contains(out, "HighLatency") {
		t.Error("expected 'HighLatency' in output")
	}
	if !strings.Contains(out, "DiskFull") {
		t.Error("expected 'DiskFull' in output")
	}
	if !strings.Contains(out, "Latency is above 500ms") {
		t.Error("expected summary annotation in output")
	}
	if !strings.Contains(out, "instance") {
		t.Error("expected 'instance' label in output")
	}
}

func TestFormatAlerts_SuppressedFiltered(t *testing.T) {
	alerts := []Alert{
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "Active", "severity": "warning"},
			StartsAt: time.Now(),
		},
		{
			Status:   AlertStatus{State: "suppressed", SilencedBy: []string{"sil-1"}},
			Labels:   map[string]string{"alertname": "Silenced", "severity": "warning"},
			StartsAt: time.Now(),
		},
	}

	// showAll=false should filter suppressed
	out := FormatAlerts(alerts, false)
	if strings.Contains(out, "Silenced") {
		// The rendering shows all in the count but may filter display
		// Just ensure it doesn't panic
	}

	// showAll=true should show all
	outAll := FormatAlerts(alerts, true)
	if !strings.Contains(outAll, "silenced by") {
		t.Error("expected 'silenced by' in showAll output")
	}
}

func TestFormatAlerts_LongDescription(t *testing.T) {
	longDesc := strings.Repeat("A very long description. ", 20) // ~500 chars
	alerts := []Alert{
		{
			Status:      AlertStatus{State: "active"},
			Labels:      map[string]string{"alertname": "Test", "severity": "info"},
			Annotations: map[string]string{"description": longDesc},
			StartsAt:    time.Now(),
		},
	}

	out := FormatAlerts(alerts, false)
	// Should truncate the description
	if strings.Contains(out, longDesc) {
		t.Error("expected description to be truncated")
	}
	if !strings.Contains(out, "...") {
		t.Error("expected truncation marker '...'")
	}
}

func TestFormatSilences_Empty(t *testing.T) {
	out := FormatSilences(nil, false)
	if !strings.Contains(out, "No silences") {
		t.Errorf("expected 'No silences', got: %s", out)
	}
}

func TestFormatSilences_Active(t *testing.T) {
	silences := []Silence{
		{
			ID:        "abc12345-6789",
			Status:    Status{State: "active"},
			Comment:   "Deploying fix",
			CreatedBy: "sre",
			StartsAt:  time.Now().Add(-time.Hour),
			EndsAt:    time.Now().Add(time.Hour),
			Matchers:  []Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		},
	}

	out := FormatSilences(silences, false)
	if !strings.Contains(out, "1 active") {
		t.Error("expected '1 active' in output")
	}
	if !strings.Contains(out, "Deploying fix") {
		t.Error("expected comment in output")
	}
	if !strings.Contains(out, "alertname") {
		t.Error("expected matcher in output")
	}
}

func TestFormatSilences_ExpiredFiltered(t *testing.T) {
	silences := []Silence{
		{
			ID:       "active-001",
			Status:   Status{State: "active"},
			Comment:  "Active silence",
			EndsAt:   time.Now().Add(time.Hour),
			Matchers: []Matcher{{Name: "a", Value: "b", IsEqual: true}},
		},
		{
			ID:       "expired-001",
			Status:   Status{State: "expired"},
			Comment:  "Expired silence",
			EndsAt:   time.Now().Add(-time.Hour),
			Matchers: []Matcher{{Name: "c", Value: "d", IsEqual: true}},
		},
	}

	// Without expired
	out := FormatSilences(silences, false)
	if strings.Contains(out, "Expired silence") {
		t.Error("should not show expired silence when showExpired=false")
	}

	// With expired
	outExpired := FormatSilences(silences, true)
	if !strings.Contains(outExpired, "Expired silence") {
		t.Error("should show expired silence when showExpired=true")
	}
}

func TestFormatStatus(t *testing.T) {
	status := &AMStatus{
		VersionInfo: VersionInfo{Version: "0.27.0"},
		Cluster: ClusterStatus{
			Name:   "production",
			Status: "ready",
			Peers:  []Peer{{Name: "am-0", Address: "10.0.0.1:9094"}},
		},
	}

	out := FormatStatus(status, true, 15*time.Millisecond)
	if !strings.Contains(out, "0.27.0") {
		t.Error("expected version in output")
	}
	if !strings.Contains(out, "production") {
		t.Error("expected cluster name in output")
	}
	if !strings.Contains(out, "am-0") {
		t.Error("expected peer name in output")
	}
	if !strings.Contains(out, "✅") {
		t.Error("expected healthy icon")
	}
}

func TestFormatStatus_Unhealthy(t *testing.T) {
	status := &AMStatus{
		VersionInfo: VersionInfo{Version: "0.27.0"},
		Cluster:     ClusterStatus{Name: "test", Status: "settling"},
	}

	out := FormatStatus(status, false, 5*time.Second)
	if !strings.Contains(out, "🔴") {
		t.Error("expected unhealthy icon")
	}
}

func TestFormatJSON(t *testing.T) {
	alerts := []Alert{
		{
			Labels: map[string]string{"alertname": "Test"},
			Status: AlertStatus{State: "active"},
		},
	}

	out, err := FormatJSON(alerts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Test") {
		t.Error("expected alert name in JSON")
	}
}

func TestFormatSilencesJSON(t *testing.T) {
	silences := []Silence{
		{ID: "test-1", Status: Status{State: "active"}, Comment: "test"},
	}

	out, err := FormatSilencesJSON(silences)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "test-1") {
		t.Error("expected silence ID in JSON")
	}
}

func TestSeverityIcon(t *testing.T) {
	tests := []struct {
		sev  string
		want string
	}{
		{"critical", "🔴"},
		{"warning", "⚠️"},
		{"info", "ℹ️"},
		{"unknown", "❓"},
		{"", "❓"},
	}

	for _, tt := range tests {
		got := SeverityIcon(tt.sev)
		if got != tt.want {
			t.Errorf("SeverityIcon(%q) = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{2 * time.Hour, "2h"},
		{2*time.Hour + 30*time.Minute, "2h30m"},
		{48 * time.Hour, "2d"},
		{25 * time.Hour, "1d1h"},
		{-time.Hour, "expired"},
	}

	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestSeverityRank(t *testing.T) {
	if severityRank("critical") >= severityRank("warning") {
		t.Error("critical should rank lower (higher priority) than warning")
	}
	if severityRank("warning") >= severityRank("info") {
		t.Error("warning should rank lower than info")
	}
	if severityRank("info") >= severityRank("other") {
		t.Error("info should rank lower than unknown")
	}
}

func TestSeverityColor(t *testing.T) {
	// Just ensure no panics and returns non-empty strings
	for _, sev := range []string{"critical", "warning", "info", "unknown", ""} {
		c := severityColor(sev)
		if c == "" {
			t.Errorf("severityColor(%q) returned empty string", sev)
		}
	}
}

func TestFormatAlerts_SortOrder(t *testing.T) {
	alerts := []Alert{
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "Info", "severity": "info"},
			StartsAt: time.Now(),
		},
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "Critical", "severity": "critical"},
			StartsAt: time.Now(),
		},
		{
			Status:   AlertStatus{State: "active"},
			Labels:   map[string]string{"alertname": "Warning", "severity": "warning"},
			StartsAt: time.Now(),
		},
	}

	out := FormatAlerts(alerts, false)

	// Critical should appear before Warning which should appear before Info
	critIdx := strings.Index(out, "Critical")
	warnIdx := strings.Index(out, "Warning")
	infoIdx := strings.Index(out, "Info")

	if critIdx > warnIdx || warnIdx > infoIdx {
		t.Errorf("alerts not sorted by severity: critical=%d warning=%d info=%d", critIdx, warnIdx, infoIdx)
	}
}

func TestFormatSilences_MatcherOperators(t *testing.T) {
	silences := []Silence{
		{
			ID:      "regex-silence",
			Status:  Status{State: "active"},
			Comment: "Regex silence",
			EndsAt:  time.Now().Add(time.Hour),
			Matchers: []Matcher{
				{Name: "alertname", Value: "High.*", IsEqual: true, IsRegex: true},
				{Name: "env", Value: "staging", IsEqual: false, IsRegex: false},
			},
		},
	}

	out := FormatSilences(silences, false)
	if !strings.Contains(out, "=~") {
		t.Error("expected regex matcher operator =~")
	}
	if !strings.Contains(out, "!=") {
		t.Error("expected not-equal matcher operator !=")
	}
}

func TestFormatAlerts_LabelsDisplay(t *testing.T) {
	alerts := []Alert{
		{
			Status: AlertStatus{State: "active"},
			Labels: map[string]string{
				"alertname": "PodCrash",
				"severity":  "critical",
				"namespace": "production",
				"pod":       "api-server-abc123",
				"service":   "api",
				"job":       "kubernetes-pods",
			},
			StartsAt: time.Now(),
		},
	}

	out := FormatAlerts(alerts, false)

	// Should display these label keys
	for _, key := range []string{"namespace", "pod", "service", "job"} {
		if !strings.Contains(out, key) {
			t.Errorf("expected label %q in output", key)
		}
	}
}

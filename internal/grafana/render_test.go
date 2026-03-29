package grafana

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDashboards(t *testing.T) {
	dashboards := []Dashboard{
		{UID: "d-1", Title: "CPU Metrics", FolderTitle: "Infrastructure", Tags: []string{"prod"}, IsStarred: true},
		{UID: "d-2", Title: "Memory", FolderTitle: "Infrastructure"},
		{UID: "d-3", Title: "API Latency", FolderTitle: "Services"},
	}

	out := FormatDashboards(dashboards)

	if !strings.Contains(out, "Dashboards (3)") {
		t.Error("missing dashboard count")
	}
	if !strings.Contains(out, "Infrastructure (2)") {
		t.Error("missing Infrastructure folder")
	}
	if !strings.Contains(out, "Services (1)") {
		t.Error("missing Services folder")
	}
	if !strings.Contains(out, "⭐") {
		t.Error("missing star icon for starred dashboard")
	}
	if !strings.Contains(out, "[prod]") {
		t.Error("missing tags")
	}
	if !strings.Contains(out, "d-1") {
		t.Error("missing UID")
	}
}

func TestFormatDashboards_Empty(t *testing.T) {
	out := FormatDashboards(nil)
	if out != "No dashboards found." {
		t.Errorf("expected empty message, got: %q", out)
	}
}

func TestFormatDashboards_NoFolder(t *testing.T) {
	dashboards := []Dashboard{
		{UID: "d-1", Title: "General Dashboard"},
	}
	out := FormatDashboards(dashboards)
	if !strings.Contains(out, "General (1)") {
		t.Error("missing General folder for unfoldered dashboards")
	}
}

func TestFormatDashboardDetail(t *testing.T) {
	dm := &DashboardMeta{
		Meta: Meta{
			Slug:        "test-dash",
			Version:     5,
			FolderTitle: "Infra",
			CreatedBy:   "admin",
			UpdatedBy:   "lester",
			Created:     time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
			Updated:     time.Date(2026, 3, 28, 14, 0, 0, 0, time.UTC),
		},
		Dashboard: map[string]interface{}{
			"title":       "My Dashboard",
			"description": "Monitors infrastructure",
			"tags":        []interface{}{"prod", "sre"},
			"panels": []interface{}{
				map[string]interface{}{"type": "graph"},
				map[string]interface{}{"type": "stat"},
				map[string]interface{}{"type": "graph"},
			},
		},
	}

	out := FormatDashboardDetail(dm)

	if !strings.Contains(out, "My Dashboard") {
		t.Error("missing title")
	}
	if !strings.Contains(out, "Monitors infrastructure") {
		t.Error("missing description")
	}
	if !strings.Contains(out, "Version: 5") {
		t.Error("missing version")
	}
	if !strings.Contains(out, "Folder:  Infra") {
		t.Error("missing folder")
	}
	if !strings.Contains(out, "Panels: 3") {
		t.Error("missing panel count")
	}
	if !strings.Contains(out, "graph: 2") {
		t.Error("missing graph panel type count")
	}
	if !strings.Contains(out, "prod, sre") {
		t.Error("missing tags")
	}
}

func TestFormatDashboardDetail_Minimal(t *testing.T) {
	dm := &DashboardMeta{
		Meta:      Meta{},
		Dashboard: map[string]interface{}{},
	}
	out := FormatDashboardDetail(dm)
	if !strings.Contains(out, "Untitled") {
		t.Error("missing Untitled for dashboard without title")
	}
}

func TestFormatDatasources(t *testing.T) {
	ds := []Datasource{
		{Name: "Prometheus", Type: "prometheus", URL: "http://prometheus:9090", IsDefault: true, Access: "proxy"},
		{Name: "Loki", Type: "loki", URL: "http://loki:3100", Access: "proxy"},
		{Name: "PostgreSQL", Type: "postgres", URL: "localhost:5432", Access: "proxy", ReadOnly: true},
	}

	out := FormatDatasources(ds)

	if !strings.Contains(out, "Data Sources (3)") {
		t.Error("missing count")
	}
	if !strings.Contains(out, "🔥") {
		t.Error("missing prometheus icon")
	}
	if !strings.Contains(out, "📜") {
		t.Error("missing loki icon")
	}
	if !strings.Contains(out, "(default)") {
		t.Error("missing default marker")
	}
	if !strings.Contains(out, "[read-only]") {
		t.Error("missing read-only marker")
	}
}

func TestFormatDatasources_Empty(t *testing.T) {
	out := FormatDatasources(nil)
	if out != "No data sources configured." {
		t.Errorf("expected empty message, got: %q", out)
	}
}

func TestFormatAlertRules(t *testing.T) {
	rules := []AlertRule{
		{Title: "High CPU", RuleGroup: "infra", For: "5m", NoDataState: "NoData", ExecErrState: "Alerting", Labels: map[string]string{"severity": "critical"}, Annotations: map[string]string{"summary": "CPU above 90%"}},
		{Title: "High Memory", RuleGroup: "infra", For: "10m", NoDataState: "NoData", ExecErrState: "OK"},
		{Title: "Error Rate", RuleGroup: "services", For: "2m", NoDataState: "Alerting", ExecErrState: "Alerting"},
	}

	out := FormatAlertRules(rules)

	if !strings.Contains(out, "Alert Rules (3)") {
		t.Error("missing rule count")
	}
	if !strings.Contains(out, "infra (2 rules)") {
		t.Error("missing infra group")
	}
	if !strings.Contains(out, "services (1 rules)") {
		t.Error("missing services group")
	}
	if !strings.Contains(out, "CPU above 90%") {
		t.Error("missing summary annotation")
	}
	if !strings.Contains(out, "severity=") {
		t.Error("missing labels")
	}
}

func TestFormatAlertRules_Empty(t *testing.T) {
	out := FormatAlertRules(nil)
	if out != "No alert rules configured." {
		t.Errorf("expected empty message, got: %q", out)
	}
}

func TestFormatAlertRules_EmptyFor(t *testing.T) {
	rules := []AlertRule{
		{Title: "Test", RuleGroup: "grp", NoDataState: "OK", ExecErrState: "OK"},
	}
	out := FormatAlertRules(rules)
	if !strings.Contains(out, "for: 0s") {
		t.Error("expected default '0s' for empty For field")
	}
}

func TestFormatAlertInstances(t *testing.T) {
	instances := []GrafanaAlertInstance{
		{
			Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
			Annotations: map[string]string{"summary": "CPU at 95%"},
			Status:      AlertInstanceStatus{State: "active"},
			StartsAt:    time.Now().Add(-30 * time.Minute),
		},
		{
			Labels:      map[string]string{"alertname": "DiskSpace", "severity": "warning"},
			Status:      AlertInstanceStatus{State: "suppressed"},
			StartsAt:    time.Now().Add(-2 * time.Hour),
		},
	}

	out := FormatAlertInstances(instances)

	if !strings.Contains(out, "2 total, 1 active") {
		t.Error("missing instance counts")
	}
	if !strings.Contains(out, "🔴") {
		t.Error("missing active icon")
	}
	if !strings.Contains(out, "🟡") {
		t.Error("missing suppressed icon")
	}
	if !strings.Contains(out, "CPU at 95%") {
		t.Error("missing summary")
	}
	if !strings.Contains(out, "HighCPU") {
		t.Error("missing alertname")
	}
}

func TestFormatAlertInstances_Empty(t *testing.T) {
	out := FormatAlertInstances(nil)
	if !strings.Contains(out, "No firing alerts") {
		t.Error("expected no firing alerts message")
	}
}

func TestFormatFolders(t *testing.T) {
	folders := []Folder{
		{UID: "f-1", Title: "Infrastructure", URL: "/dashboards/f/f-1/infrastructure"},
		{UID: "f-2", Title: "Services", URL: "/dashboards/f/f-2/services"},
	}

	out := FormatFolders(folders)

	if !strings.Contains(out, "Folders (2)") {
		t.Error("missing folder count")
	}
	if !strings.Contains(out, "Infrastructure") {
		t.Error("missing folder name")
	}
	if !strings.Contains(out, "f-1") {
		t.Error("missing folder UID")
	}
}

func TestFormatFolders_Empty(t *testing.T) {
	out := FormatFolders(nil)
	if out != "No folders found." {
		t.Errorf("expected empty message, got: %q", out)
	}
}

func TestFormatSummary(t *testing.T) {
	s := &Summary{
		Dashboards:   15,
		Folders:      3,
		Datasources:  5,
		AlertRules:   12,
		FiringAlerts: 2,
		Version:      "10.3.1",
		OrgName:      "Main Org",
	}

	out := FormatSummary(s)

	if !strings.Contains(out, "Main Org") {
		t.Error("missing org name")
	}
	if !strings.Contains(out, "10.3.1") {
		t.Error("missing version")
	}
	if !strings.Contains(out, "Dashboards:   15") {
		t.Error("missing dashboard count")
	}
	if !strings.Contains(out, "🔥 Firing:     2") {
		t.Error("missing firing count")
	}
}

func TestFormatSummary_NoFiring(t *testing.T) {
	s := &Summary{Dashboards: 5}
	out := FormatSummary(s)
	if !strings.Contains(out, "No firing alerts") {
		t.Error("expected no firing alerts message")
	}
}

func TestFormatStatus(t *testing.T) {
	health := &HealthResponse{
		Version:  "10.3.1",
		Commit:   "abc1234",
		Database: "ok",
	}
	org := &OrgInfo{ID: 1, Name: "Main Org"}

	out := FormatStatus(health, org)

	if !strings.Contains(out, "10.3.1") {
		t.Error("missing version")
	}
	if !strings.Contains(out, "abc1234") {
		t.Error("missing commit")
	}
	if !strings.Contains(out, "ok") {
		t.Error("missing database status")
	}
	if !strings.Contains(out, "Main Org") {
		t.Error("missing org name")
	}
}

func TestFormatStatus_NilOrg(t *testing.T) {
	health := &HealthResponse{Version: "10.3.1"}
	out := FormatStatus(health, nil)
	if strings.Contains(out, "Org:") {
		t.Error("should not show org when nil")
	}
}

func TestFormatJSON(t *testing.T) {
	data := map[string]string{"key": "value"}
	out := FormatJSON(data)
	if !strings.Contains(out, `"key": "value"`) {
		t.Errorf("expected JSON, got: %s", out)
	}
}

func TestDsTypeIcon(t *testing.T) {
	tests := []struct {
		dsType string
		want   string
	}{
		{"prometheus", "🔥"},
		{"loki", "📜"},
		{"elasticsearch", "🔍"},
		{"mysql", "🗄️"},
		{"postgres", "🗄️"},
		{"influxdb", "📈"},
		{"graphite", "📉"},
		{"cloudwatch", "☁️"},
		{"tempo", "🔗"},
		{"jaeger", "🔗"},
		{"unknown", "📦"},
	}

	for _, tt := range tests {
		t.Run(tt.dsType, func(t *testing.T) {
			if got := dsTypeIcon(tt.dsType); got != tt.want {
				t.Errorf("dsTypeIcon(%q) = %q, want %q", tt.dsType, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{2 * time.Hour, "2h"},
		{25 * time.Hour, "1d1h"},
		{48 * time.Hour, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatDuration(tt.d); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is..."},
		{"exact", 5, "exact"},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			if got := truncate(tt.s, tt.maxLen); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestFormatLabels(t *testing.T) {
	labels := map[string]string{"severity": "critical", "team": "sre"}
	out := formatLabels(labels)
	if !strings.Contains(out, "severity=") {
		t.Error("missing severity label")
	}
	if !strings.Contains(out, "team=") {
		t.Error("missing team label")
	}
}

func TestFormatLabels_Empty(t *testing.T) {
	out := formatLabels(nil)
	if out != "{}" {
		t.Errorf("expected {}, got %q", out)
	}
}

func TestStateIcon(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{"active", "🔴"},
		{"suppressed", "🟡"},
		{"unprocessed", "⚪"},
		{"", "⚪"},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if got := stateIcon(tt.state); got != tt.want {
				t.Errorf("stateIcon(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

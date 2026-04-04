package grafana

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGrafanaConfig_IsConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  GrafanaConfig
		want bool
	}{
		{"empty", GrafanaConfig{}, false},
		{"url only", GrafanaConfig{URL: "http://grafana:3000"}, true},
		{"url and key", GrafanaConfig{URL: "http://grafana:3000", APIKey: "glsa_xxx"}, true},
		{"key only no url", GrafanaConfig{APIKey: "glsa_xxx"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDashboard_JSON(t *testing.T) {
	d := Dashboard{
		ID:          1,
		UID:         "abc123",
		Title:       "My Dashboard",
		Type:        "dash-db",
		Tags:        []string{"production", "sre"},
		IsStarred:   true,
		FolderTitle: "Infrastructure",
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Dashboard
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.UID != "abc123" {
		t.Errorf("UID = %q, want abc123", got.UID)
	}
	if got.Title != "My Dashboard" {
		t.Errorf("Title = %q, want My Dashboard", got.Title)
	}
	if len(got.Tags) != 2 {
		t.Errorf("Tags len = %d, want 2", len(got.Tags))
	}
	if !got.IsStarred {
		t.Error("IsStarred should be true")
	}
}

func TestDatasource_JSON(t *testing.T) {
	ds := Datasource{
		ID:        1,
		UID:       "prom-uid",
		Name:      "Prometheus",
		Type:      "prometheus",
		URL:       "http://prometheus:9090",
		IsDefault: true,
	}

	data, err := json.Marshal(ds)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Datasource
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != "prometheus" {
		t.Errorf("Type = %q, want prometheus", got.Type)
	}
	if !got.IsDefault {
		t.Error("IsDefault should be true")
	}
}

func TestAlertRule_JSON(t *testing.T) {
	ar := AlertRule{
		ID:           1,
		UID:          "rule-1",
		Title:        "High CPU",
		RuleGroup:    "infra",
		For:          "5m",
		NoDataState:  "NoData",
		ExecErrState: "Alerting",
		Labels:       map[string]string{"severity": "critical"},
		Annotations:  map[string]string{"summary": "CPU above 90%"},
	}

	data, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got AlertRule
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Title != "High CPU" {
		t.Errorf("Title = %q, want High CPU", got.Title)
	}
	if got.Labels["severity"] != "critical" {
		t.Errorf("severity = %q, want critical", got.Labels["severity"])
	}
}

func TestGrafanaAlertInstance_JSON(t *testing.T) {
	now := time.Now()
	inst := GrafanaAlertInstance{
		Labels:      map[string]string{"alertname": "HighMem", "severity": "warning"},
		Annotations: map[string]string{"summary": "Memory usage high"},
		StartsAt:    now.Add(-10 * time.Minute),
		Status:      AlertInstanceStatus{State: "active"},
	}

	data, err := json.Marshal(inst)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got GrafanaAlertInstance
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Labels["alertname"] != "HighMem" {
		t.Errorf("alertname = %q, want HighMem", got.Labels["alertname"])
	}
	if got.Status.State != "active" {
		t.Errorf("state = %q, want active", got.Status.State)
	}
}

func TestHealthResponse_JSON(t *testing.T) {
	h := HealthResponse{
		Commit:   "abc123",
		Database: "ok",
		Version:  "10.3.1",
	}

	data, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got HealthResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Version != "10.3.1" {
		t.Errorf("Version = %q, want 10.3.1", got.Version)
	}
}

func TestSummary_Fields(t *testing.T) {
	s := Summary{
		Dashboards:   15,
		Folders:      3,
		Datasources:  5,
		AlertRules:   12,
		FiringAlerts: 2,
		Version:      "10.3.1",
		OrgName:      "Main Org",
	}

	if s.Dashboards != 15 {
		t.Errorf("Dashboards = %d, want 15", s.Dashboards)
	}
	if s.FiringAlerts != 2 {
		t.Errorf("FiringAlerts = %d, want 2", s.FiringAlerts)
	}
}

func TestFolder_JSON(t *testing.T) {
	f := Folder{
		ID:    1,
		UID:   "folder-uid",
		Title: "Infrastructure",
		URL:   "/dashboards/f/folder-uid/infrastructure",
	}

	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Folder
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Title != "Infrastructure" {
		t.Errorf("Title = %q, want Infrastructure", got.Title)
	}
}

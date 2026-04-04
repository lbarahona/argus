package prometheus

import (
	"testing"
)

func TestPrometheusConfigIsConfigured(t *testing.T) {
	cfg := PrometheusConfig{}
	if cfg.IsConfigured() {
		t.Error("expected not configured when URL is empty")
	}
	cfg.URL = "http://localhost:9090"
	if !cfg.IsConfigured() {
		t.Error("expected configured when URL is set")
	}
}

func TestBuildSummaryNil(t *testing.T) {
	s := BuildSummary(nil, nil, nil)
	if s.TotalRuleGroups != 0 || s.ActiveTargets != 0 || s.FiringAlerts != 0 {
		t.Error("expected all zeros for nil inputs")
	}
}

func TestBuildSummaryWithRules(t *testing.T) {
	rules := &RulesData{
		Groups: []RuleGroup{
			{
				Rules: []Rule{
					{Type: "alerting"},
					{Type: "alerting"},
					{Type: "recording"},
				},
			},
			{
				Rules: []Rule{
					{Type: "recording"},
				},
			},
		},
	}
	s := BuildSummary(rules, nil, nil)
	if s.TotalRuleGroups != 2 {
		t.Errorf("expected 2 groups, got %d", s.TotalRuleGroups)
	}
	if s.TotalAlertRules != 2 {
		t.Errorf("expected 2 alert rules, got %d", s.TotalAlertRules)
	}
	if s.TotalRecordRules != 2 {
		t.Errorf("expected 2 record rules, got %d", s.TotalRecordRules)
	}
}

func TestBuildSummaryWithTargets(t *testing.T) {
	targets := &TargetsData{
		ActiveTargets: []Target{
			{Health: "up"},
			{Health: "up"},
			{Health: "down"},
		},
	}
	s := BuildSummary(nil, targets, nil)
	if s.ActiveTargets != 3 {
		t.Errorf("expected 3 active targets, got %d", s.ActiveTargets)
	}
	if s.HealthyTargets != 2 {
		t.Errorf("expected 2 healthy, got %d", s.HealthyTargets)
	}
	if s.UnhealthyTargets != 1 {
		t.Errorf("expected 1 unhealthy, got %d", s.UnhealthyTargets)
	}
}

func TestBuildSummaryWithAlerts(t *testing.T) {
	alerts := &AlertsData{
		Alerts: []PromAlert{
			{State: "firing"},
			{State: "firing"},
			{State: "pending"},
			{State: "inactive"},
		},
	}
	s := BuildSummary(nil, nil, alerts)
	if s.FiringAlerts != 2 {
		t.Errorf("expected 2 firing, got %d", s.FiringAlerts)
	}
	if s.PendingAlerts != 1 {
		t.Errorf("expected 1 pending, got %d", s.PendingAlerts)
	}
}

func TestBuildSummaryCombined(t *testing.T) {
	rules := &RulesData{Groups: []RuleGroup{{Rules: []Rule{{Type: "alerting"}}}}}
	targets := &TargetsData{ActiveTargets: []Target{{Health: "up"}}}
	alerts := &AlertsData{Alerts: []PromAlert{{State: "firing"}}}

	s := BuildSummary(rules, targets, alerts)
	if s.TotalRuleGroups != 1 || s.TotalAlertRules != 1 || s.ActiveTargets != 1 || s.FiringAlerts != 1 {
		t.Error("combined summary has wrong values")
	}
}

package prometheus

import (
	"encoding/json"
	"time"
)

// PrometheusConfig holds Prometheus connection details.
type PrometheusConfig struct {
	URL       string    `yaml:"url" json:"url"`
	BasicAuth BasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// BasicAuth holds username/password for HTTP basic auth.
type BasicAuth struct {
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
}

// IsConfigured returns true if the Prometheus URL is set.
func (c PrometheusConfig) IsConfigured() bool {
	return c.URL != ""
}

// APIResponse is the standard Prometheus API response wrapper.
type APIResponse struct {
	Status    string          `json:"status"`
	Data      json.RawMessage `json:"data"`
	ErrorType string          `json:"errorType,omitempty"`
	Error     string          `json:"error,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
}

// RulesData wraps the rules response.
type RulesData struct {
	Groups []RuleGroup `json:"groups"`
}

// RuleGroup is a named group of rules.
type RuleGroup struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	Rules    []Rule  `json:"rules"`
	Interval float64 `json:"interval"`
}

// Rule is an alerting or recording rule.
type Rule struct {
	Name        string            `json:"name"`
	Query       string            `json:"query"`
	Duration    float64           `json:"duration,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Alerts      []RuleAlert       `json:"alerts,omitempty"`
	Health      string            `json:"health"`
	State       string            `json:"state,omitempty"` // firing, pending, inactive
	Type        string            `json:"type"`            // alerting, recording
	LastError   string            `json:"lastError,omitempty"`
	EvalTime    float64           `json:"evaluationTime"`
}

// RuleAlert is an active alert instance within a rule.
type RuleAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    time.Time         `json:"activeAt"`
	Value       string            `json:"value"`
}

// TargetsData wraps the targets response.
type TargetsData struct {
	ActiveTargets  []Target `json:"activeTargets"`
	DroppedTargets []Target `json:"droppedTargets"`
}

// Target is a Prometheus scrape target.
type Target struct {
	DiscoveredLabels map[string]string `json:"discoveredLabels"`
	Labels           map[string]string `json:"labels"`
	ScrapePool       string            `json:"scrapePool"`
	ScrapeURL        string            `json:"scrapeUrl"`
	GlobalURL        string            `json:"globalUrl,omitempty"`
	LastError        string            `json:"lastError"`
	LastScrape       time.Time         `json:"lastScrape"`
	LastScrapeDur    float64           `json:"lastScrapeDuration"`
	Health           string            `json:"health"` // up, down, unknown
}

// AlertsData wraps the alerts response.
type AlertsData struct {
	Alerts []PromAlert `json:"alerts"`
}

// PromAlert is a firing alert from Prometheus.
type PromAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	State       string            `json:"state"`
	ActiveAt    time.Time         `json:"activeAt"`
	Value       string            `json:"value"`
}

// QueryResult holds the result of an instant query.
type QueryResult struct {
	ResultType string          `json:"resultType"` // vector, matrix, scalar, string
	Result     json.RawMessage `json:"result"`
}

// VectorSample is a single sample from a vector query.
type VectorSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]interface{}    `json:"value"` // [timestamp, "value"]
}

// MatrixSeries is a time series from a matrix query.
type MatrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]interface{}  `json:"values"`
}

// RuntimeInfo holds Prometheus runtime information.
type RuntimeInfo struct {
	StartTime           string `json:"startTime"`
	CWD                 string `json:"CWD"`
	ReloadConfigSuccess bool   `json:"reloadConfigSuccess"`
	LastConfigTime      string `json:"lastConfigTime"`
	CorruptionCount     int    `json:"corruptionCount"`
	GoroutineCount      int    `json:"goroutineCount"`
	GOMAXPROCS          int    `json:"GOMAXPROCS"`
	GOGC                string `json:"GOGC"`
	GODEBUG             string `json:"GODEBUG"`
	StorageRetention    string `json:"storageRetention"`
}

// BuildInfo holds Prometheus build information.
type BuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

// Summary holds aggregated stats for quick overview.
type Summary struct {
	TotalRuleGroups  int
	TotalAlertRules  int
	TotalRecordRules int
	FiringAlerts     int
	PendingAlerts    int
	ActiveTargets    int
	HealthyTargets   int
	UnhealthyTargets int
}

// BuildSummary aggregates stats from rules and targets data.
func BuildSummary(rules *RulesData, targets *TargetsData, alerts *AlertsData) Summary {
	s := Summary{}
	if rules != nil {
		s.TotalRuleGroups = len(rules.Groups)
		for _, g := range rules.Groups {
			for _, r := range g.Rules {
				if r.Type == "alerting" {
					s.TotalAlertRules++
				} else {
					s.TotalRecordRules++
				}
			}
		}
	}
	if targets != nil {
		s.ActiveTargets = len(targets.ActiveTargets)
		for _, t := range targets.ActiveTargets {
			if t.Health == "up" {
				s.HealthyTargets++
			} else {
				s.UnhealthyTargets++
			}
		}
	}
	if alerts != nil {
		for _, a := range alerts.Alerts {
			switch a.State {
			case "firing":
				s.FiringAlerts++
			case "pending":
				s.PendingAlerts++
			}
		}
	}
	return s
}

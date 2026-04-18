package correlate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	amlib "github.com/lbarahona/argus/internal/alertmanager"
	grafanalib "github.com/lbarahona/argus/internal/grafana"
	lokilib "github.com/lbarahona/argus/internal/loki"
	promlib "github.com/lbarahona/argus/internal/prometheus"
)

var normalizeNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// StackOptions configures cross-tool alert correlation.
type StackOptions struct {
	Duration    int
	LogLimit    int
	SampleLimit int
}

// CorrelatedSignal is a normalized alert-like signal from one source.
type CorrelatedSignal struct {
	Source   string            `json:"source"`
	Name     string            `json:"name"`
	Service  string            `json:"service,omitempty"`
	Severity string            `json:"severity,omitempty"`
	State    string            `json:"state,omitempty"`
	Summary  string            `json:"summary,omitempty"`
	StartsAt time.Time         `json:"starts_at,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// CorrelatedGroup represents related signals across tools.
type CorrelatedGroup struct {
	Key        string             `json:"key"`
	Service    string             `json:"service,omitempty"`
	Severity   string             `json:"severity"`
	Sources    []string           `json:"sources"`
	AlertNames []string           `json:"alert_names"`
	Signals    []CorrelatedSignal `json:"signals"`
	Logs       []lokilib.LogEntry `json:"logs,omitempty"`
	Score      int                `json:"score"`
}

// StackResult is the full result for the stack correlation command.
type StackResult struct {
	Duration    time.Duration     `json:"duration"`
	GeneratedAt time.Time         `json:"generated_at"`
	Groups      []CorrelatedGroup `json:"groups"`
}

type amAlerter interface {
	ListAlerts(ctx context.Context, opts *amlib.AlertListOptions) ([]amlib.Alert, error)
}

type promAlerter interface {
	Alerts(ctx context.Context) (*promlib.AlertsData, error)
}

type grafanaAlerter interface {
	AlertInstances(ctx context.Context) ([]grafanalib.GrafanaAlertInstance, error)
}

type lokiQueryer interface {
	QueryRange(ctx context.Context, logQL string, start, end time.Time, limit int) (*lokilib.QueryResult, error)
}

// RunStack correlates active alerts across Alertmanager, Prometheus, Grafana, and Loki.
func RunStack(ctx context.Context, amClient amAlerter, promClient promAlerter, grafanaClient grafanaAlerter, lokiClient lokiQueryer, opts StackOptions) (*StackResult, error) {
	if opts.Duration <= 0 {
		opts.Duration = 60
	}
	if opts.LogLimit <= 0 {
		opts.LogLimit = 50
	}
	if opts.SampleLimit <= 0 {
		opts.SampleLimit = 3
	}

	result := &StackResult{
		Duration:    time.Duration(opts.Duration) * time.Minute,
		GeneratedAt: time.Now().UTC(),
	}

	var signals []CorrelatedSignal

	active := true
	amAlerts, err := amClient.ListAlerts(ctx, &amlib.AlertListOptions{Active: &active})
	if err != nil {
		return nil, fmt.Errorf("listing Alertmanager alerts: %w", err)
	}
	for _, alert := range amAlerts {
		signals = append(signals, CorrelatedSignal{
			Source:   "alertmanager",
			Name:     firstNonEmpty(alert.Labels["alertname"], alert.Annotations["summary"], "unnamed-alert"),
			Service:  extractService(alert.Labels),
			Severity: normalizeSeverity(alert.Labels["severity"]),
			State:    firstNonEmpty(alert.Status.State, "active"),
			Summary:  firstNonEmpty(alert.Annotations["summary"], alert.Annotations["description"]),
			StartsAt: alert.StartsAt,
			Labels:   copyMap(alert.Labels),
		})
	}

	promAlerts, err := promClient.Alerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Prometheus alerts: %w", err)
	}
	for _, alert := range promAlerts.Alerts {
		signals = append(signals, CorrelatedSignal{
			Source:   "prometheus",
			Name:     firstNonEmpty(alert.Labels["alertname"], alert.Annotations["summary"], "unnamed-alert"),
			Service:  extractService(alert.Labels),
			Severity: normalizeSeverity(alert.Labels["severity"]),
			State:    firstNonEmpty(alert.State, "firing"),
			Summary:  firstNonEmpty(alert.Annotations["summary"], alert.Annotations["description"]),
			StartsAt: alert.ActiveAt,
			Labels:   copyMap(alert.Labels),
		})
	}

	grafanaAlerts, err := grafanaClient.AlertInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Grafana alert instances: %w", err)
	}
	for _, alert := range grafanaAlerts {
		signals = append(signals, CorrelatedSignal{
			Source:   "grafana",
			Name:     firstNonEmpty(alert.Labels["alertname"], alert.Annotations["summary"], "unnamed-alert"),
			Service:  extractService(alert.Labels),
			Severity: normalizeSeverity(alert.Labels["severity"]),
			State:    firstNonEmpty(alert.Status.State, "active"),
			Summary:  firstNonEmpty(alert.Annotations["summary"], alert.Annotations["description"]),
			StartsAt: alert.StartsAt,
			Labels:   copyMap(alert.Labels),
		})
	}

	result.Groups = groupSignals(signals)
	attachLokiLogs(ctx, result.Groups, lokiClient, opts)
	scoreGroups(result.Groups)
	sortGroups(result.Groups)
	return result, nil
}

func groupSignals(signals []CorrelatedSignal) []CorrelatedGroup {
	groups := make(map[string]*CorrelatedGroup)
	for _, sig := range signals {
		key := correlationKey(sig)
		group := groups[key]
		if group == nil {
			group = &CorrelatedGroup{
				Key:      key,
				Service:  sig.Service,
				Severity: sig.Severity,
			}
			groups[key] = group
		}
		group.Signals = append(group.Signals, sig)
		if sig.Service != "" && group.Service == "" {
			group.Service = sig.Service
		}
		group.Severity = higherSeverity(group.Severity, sig.Severity)
	}

	out := make([]CorrelatedGroup, 0, len(groups))
	for _, group := range groups {
		sourceSet := make(map[string]struct{})
		alertSet := make(map[string]struct{})
		for _, sig := range group.Signals {
			sourceSet[sig.Source] = struct{}{}
			alertSet[sig.Name] = struct{}{}
		}
		group.Sources = sortedKeys(sourceSet)
		group.AlertNames = sortedKeys(alertSet)
		out = append(out, *group)
	}
	return out
}

func attachLokiLogs(ctx context.Context, groups []CorrelatedGroup, lokiClient lokiQueryer, opts StackOptions) {
	if lokiClient == nil {
		return
	}

	start := time.Now().Add(-time.Duration(opts.Duration) * time.Minute)
	end := time.Now()
	cache := make(map[string][]lokilib.LogEntry)
	for i := range groups {
		service := groups[i].Service
		if service == "" {
			continue
		}
		if entries, ok := cache[service]; ok {
			groups[i].Logs = entries
			continue
		}

		queries := buildLokiQueries(service)
		var matched []lokilib.LogEntry
		for _, q := range queries {
			resp, err := lokiClient.QueryRange(ctx, q, start, end, opts.LogLimit)
			if err != nil {
				continue
			}
			entries := trimLogEntries(lokilib.ParseEntries(resp), opts.SampleLimit)
			if len(entries) > 0 {
				matched = entries
				break
			}
		}
		cache[service] = matched
		groups[i].Logs = matched
	}
}

func buildLokiQueries(service string) []string {
	quoted := strings.ReplaceAll(service, `"`, `\"`)
	return []string{
		fmt.Sprintf(`{service_name="%s"} |~ "(?i)(error|exception|timeout|fail|panic)"`, quoted),
		fmt.Sprintf(`{service="%s"} |~ "(?i)(error|exception|timeout|fail|panic)"`, quoted),
		fmt.Sprintf(`{app="%s"} |~ "(?i)(error|exception|timeout|fail|panic)"`, quoted),
		fmt.Sprintf(`{job="%s"} |~ "(?i)(error|exception|timeout|fail|panic)"`, quoted),
	}
}

func trimLogEntries(entries []lokilib.LogEntry, limit int) []lokilib.LogEntry {
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func scoreGroups(groups []CorrelatedGroup) {
	for i := range groups {
		sourceScore := len(groups[i].Sources) * 25
		logScore := len(groups[i].Logs) * 8
		severityScore := map[string]int{"critical": 30, "warning": 18, "info": 8, "unknown": 4}[groups[i].Severity]
		groups[i].Score = sourceScore + logScore + severityScore
	}
}

func sortGroups(groups []CorrelatedGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Score != groups[j].Score {
			return groups[i].Score > groups[j].Score
		}
		if groups[i].Service != groups[j].Service {
			return groups[i].Service < groups[j].Service
		}
		return groups[i].Key < groups[j].Key
	})
}

func correlationKey(sig CorrelatedSignal) string {
	service := sig.Service
	if service == "" {
		service = "unknown-service"
	}
	name := normalizeName(sig.Name)
	if name == "" {
		name = normalizeName(sig.Summary)
	}
	if name == "" {
		name = "generic-alert"
	}
	return service + "::" + name
}

func normalizeName(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = normalizeNonAlnum.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	return v
}

func normalizeSeverity(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical", "page", "fatal", "high":
		return "critical"
	case "warning", "warn", "medium":
		return "warning"
	case "info", "informational", "low":
		return "info"
	default:
		return "unknown"
	}
}

func higherSeverity(a, b string) string {
	rank := map[string]int{"critical": 4, "warning": 3, "info": 2, "unknown": 1, "": 0}
	if rank[b] > rank[a] {
		return b
	}
	if a == "" {
		return "unknown"
	}
	return a
}

func extractService(labels map[string]string) string {
	for _, key := range []string{"service", "service_name", "serviceName", "app", "job", "component", "namespace", "instance"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func copyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

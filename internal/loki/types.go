package loki

import "time"

// LokiConfig holds user config for connecting to a Loki instance.
type LokiConfig struct {
	URL       string    `yaml:"url" json:"url"`
	BasicAuth BasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
	TenantID  string    `yaml:"tenant_id,omitempty" json:"tenant_id,omitempty"` // X-Scope-OrgID for multi-tenant
}

// BasicAuth holds basic auth credentials.
type BasicAuth struct {
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
}

// IsConfigured returns true if the Loki URL is set.
func (c LokiConfig) IsConfigured() bool {
	return c.URL != ""
}

// QueryResult represents the response from /loki/api/v1/query or query_range.
type QueryResult struct {
	Status string     `json:"status"`
	Data   ResultData `json:"data"`
}

// ResultData holds the typed result from a Loki query.
type ResultData struct {
	ResultType string   `json:"resultType"` // "streams", "matrix", "vector"
	Result     []Stream `json:"result"`
}

// Stream represents a single log stream (a label set + entries).
type Stream struct {
	Labels  map[string]string `json:"stream"`
	Values  [][]string        `json:"values"` // Each entry: [timestamp_ns, log_line]
}

// LogEntry is a parsed log line with its timestamp and labels.
type LogEntry struct {
	Timestamp time.Time
	Line      string
	Labels    map[string]string
}

// LabelValuesResponse represents /loki/api/v1/label/<name>/values.
type LabelValuesResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

// LabelNamesResponse represents /loki/api/v1/labels.
type LabelNamesResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

// SeriesResponse represents /loki/api/v1/series.
type SeriesResponse struct {
	Status string              `json:"status"`
	Data   []map[string]string `json:"data"`
}

// StatsResponse represents /loki/api/v1/index/stats.
type StatsResponse struct {
	Streams uint64 `json:"streams"`
	Chunks  uint64 `json:"chunks"`
	Entries uint64 `json:"entries"`
	Bytes   uint64 `json:"bytes"`
}

// TailEntry represents a single entry from /loki/api/v1/tail (WebSocket).
type TailEntry struct {
	Streams []Stream `json:"streams"`
}

// BuildInfoResponse represents /loki/api/v1/status/buildinfo.
type BuildInfoResponse struct {
	Status string    `json:"status"`
	Data   BuildInfo `json:"data"`
}

// BuildInfo holds Loki version info.
type BuildInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildDate string `json:"buildDate"`
	BuildUser string `json:"buildUser"`
	GoVersion string `json:"goVersion"`
}

// Summary aggregates Loki instance statistics for the summary command.
type Summary struct {
	Healthy  bool
	Latency  time.Duration
	Version  string
	Labels   int
	Series   int
	Stats    *StatsResponse
}

package loki

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

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

// ResultData holds the typed result from a Loki query. Loki returns three
// shapes keyed by resultType: log streams, and Prometheus-style matrix or
// vector samples for metric LogQL (rate, count_over_time, ...).
// JSON tags matter only for marshaling (Go's default field-name encoding
// since ResultData has no MarshalJSON) — decoding always goes through
// UnmarshalJSON below, which parses the raw "resultType"/"result" keys
// itself regardless of these tags.
type ResultData struct {
	ResultType string         `json:"resultType"`
	Streams    []Stream       `json:"result,omitempty"`
	Series     []MetricSeries `json:"series,omitempty"`
}

// MetricSeries is one matrix/vector series.
type MetricSeries struct {
	Metric map[string]string `json:"metric"`
	Values []SamplePoint     `json:"values"`
}

// SamplePoint is one sample of a metric series.
type SamplePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

func (d *ResultData) UnmarshalJSON(b []byte) error {
	var raw struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	d.ResultType = raw.ResultType
	if len(raw.Result) == 0 {
		return nil
	}

	switch raw.ResultType {
	case "matrix":
		var rows []struct {
			Metric map[string]string    `json:"metric"`
			Values [][2]json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(raw.Result, &rows); err != nil {
			return fmt.Errorf("decoding matrix result: %w", err)
		}
		for _, r := range rows {
			s := MetricSeries{Metric: r.Metric}
			for _, v := range r.Values {
				if p, ok := samplePoint(v); ok {
					s.Values = append(s.Values, p)
				}
			}
			d.Series = append(d.Series, s)
		}
	case "vector":
		var rows []struct {
			Metric map[string]string  `json:"metric"`
			Value  [2]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw.Result, &rows); err != nil {
			return fmt.Errorf("decoding vector result: %w", err)
		}
		for _, r := range rows {
			s := MetricSeries{Metric: r.Metric}
			if p, ok := samplePoint(r.Value); ok {
				s.Values = append(s.Values, p)
			}
			d.Series = append(d.Series, s)
		}
	default: // "streams" and unknown types that carry stream-shaped results
		if err := json.Unmarshal(raw.Result, &d.Streams); err != nil {
			return fmt.Errorf("decoding streams result: %w", err)
		}
	}
	return nil
}

// samplePoint converts a [unix_seconds, "value"] pair. Loki emits the
// timestamp as a bare float number and the value as a quoted string, so each
// element is parsed from its raw JSON form.
func samplePoint(pair [2]json.RawMessage) (SamplePoint, bool) {
	var ts float64
	if err := json.Unmarshal(pair[0], &ts); err != nil {
		return SamplePoint{}, false
	}
	var valStr string
	if err := json.Unmarshal(pair[1], &valStr); err != nil {
		// tolerate a bare-number value as well
		var valNum float64
		if err2 := json.Unmarshal(pair[1], &valNum); err2 != nil {
			return SamplePoint{}, false
		}
		sec := int64(ts)
		return SamplePoint{Timestamp: time.Unix(sec, int64((ts-float64(sec))*1e9)), Value: valNum}, true
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return SamplePoint{}, false
	}
	sec := int64(ts)
	return SamplePoint{Timestamp: time.Unix(sec, int64((ts-float64(sec))*1e9)), Value: val}, true
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

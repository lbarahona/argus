package types

import "time"

// Instance represents a configured Signoz instance.
type Instance struct {
	URL        string `yaml:"url" json:"url"`
	APIKey     string `yaml:"api_key" json:"api_key"`
	Name       string `yaml:"name" json:"name"`
	APIVersion string `yaml:"api_version,omitempty" json:"api_version,omitempty"` // "v3" (self-hosted) or "v5" (cloud), default "v3"
}

// GetAPIVersion returns the API version, defaulting to "v3".
func (i Instance) GetAPIVersion() string {
	if i.APIVersion == "" {
		return "v3"
	}
	return i.APIVersion
}

// AIConfig holds configuration for AI providers.
type AIConfig struct {
	Provider     string        `yaml:"provider"`      // anthropic, openai, bedrock
	Model        string        `yaml:"model"`         // auto or specific model name
	AnthropicKey string        `yaml:"anthropic_key"`
	OpenAIKey    string        `yaml:"openai_key"`
	Bedrock      BedrockConfig `yaml:"bedrock"`
}

// BedrockConfig holds Bedrock-specific configuration.
type BedrockConfig struct {
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token"`
	Model    string `yaml:"model"`
}

// AlertmanagerConfig holds Alertmanager connection details.
type AlertmanagerConfig struct {
	URL       string `yaml:"url" json:"url"`
	BasicAuth struct {
		Username string `yaml:"username,omitempty" json:"username,omitempty"`
		Password string `yaml:"password,omitempty" json:"password,omitempty"`
	} `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// IsConfigured returns true if the Alertmanager URL is set.
func (c AlertmanagerConfig) IsConfigured() bool {
	return c.URL != ""
}

// PrometheusConfig holds Prometheus connection details.
type PrometheusConfig struct {
	URL       string `yaml:"url" json:"url"`
	BasicAuth struct {
		Username string `yaml:"username,omitempty" json:"username,omitempty"`
		Password string `yaml:"password,omitempty" json:"password,omitempty"`
	} `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// IsConfigured returns true if the Prometheus URL is set.
func (c PrometheusConfig) IsConfigured() bool {
	return c.URL != ""
}

// Config represents the application configuration.
type Config struct {
	AnthropicKey    string              `yaml:"anthropic_key"`     // Legacy (still works)
	AI              AIConfig            `yaml:"ai"`                // New structured AI config
	DefaultInstance string              `yaml:"default_instance"`
	Instances       map[string]Instance `yaml:"instances"`
	Alertmanager    AlertmanagerConfig  `yaml:"alertmanager,omitempty"` // Alertmanager integration
	Grafana         GrafanaConfig       `yaml:"grafana,omitempty"`      // Grafana integration
	Prometheus      PrometheusConfig    `yaml:"prometheus,omitempty"`   // Prometheus integration
}

// GrafanaConfig holds Grafana connection details.
type GrafanaConfig struct {
	URL    string `yaml:"url" json:"url"`
	APIKey string `yaml:"api_key,omitempty" json:"api_key,omitempty"` // Service account token or API key
}

// IsConfigured returns true if the Grafana URL is set.
func (c GrafanaConfig) IsConfigured() bool {
	return c.URL != ""
}

// GetAIConfig returns the effective AI config, merging legacy fields.
func (c Config) GetAIConfig() AIConfig {
	cfg := c.AI
	// Legacy fallback: if ai.anthropic_key is empty but root-level anthropic_key exists
	if cfg.AnthropicKey == "" && c.AnthropicKey != "" {
		cfg.AnthropicKey = c.AnthropicKey
	}
	// Default provider to anthropic
	if cfg.Provider == "" {
		cfg.Provider = "anthropic"
	}
	return cfg
}

// HealthStatus represents the health of a Signoz instance.
type HealthStatus struct {
	InstanceName string
	InstanceKey  string
	URL          string
	Healthy      bool
	Message      string
	Latency      time.Duration
}

// LogEntry represents a single log entry from Signoz.
type LogEntry struct {
	Timestamp    time.Time         `json:"timestamp"`
	Body         string            `json:"body"`
	SeverityText string            `json:"severity_text"`
	ServiceName  string            `json:"service_name"`
	Attributes   map[string]string `json:"attributes"`
}

// TraceEntry represents a single trace/span from Signoz.
type TraceEntry struct {
	Timestamp    time.Time         `json:"timestamp"`
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	ServiceName  string            `json:"service_name"`
	OperationName string           `json:"operation_name"`
	DurationNano int64             `json:"duration_nano"`
	StatusCode   string            `json:"status_code"`
	Attributes   map[string]string `json:"attributes"`
}

// DurationMs returns the duration in milliseconds.
func (t TraceEntry) DurationMs() float64 {
	return float64(t.DurationNano) / 1e6
}

// MetricEntry represents a metric data point from Signoz.
type MetricEntry struct {
	Timestamp  time.Time         `json:"timestamp"`
	MetricName string            `json:"metric_name"`
	Value      float64           `json:"value"`
	Labels     map[string]string `json:"labels"`
}

// QueryResult holds results from a Signoz query.
type QueryResult struct {
	Logs    []LogEntry        `json:"logs,omitempty"`
	Traces  []TraceEntry      `json:"traces,omitempty"`
	Metrics []MetricEntry     `json:"metrics,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// Service represents a service discovered in Signoz.
type Service struct {
	Name      string  `json:"serviceName"`
	NumErrors int     `json:"numErrors"`
	NumCalls  int     `json:"numCalls"`
	ErrorRate float64 `json:"errorRate,omitempty"`
}

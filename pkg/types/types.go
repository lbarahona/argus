package types

import "time"

// Instance represents a configured Signoz instance.
type Instance struct {
	URL    string `yaml:"url" json:"url"`
	APIKey string `yaml:"api_key" json:"api_key"`
	Name   string `yaml:"name" json:"name"`
}

// Config represents the application configuration.
type Config struct {
	AnthropicKey    string              `yaml:"anthropic_key"`
	DefaultInstance string              `yaml:"default_instance"`
	Instances       map[string]Instance `yaml:"instances"`
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

// QueryResult holds results from a Signoz query.
type QueryResult struct {
	Logs    []LogEntry        `json:"logs,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// Service represents a service discovered in Signoz.
type Service struct {
	Name      string `json:"serviceName"`
	NumErrors int    `json:"numErrors"`
	NumCalls  int    `json:"numCalls"`
}

package alertmanager

import "time"

// Alert represents an Alertmanager alert.
type Alert struct {
	Annotations  map[string]string `json:"annotations"`
	EndsAt       time.Time         `json:"endsAt"`
	Fingerprint  string            `json:"fingerprint"`
	Receivers    []Receiver        `json:"receivers"`
	StartsAt     time.Time         `json:"startsAt"`
	Status       AlertStatus       `json:"status"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	GeneratorURL string            `json:"generatorURL"`
	Labels       map[string]string `json:"labels"`
}

// AlertStatus holds the inhibition/silencing/active state.
type AlertStatus struct {
	InhibitedBy []string `json:"inhibitedBy"`
	SilencedBy  []string `json:"silencedBy"`
	State       string   `json:"state"` // "active", "suppressed", "unprocessed"
}

// Receiver represents a notification receiver.
type Receiver struct {
	Name string `json:"name"`
}

// AlertGroup represents a group of alerts.
type AlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver Receiver          `json:"receiver"`
	Alerts   []Alert           `json:"alerts"`
}

// Silence represents a silence entry.
type Silence struct {
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
	Comment   string    `json:"comment"`
	CreatedBy string    `json:"createdBy"`
	EndsAt    time.Time `json:"endsAt"`
	StartsAt  time.Time `json:"startsAt"`
	Matchers  []Matcher `json:"matchers"`
}

// Status holds the state of a silence.
type Status struct {
	State string `json:"state"` // "active", "pending", "expired"
}

// Matcher defines a label matcher for silences.
type Matcher struct {
	IsEqual bool   `json:"isEqual"`
	IsRegex bool   `json:"isRegex"`
	Name    string `json:"name"`
	Value   string `json:"value"`
}

// SilenceRequest is used to create or update a silence.
type SilenceRequest struct {
	ID        string    `json:"id,omitempty"`
	Matchers  []Matcher `json:"matchers"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	CreatedBy string    `json:"createdBy"`
	Comment   string    `json:"comment"`
}

// SilenceResponse is the Alertmanager response when creating a silence.
type SilenceResponse struct {
	SilenceID string `json:"silenceID"`
}

// AMStatus represents Alertmanager cluster status from /api/v2/status.
type AMStatus struct {
	Cluster     ClusterStatus `json:"cluster"`
	VersionInfo VersionInfo   `json:"versionInfo"`
	Uptime      time.Time     `json:"uptime"`
}

// ClusterStatus holds cluster peer info.
type ClusterStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Peers  []Peer `json:"peers"`
}

// Peer represents a cluster peer.
type Peer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// VersionInfo holds Alertmanager version details.
type VersionInfo struct {
	Branch    string `json:"branch"`
	BuildDate string `json:"buildDate"`
	BuildUser string `json:"buildUser"`
	GoVersion string `json:"goVersion"`
	Revision  string `json:"revision"`
	Version   string `json:"version"`
}

// AlertmanagerConfig holds user config for connecting to Alertmanager.
type AlertmanagerConfig struct {
	URL       string    `yaml:"url" json:"url"`
	BasicAuth BasicAuth `yaml:"basic_auth,omitempty" json:"basic_auth,omitempty"`
}

// BasicAuth holds basic auth credentials.
type BasicAuth struct {
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
}

// IsConfigured returns true if the URL is set.
func (c AlertmanagerConfig) IsConfigured() bool {
	return c.URL != ""
}

// AlertSummary provides a quick overview of alert state.
type AlertSummary struct {
	TotalAlerts      int
	ActiveAlerts     int
	SuppressedAlerts int
	FiringByName     map[string]int // alertname -> count
	BySeverity       map[string]int // severity -> count
}

// BuildSummary creates a summary from a list of alerts.
func BuildSummary(alerts []Alert) AlertSummary {
	s := AlertSummary{
		TotalAlerts:  len(alerts),
		FiringByName: make(map[string]int),
		BySeverity:   make(map[string]int),
	}
	for _, a := range alerts {
		switch a.Status.State {
		case "active":
			s.ActiveAlerts++
		default:
			s.SuppressedAlerts++
		}
		if name, ok := a.Labels["alertname"]; ok {
			s.FiringByName[name]++
		}
		sev := a.Labels["severity"]
		if sev == "" {
			sev = "unknown"
		}
		s.BySeverity[sev]++
	}
	return s
}

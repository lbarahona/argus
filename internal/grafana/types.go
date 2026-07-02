package grafana

import "time"

// GrafanaConfig holds user config for connecting to Grafana.
type GrafanaConfig struct {
	URL    string `yaml:"url" json:"url"`
	APIKey string `yaml:"api_key,omitempty" json:"api_key,omitempty"` // Service account token or API key
}

// IsConfigured returns true if the URL is set.
func (c GrafanaConfig) IsConfigured() bool {
	return c.URL != ""
}

// Dashboard represents a Grafana dashboard.
type Dashboard struct {
	ID          int      `json:"id"`
	UID         string   `json:"uid"`
	Title       string   `json:"title"`
	URI         string   `json:"uri"`
	URL         string   `json:"url"`
	Slug        string   `json:"slug"`
	Type        string   `json:"type"` // "dash-db" or "dash-folder"
	Tags        []string `json:"tags"`
	IsStarred   bool     `json:"isStarred"`
	FolderID    int      `json:"folderId"`
	FolderUID   string   `json:"folderUid"`
	FolderTitle string   `json:"folderTitle"`
	FolderURL   string   `json:"folderUrl"`
}

// DashboardMeta holds full dashboard details from /api/dashboards/uid/:uid.
type DashboardMeta struct {
	Meta      Meta                   `json:"meta"`
	Dashboard map[string]interface{} `json:"dashboard"`
}

// Meta holds dashboard metadata.
type Meta struct {
	Type        string    `json:"type"`
	CanSave     bool      `json:"canSave"`
	CanEdit     bool      `json:"canEdit"`
	CanAdmin    bool      `json:"canAdmin"`
	CanStar     bool      `json:"canStar"`
	CanDelete   bool      `json:"canDelete"`
	Slug        string    `json:"slug"`
	URL         string    `json:"url"`
	Expires     string    `json:"expires"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	UpdatedBy   string    `json:"updatedBy"`
	CreatedBy   string    `json:"createdBy"`
	Version     int       `json:"version"`
	FolderID    int       `json:"folderId"`
	FolderUID   string    `json:"folderUid"`
	FolderTitle string    `json:"folderTitle"`
	FolderURL   string    `json:"folderUrl"`
}

// Datasource represents a Grafana data source.
type Datasource struct {
	ID        int    `json:"id"`
	UID       string `json:"uid"`
	OrgID     int    `json:"orgId"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "prometheus", "loki", "elasticsearch", etc.
	TypeName  string `json:"typeName"`
	Access    string `json:"access"` // "proxy" or "direct"
	URL       string `json:"url"`
	IsDefault bool   `json:"isDefault"`
	ReadOnly  bool   `json:"readOnly"`
}

// AlertRule represents a Grafana alerting rule (unified alerting / Grafana 9+).
type AlertRule struct {
	ID           int64             `json:"id"`
	UID          string            `json:"uid"`
	OrgID        int64             `json:"orgId"`
	FolderUID    string            `json:"folderUID"`
	RuleGroup    string            `json:"ruleGroup"`
	Title        string            `json:"title"`
	Condition    string            `json:"condition"`
	NoDataState  string            `json:"noDataState"`
	ExecErrState string            `json:"execErrState"`
	For          string            `json:"for"`
	Annotations  map[string]string `json:"annotations"`
	Labels       map[string]string `json:"labels"`
	Updated      time.Time         `json:"updated"`
}

// AlertRuleGroup represents a group of alert rules.
type AlertRuleGroup struct {
	FolderUID string      `json:"folderUid"`
	Name      string      `json:"name"`
	Interval  int         `json:"interval"`
	Rules     []AlertRule `json:"rules"`
}

// ProvisionedAlertRules wraps the response from /api/v1/provisioning/alert-rules.
type ProvisionedAlertRules []AlertRule

// GrafanaAlertInstance represents a firing alert instance from /api/alertmanager/grafana/api/v2/alerts.
type GrafanaAlertInstance struct {
	Annotations  map[string]string   `json:"annotations"`
	EndsAt       time.Time           `json:"endsAt"`
	Fingerprint  string              `json:"fingerprint"`
	Receivers    []Receiver          `json:"receivers"`
	StartsAt     time.Time           `json:"startsAt"`
	Status       AlertInstanceStatus `json:"status"`
	Labels       map[string]string   `json:"labels"`
	GeneratorURL string              `json:"generatorURL"`
	UpdatedAt    time.Time           `json:"updatedAt"`
}

// Receiver holds receiver info.
type Receiver struct {
	Name string `json:"name"`
}

// AlertInstanceStatus holds alert instance state.
type AlertInstanceStatus struct {
	InhibitedBy []string `json:"inhibitedBy"`
	SilencedBy  []string `json:"silencedBy"`
	State       string   `json:"state"` // "active", "suppressed", "unprocessed"
}

// Folder represents a Grafana folder.
type Folder struct {
	ID        int       `json:"id"`
	UID       string    `json:"uid"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	HasACL    bool      `json:"hasAcl"`
	CanSave   bool      `json:"canSave"`
	CanEdit   bool      `json:"canEdit"`
	CanAdmin  bool      `json:"canAdmin"`
	CanDelete bool      `json:"canDelete"`
	CreatedBy string    `json:"createdBy"`
	Created   time.Time `json:"created"`
	UpdatedBy string    `json:"updatedBy"`
	Updated   time.Time `json:"updated"`
	Version   int       `json:"version"`
}

// OrgInfo represents the current org from /api/org.
type OrgInfo struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// HealthResponse from /api/health.
type HealthResponse struct {
	Commit   string `json:"commit"`
	Database string `json:"database"`
	Version  string `json:"version"`
}

// SearchResult is the union type for search (can be dashboard or folder).
type SearchResult = Dashboard

// Summary aggregates Grafana instance statistics.
type Summary struct {
	Dashboards   int
	Folders      int
	Datasources  int
	AlertRules   int
	FiringAlerts int
	Version      string
	OrgName      string
}

package grafana

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newMockServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		kind := r.URL.Query().Get("type")

		results := []Dashboard{
			{ID: 1, UID: "dash-1", Title: "CPU Metrics", Type: "dash-db", FolderTitle: "Infrastructure", Tags: []string{"prod"}},
			{ID: 2, UID: "dash-2", Title: "Memory", Type: "dash-db", FolderTitle: "Infrastructure"},
			{ID: 3, UID: "folder-1", Title: "Infrastructure", Type: "dash-folder"},
			{ID: 4, UID: "dash-3", Title: "API Latency", Type: "dash-db", FolderTitle: "Services"},
		}

		var filtered []Dashboard
		for _, d := range results {
			if kind != "" && d.Type != kind {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(d.Title), strings.ToLower(q)) {
				continue
			}
			filtered = append(filtered, d)
		}

		json.NewEncoder(w).Encode(filtered)
	})

	mux.HandleFunc("/api/dashboards/uid/", func(w http.ResponseWriter, r *http.Request) {
		uid := strings.TrimPrefix(r.URL.Path, "/api/dashboards/uid/")
		if uid == "not-found" {
			http.Error(w, `{"message":"Dashboard not found"}`, 404)
			return
		}
		dm := DashboardMeta{
			Meta: Meta{
				Slug:        "test-dash",
				Version:     5,
				FolderTitle: "Test Folder",
				CreatedBy:   "admin",
				UpdatedBy:   "admin",
			},
			Dashboard: map[string]interface{}{
				"title":       "Test Dashboard",
				"description": "A test dashboard",
				"tags":        []interface{}{"prod", "sre"},
				"panels": []interface{}{
					map[string]interface{}{"type": "graph", "title": "Panel 1"},
					map[string]interface{}{"type": "stat", "title": "Panel 2"},
					map[string]interface{}{"type": "graph", "title": "Panel 3"},
				},
			},
		}
		json.NewEncoder(w).Encode(dm)
	})

	mux.HandleFunc("/api/folders", func(w http.ResponseWriter, r *http.Request) {
		folders := []Folder{
			{ID: 1, UID: "f-1", Title: "Infrastructure", URL: "/dashboards/f/f-1/infrastructure"},
			{ID: 2, UID: "f-2", Title: "Services", URL: "/dashboards/f/f-2/services"},
		}
		json.NewEncoder(w).Encode(folders)
	})

	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		ds := []Datasource{
			{ID: 1, UID: "prom-1", Name: "Prometheus", Type: "prometheus", URL: "http://prometheus:9090", IsDefault: true, Access: "proxy"},
			{ID: 2, UID: "loki-1", Name: "Loki", Type: "loki", URL: "http://loki:3100", Access: "proxy"},
			{ID: 3, UID: "pg-1", Name: "PostgreSQL", Type: "postgres", URL: "localhost:5432", Access: "proxy", ReadOnly: true},
		}
		json.NewEncoder(w).Encode(ds)
	})

	mux.HandleFunc("/api/v1/provisioning/alert-rules", func(w http.ResponseWriter, r *http.Request) {
		rules := []AlertRule{
			{ID: 1, UID: "rule-1", Title: "High CPU", RuleGroup: "infra", For: "5m", NoDataState: "NoData", ExecErrState: "Alerting", Labels: map[string]string{"severity": "critical"}, Annotations: map[string]string{"summary": "CPU above 90%"}},
			{ID: 2, UID: "rule-2", Title: "High Memory", RuleGroup: "infra", For: "10m", NoDataState: "NoData", ExecErrState: "OK", Labels: map[string]string{"severity": "warning"}},
			{ID: 3, UID: "rule-3", Title: "Error Rate", RuleGroup: "services", For: "2m", NoDataState: "Alerting", ExecErrState: "Alerting", Labels: map[string]string{"severity": "critical"}},
		}
		json.NewEncoder(w).Encode(rules)
	})

	mux.HandleFunc("/api/alertmanager/grafana/api/v2/alerts", func(w http.ResponseWriter, r *http.Request) {
		instances := []GrafanaAlertInstance{
			{
				Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical"},
				Annotations: map[string]string{"summary": "CPU at 95%"},
				Status:      AlertInstanceStatus{State: "active"},
			},
			{
				Labels:      map[string]string{"alertname": "DiskSpace", "severity": "warning"},
				Annotations: map[string]string{"summary": "Disk 85% full"},
				Status:      AlertInstanceStatus{State: "suppressed"},
			},
		}
		json.NewEncoder(w).Encode(instances)
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		h := HealthResponse{
			Commit:   "abc1234",
			Database: "ok",
			Version:  "10.3.1",
		}
		json.NewEncoder(w).Encode(h)
	})

	mux.HandleFunc("/api/org", func(w http.ResponseWriter, r *http.Request) {
		org := OrgInfo{ID: 1, Name: "Main Org"}
		json.NewEncoder(w).Encode(org)
	})

	return httptest.NewServer(mux)
}

func TestClient_Dashboards(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	dashboards, err := client.Dashboards(context.Background())
	if err != nil {
		t.Fatalf("Dashboards: %v", err)
	}
	// Should only return dash-db type
	for _, d := range dashboards {
		if d.Type != "dash-db" {
			t.Errorf("expected dash-db, got %s for %s", d.Type, d.Title)
		}
	}
	if len(dashboards) != 3 {
		t.Errorf("got %d dashboards, want 3", len(dashboards))
	}
}

func TestClient_Search(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	// Search all
	results, err := client.Search(context.Background(), "", "", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("got %d results, want 4", len(results))
	}

	// Search by query
	results, err = client.Search(context.Background(), "CPU", "", 0)
	if err != nil {
		t.Fatalf("Search CPU: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results for CPU, want 1", len(results))
	}

	// Search folders only
	results, err = client.Search(context.Background(), "", "dash-folder", 0)
	if err != nil {
		t.Fatalf("Search folders: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d folders, want 1", len(results))
	}
}

func TestClient_GetDashboard(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	dm, err := client.GetDashboard(context.Background(), "dash-1")
	if err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}

	if dm.Meta.Slug != "test-dash" {
		t.Errorf("Slug = %q, want test-dash", dm.Meta.Slug)
	}
	if title, ok := dm.Dashboard["title"].(string); !ok || title != "Test Dashboard" {
		t.Errorf("title = %v, want Test Dashboard", dm.Dashboard["title"])
	}
}

func TestClient_GetDashboard_EscapesUID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(DashboardMeta{})
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())

	uid := "a/b?c"
	if _, err := client.GetDashboard(context.Background(), uid); err != nil {
		t.Fatalf("GetDashboard: %v", err)
	}

	want := "/api/dashboards/uid/" + url.PathEscape(uid)
	if gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

func TestClient_GetDashboard_NotFound(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	_, err := client.GetDashboard(context.Background(), "not-found")
	if err == nil {
		t.Fatal("expected error for not-found dashboard")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got: %v", err)
	}
}

func TestClient_Folders(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	folders, err := client.Folders(context.Background())
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != 2 {
		t.Errorf("got %d folders, want 2", len(folders))
	}
	if folders[0].Title != "Infrastructure" {
		t.Errorf("folder[0].Title = %q, want Infrastructure", folders[0].Title)
	}
}

func TestClient_Datasources(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	ds, err := client.Datasources(context.Background())
	if err != nil {
		t.Fatalf("Datasources: %v", err)
	}
	if len(ds) != 3 {
		t.Errorf("got %d datasources, want 3", len(ds))
	}

	// Check default
	hasDefault := false
	for _, d := range ds {
		if d.IsDefault {
			hasDefault = true
			if d.Name != "Prometheus" {
				t.Errorf("default ds = %q, want Prometheus", d.Name)
			}
		}
	}
	if !hasDefault {
		t.Error("no default datasource found")
	}
}

func TestClient_AlertRules(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	rules, err := client.AlertRules(context.Background())
	if err != nil {
		t.Fatalf("AlertRules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("got %d rules, want 3", len(rules))
	}
}

func TestClient_AlertInstances(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	instances, err := client.AlertInstances(context.Background())
	if err != nil {
		t.Fatalf("AlertInstances: %v", err)
	}
	if len(instances) != 2 {
		t.Errorf("got %d instances, want 2", len(instances))
	}

	active := 0
	for _, inst := range instances {
		if inst.Status.State == "active" {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active = %d, want 1", active)
	}
}

func TestClient_Health(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Version != "10.3.1" {
		t.Errorf("Version = %q, want 10.3.1", health.Version)
	}
	if health.Database != "ok" {
		t.Errorf("Database = %q, want ok", health.Database)
	}
}

func TestClient_Org(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	org, err := client.Org(context.Background())
	if err != nil {
		t.Fatalf("Org: %v", err)
	}
	if org.Name != "Main Org" {
		t.Errorf("Name = %q, want Main Org", org.Name)
	}
	if org.ID != 1 {
		t.Errorf("ID = %d, want 1", org.ID)
	}
}

func TestClient_BuildSummary(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	summary, err := client.BuildSummary(context.Background())
	if err != nil {
		t.Fatalf("BuildSummary: %v", err)
	}

	if summary.Dashboards != 3 {
		t.Errorf("Dashboards = %d, want 3", summary.Dashboards)
	}
	if summary.Folders != 1 {
		t.Errorf("Folders = %d, want 1", summary.Folders)
	}
	if summary.Datasources != 3 {
		t.Errorf("Datasources = %d, want 3", summary.Datasources)
	}
	if summary.AlertRules != 3 {
		t.Errorf("AlertRules = %d, want 3", summary.AlertRules)
	}
	if summary.FiringAlerts != 1 {
		t.Errorf("FiringAlerts = %d, want 1", summary.FiringAlerts)
	}
	if summary.Version != "10.3.1" {
		t.Errorf("Version = %q, want 10.3.1", summary.Version)
	}
	if summary.OrgName != "Main Org" {
		t.Errorf("OrgName = %q, want Main Org", summary.OrgName)
	}
}

func TestClient_AuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(HealthResponse{Version: "10.3.1"})
	}))
	defer srv.Close()

	client := NewClientWithHTTPAndKey(srv.URL, srv.Client(), "glsa_test_token")
	_, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	if gotAuth != "Bearer glsa_test_token" {
		t.Errorf("Authorization = %q, want Bearer glsa_test_token", gotAuth)
	}
}

func TestClient_NoAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(HealthResponse{Version: "10.3.1"})
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	_, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	if gotAuth != "" {
		t.Errorf("Authorization should be empty, got %q", gotAuth)
	}
}

func TestClient_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", 500)
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())

	_, err := client.Dashboards(context.Background())
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 error, got: %v", err)
	}
}

func TestClient_Unreachable(t *testing.T) {
	client := NewClientWithHTTP("http://localhost:1", &http.Client{})

	_, err := client.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestClient_TrailingSlash(t *testing.T) {
	srv := newMockServer()
	defer srv.Close()
	// URL with trailing slash
	client := NewClientWithHTTP(srv.URL+"/", srv.Client())

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.Version != "10.3.1" {
		t.Errorf("Version = %q, want 10.3.1", health.Version)
	}
}

func TestNewClient(t *testing.T) {
	cfg := GrafanaConfig{
		URL:    "http://grafana:3000",
		APIKey: "glsa_xxx",
	}
	client := NewClient(cfg)
	if client.baseURL != "http://grafana:3000" {
		t.Errorf("baseURL = %q, want http://grafana:3000", client.baseURL)
	}
	if client.apiKey != "glsa_xxx" {
		t.Errorf("apiKey = %q, want glsa_xxx", client.apiKey)
	}
}

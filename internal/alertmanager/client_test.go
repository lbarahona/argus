package alertmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockAMServer creates a test Alertmanager API server.
func mockAMServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v2/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(AMStatus{
			VersionInfo: VersionInfo{Version: "0.27.0"},
			Cluster:     ClusterStatus{Name: "test-cluster", Status: "ready", Peers: []Peer{{Name: "peer1", Address: "10.0.0.1:9094"}}},
		})
	})

	mux.HandleFunc("/api/v2/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts/groups" {
			json.NewEncoder(w).Encode([]AlertGroup{
				{
					Labels:   map[string]string{"alertname": "HighLatency"},
					Receiver: Receiver{Name: "default"},
					Alerts: []Alert{
						{
							Fingerprint: "abc123",
							Status:      AlertStatus{State: "active"},
							Labels:      map[string]string{"alertname": "HighLatency", "severity": "critical"},
							Annotations: map[string]string{"summary": "High latency detected"},
							StartsAt:    time.Now().Add(-30 * time.Minute),
						},
					},
				},
			})
			return
		}

		alerts := []Alert{
			{
				Fingerprint: "abc123",
				Status:      AlertStatus{State: "active"},
				Labels:      map[string]string{"alertname": "HighLatency", "severity": "critical", "instance": "web-1"},
				Annotations: map[string]string{"summary": "High latency detected on web-1"},
				StartsAt:    time.Now().Add(-30 * time.Minute),
				Receivers:   []Receiver{{Name: "slack"}},
			},
			{
				Fingerprint: "def456",
				Status:      AlertStatus{State: "active"},
				Labels:      map[string]string{"alertname": "DiskFull", "severity": "warning", "instance": "db-1"},
				Annotations: map[string]string{"summary": "Disk usage above 90%"},
				StartsAt:    time.Now().Add(-2 * time.Hour),
			},
			{
				Fingerprint: "ghi789",
				Status:      AlertStatus{State: "suppressed", SilencedBy: []string{"silence-001"}},
				Labels:      map[string]string{"alertname": "HighCPU", "severity": "warning"},
				Annotations: map[string]string{"summary": "CPU above 80%"},
				StartsAt:    time.Now().Add(-10 * time.Minute),
			},
		}
		json.NewEncoder(w).Encode(alerts)
	})

	mux.HandleFunc("/api/v2/alerts/groups", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]AlertGroup{})
	})

	mux.HandleFunc("/api/v2/silences", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var req SilenceRequest
			json.NewDecoder(r.Body).Decode(&req)
			json.NewEncoder(w).Encode(SilenceResponse{SilenceID: "new-silence-001"})
			return
		}
		silences := []Silence{
			{
				ID:        "silence-001",
				Status:    Status{State: "active"},
				Comment:   "Deploying fix for CPU issue",
				CreatedBy: "sre-team",
				StartsAt:  time.Now().Add(-time.Hour),
				EndsAt:    time.Now().Add(time.Hour),
				Matchers: []Matcher{
					{Name: "alertname", Value: "HighCPU", IsEqual: true},
				},
			},
			{
				ID:        "silence-002",
				Status:    Status{State: "expired"},
				Comment:   "Maintenance window",
				CreatedBy: "ops",
				StartsAt:  time.Now().Add(-24 * time.Hour),
				EndsAt:    time.Now().Add(-12 * time.Hour),
				Matchers: []Matcher{
					{Name: "env", Value: "staging", IsEqual: true},
				},
			},
		}
		json.NewEncoder(w).Encode(silences)
	})

	mux.HandleFunc("/api/v2/silence/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		json.NewEncoder(w).Encode(Silence{
			ID:        "silence-001",
			Status:    Status{State: "active"},
			Comment:   "Test silence",
			CreatedBy: "argus",
		})
	})

	mux.HandleFunc("/api/v2/receivers", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Receiver{
			{Name: "default"},
			{Name: "slack-critical"},
			{Name: "pagerduty"},
		})
	})

	return httptest.NewServer(mux)
}

func TestClient_Healthy(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	healthy, latency, err := client.Healthy(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy")
	}
	if latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestClient_Status(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	status, err := client.Status(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.VersionInfo.Version != "0.27.0" {
		t.Errorf("version = %q, want 0.27.0", status.VersionInfo.Version)
	}
	if status.Cluster.Name != "test-cluster" {
		t.Errorf("cluster name = %q, want test-cluster", status.Cluster.Name)
	}
	if len(status.Cluster.Peers) != 1 {
		t.Errorf("peers = %d, want 1", len(status.Cluster.Peers))
	}
}

func TestClient_ListAlerts(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	alerts, err := client.ListAlerts(context.Background(), nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(alerts) != 3 {
		t.Fatalf("expected 3 alerts, got %d", len(alerts))
	}

	// Check first alert
	if alerts[0].Labels["alertname"] != "HighLatency" {
		t.Errorf("first alert = %q, want HighLatency", alerts[0].Labels["alertname"])
	}
	if alerts[0].Status.State != "active" {
		t.Errorf("first alert state = %q, want active", alerts[0].Status.State)
	}
}

func TestClient_ListAlerts_WithOptions(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	active := true
	opts := &AlertListOptions{
		Active: &active,
		Filter: []string{`alertname="HighLatency"`},
	}
	alerts, err := client.ListAlerts(context.Background(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Mock returns all alerts regardless of filter, but the call should succeed
	if len(alerts) == 0 {
		t.Error("expected at least one alert")
	}
}

func TestClient_ListAlertGroups(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	groups, err := client.ListAlertGroups(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic or error
	_ = groups
}

func TestClient_ListSilences(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	silences, err := client.ListSilences(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(silences) != 2 {
		t.Fatalf("expected 2 silences, got %d", len(silences))
	}
	if silences[0].Comment != "Deploying fix for CPU issue" {
		t.Errorf("first silence comment = %q", silences[0].Comment)
	}
}

func TestClient_GetSilence(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	silence, err := client.GetSilence(context.Background(), "silence-001")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if silence.ID != "silence-001" {
		t.Errorf("silence ID = %q, want silence-001", silence.ID)
	}
}

func TestClient_CreateSilence(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	now := time.Now()
	req := SilenceRequest{
		Matchers:  []Matcher{{Name: "alertname", Value: "HighCPU", IsEqual: true}},
		StartsAt:  now,
		EndsAt:    now.Add(2 * time.Hour),
		CreatedBy: "argus-test",
		Comment:   "Test silence creation",
	}

	id, err := client.CreateSilence(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "new-silence-001" {
		t.Errorf("silence ID = %q, want new-silence-001", id)
	}
}

func TestClient_DeleteSilence(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	err := client.DeleteSilence(context.Background(), "silence-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_ListReceivers(t *testing.T) {
	srv := mockAMServer(t)
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())
	receivers, err := client.ListReceivers(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(receivers) != 3 {
		t.Fatalf("expected 3 receivers, got %d", len(receivers))
	}
	names := make(map[string]bool)
	for _, r := range receivers {
		names[r.Name] = true
	}
	for _, expected := range []string{"default", "slack-critical", "pagerduty"} {
		if !names[expected] {
			t.Errorf("missing receiver %q", expected)
		}
	}
}

func TestClient_Error_Handling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, srv.Client())

	_, err := client.ListAlerts(context.Background(), nil)
	if err == nil {
		t.Error("expected error for 500 response")
	}

	_, err = client.Status(context.Background())
	if err == nil {
		t.Error("expected error for 500 response")
	}

	_, err = client.ListSilences(context.Background())
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestClient_Unreachable(t *testing.T) {
	client := NewClientWithHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond})

	healthy, _, err := client.Healthy(context.Background())
	if err == nil {
		t.Error("expected error for unreachable server")
	}
	if healthy {
		t.Error("should not be healthy")
	}
}

func TestClient_BasicAuth(t *testing.T) {
	var gotAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		gotAuth = ok && user == "admin" && pass == "secret"
		json.NewEncoder(w).Encode(AMStatus{VersionInfo: VersionInfo{Version: "test"}})
	}))
	defer srv.Close()

	cfg := AlertmanagerConfig{
		URL:       srv.URL,
		BasicAuth: BasicAuth{Username: "admin", Password: "secret"},
	}
	client := NewClient(cfg)
	_, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotAuth {
		t.Error("basic auth was not sent")
	}
}

func TestNewClient_TrailingSlash(t *testing.T) {
	cfg := AlertmanagerConfig{URL: "http://localhost:9093/"}
	client := NewClient(cfg)
	if client.baseURL != "http://localhost:9093" {
		t.Errorf("baseURL = %q, should strip trailing slash", client.baseURL)
	}
}

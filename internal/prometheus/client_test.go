package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mockPromServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/rules", func(w http.ResponseWriter, r *http.Request) {
		ruleType := r.URL.Query().Get("type")
		groups := []RuleGroup{
			{
				Name:     "test-group",
				File:     "/etc/prometheus/rules.yml",
				Interval: 60,
				Rules: []Rule{
					{
						Name:        "HighErrorRate",
						Query:       `rate(http_errors_total[5m]) > 0.05`,
						Type:        "alerting",
						State:       "firing",
						Health:      "ok",
						Duration:    300,
						Labels:      map[string]string{"severity": "critical"},
						Annotations: map[string]string{"summary": "Error rate is high"},
						Alerts: []RuleAlert{
							{
								Labels:   map[string]string{"instance": "web-1"},
								State:    "firing",
								ActiveAt: time.Now().Add(-10 * time.Minute),
								Value:    "0.12",
							},
						},
					},
					{
						Name:   "http_request_rate:5m",
						Query:  `rate(http_requests_total[5m])`,
						Type:   "recording",
						Health: "ok",
					},
				},
			},
		}

		if ruleType == "alert" {
			for i := range groups {
				var filtered []Rule
				for _, r := range groups[i].Rules {
					if r.Type == "alerting" {
						filtered = append(filtered, r)
					}
				}
				groups[i].Rules = filtered
			}
		} else if ruleType == "record" {
			for i := range groups {
				var filtered []Rule
				for _, r := range groups[i].Rules {
					if r.Type == "recording" {
						filtered = append(filtered, r)
					}
				}
				groups[i].Rules = filtered
			}
		}

		data, _ := json.Marshal(RulesData{Groups: groups})
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		data, _ := json.Marshal(AlertsData{
			Alerts: []PromAlert{
				{
					Labels:      map[string]string{"alertname": "HighErrorRate", "severity": "critical", "instance": "web-1"},
					Annotations: map[string]string{"summary": "Error rate above threshold"},
					State:       "firing",
					ActiveAt:    time.Now().Add(-10 * time.Minute),
					Value:       "0.12",
				},
				{
					Labels:      map[string]string{"alertname": "DiskAlmostFull", "severity": "warning"},
					Annotations: map[string]string{"summary": "Disk is 85% full"},
					State:       "pending",
					ActiveAt:    time.Now().Add(-2 * time.Minute),
					Value:       "0.85",
				},
			},
		})
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/targets", func(w http.ResponseWriter, r *http.Request) {
		data, _ := json.Marshal(TargetsData{
			ActiveTargets: []Target{
				{
					Labels:        map[string]string{"instance": "localhost:9090", "job": "prometheus"},
					ScrapePool:    "prometheus",
					ScrapeURL:     "http://localhost:9090/metrics",
					Health:        "up",
					LastScrape:    time.Now().Add(-15 * time.Second),
					LastScrapeDur: 0.015,
				},
				{
					Labels:        map[string]string{"instance": "web-1:8080", "job": "web"},
					ScrapePool:    "web",
					ScrapeURL:     "http://web-1:8080/metrics",
					Health:        "up",
					LastScrape:    time.Now().Add(-30 * time.Second),
					LastScrapeDur: 0.045,
				},
				{
					Labels:        map[string]string{"instance": "web-2:8080", "job": "web"},
					ScrapePool:    "web",
					ScrapeURL:     "http://web-2:8080/metrics",
					Health:        "down",
					LastError:     "connection refused",
					LastScrape:    time.Now().Add(-30 * time.Second),
					LastScrapeDur: 0.001,
				},
			},
		})
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		query := r.FormValue("query")

		var result interface{}
		if query == "up" {
			result = QueryResult{
				ResultType: "vector",
				Result: mustMarshal([]VectorSample{
					{
						Metric: map[string]string{"job": "prometheus", "instance": "localhost:9090"},
						Value:  [2]interface{}{float64(time.Now().Unix()), "1"},
					},
					{
						Metric: map[string]string{"job": "web", "instance": "web-1:8080"},
						Value:  [2]interface{}{float64(time.Now().Unix()), "1"},
					},
				}),
			}
		} else if query == "scalar(1+1)" {
			result = QueryResult{
				ResultType: "scalar",
				Result:     mustMarshal([2]interface{}{float64(time.Now().Unix()), "2"}),
			}
		} else {
			result = QueryResult{
				ResultType: "vector",
				Result:     mustMarshal([]VectorSample{}),
			}
		}
		data, _ := json.Marshal(result)
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/query_range", func(w http.ResponseWriter, r *http.Request) {
		result := QueryResult{
			ResultType: "matrix",
			Result: mustMarshal([]MatrixSeries{
				{
					Metric: map[string]string{"job": "web"},
					Values: [][2]interface{}{
						{float64(time.Now().Add(-5 * time.Minute).Unix()), "100"},
						{float64(time.Now().Add(-4 * time.Minute).Unix()), "110"},
						{float64(time.Now().Add(-3 * time.Minute).Unix()), "105"},
					},
				},
			}),
		}
		data, _ := json.Marshal(result)
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/status/runtimeinfo", func(w http.ResponseWriter, r *http.Request) {
		data, _ := json.Marshal(RuntimeInfo{
			StartTime:           "2026-03-27T00:00:00Z",
			ReloadConfigSuccess: true,
			GoroutineCount:      42,
			GOMAXPROCS:          8,
			StorageRetention:    "15d",
		})
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/status/buildinfo", func(w http.ResponseWriter, r *http.Request) {
		data, _ := json.Marshal(BuildInfo{
			Version:   "2.53.0",
			Revision:  "abc123",
			Branch:    "HEAD",
			GoVersion: "go1.23.0",
		})
		resp := map[string]interface{}{"status": "success", "data": json.RawMessage(data)}
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Prometheus Server is Healthy.\n"))
	})

	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("Prometheus Server is Ready.\n"))
	})

	return httptest.NewServer(mux)
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return json.RawMessage(b)
}

// --- Client tests ---

func TestNewClient(t *testing.T) {
	cfg := PrometheusConfig{URL: "http://localhost:9090/"}
	c := NewClient(cfg)
	if c.baseURL != "http://localhost:9090" {
		t.Errorf("expected trailing slash stripped, got %s", c.baseURL)
	}
	if c.basicAuth != nil {
		t.Error("expected no basic auth")
	}
}

func TestNewClientWithBasicAuth(t *testing.T) {
	cfg := PrometheusConfig{URL: "http://localhost:9090"}
	cfg.BasicAuth.Username = "admin"
	cfg.BasicAuth.Password = "secret"
	c := NewClient(cfg)
	if c.basicAuth == nil {
		t.Fatal("expected basic auth to be set")
	}
	if c.basicAuth.Username != "admin" {
		t.Errorf("expected admin, got %s", c.basicAuth.Username)
	}
}

func TestNewClientWithHTTP(t *testing.T) {
	c := NewClientWithHTTP("http://prom:9090/", &http.Client{})
	if c.baseURL != "http://prom:9090" {
		t.Errorf("expected trailing slash stripped, got %s", c.baseURL)
	}
}

func TestRules(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	data, err := c.Rules(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(data.Groups))
	}
	if len(data.Groups[0].Rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(data.Groups[0].Rules))
	}
}

func TestRulesFilterAlert(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	data, err := c.Rules(context.Background(), "alert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, g := range data.Groups {
		for _, r := range g.Rules {
			if r.Type != "alerting" {
				t.Errorf("expected only alerting rules, got %s", r.Type)
			}
		}
	}
}

func TestRulesFilterRecord(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	data, err := c.Rules(context.Background(), "record")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, g := range data.Groups {
		for _, r := range g.Rules {
			if r.Type != "recording" {
				t.Errorf("expected only recording rules, got %s", r.Type)
			}
		}
	}
}

func TestAlerts(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	data, err := c.Alerts(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.Alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(data.Alerts))
	}
	if data.Alerts[0].State != "firing" {
		t.Errorf("expected firing state, got %s", data.Alerts[0].State)
	}
	if data.Alerts[1].State != "pending" {
		t.Errorf("expected pending state, got %s", data.Alerts[1].State)
	}
}

func TestTargets(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	data, err := c.Targets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data.ActiveTargets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(data.ActiveTargets))
	}
	healthy := 0
	for _, t := range data.ActiveTargets {
		if t.Health == "up" {
			healthy++
		}
	}
	if healthy != 2 {
		t.Errorf("expected 2 healthy targets, got %d", healthy)
	}
}

func TestQuery(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	result, err := c.Query(context.Background(), "up", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResultType != "vector" {
		t.Errorf("expected vector, got %s", result.ResultType)
	}
	var samples []VectorSample
	if err := json.Unmarshal(result.Result, &samples); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if len(samples) != 2 {
		t.Errorf("expected 2 samples, got %d", len(samples))
	}
}

func TestQueryWithTimestamp(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	ts := time.Now().Add(-1 * time.Hour)
	result, err := c.Query(context.Background(), "up", &ts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResultType != "vector" {
		t.Errorf("expected vector, got %s", result.ResultType)
	}
}

func TestQueryRange(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	end := time.Now()
	start := end.Add(-5 * time.Minute)
	result, err := c.QueryRange(context.Background(), "rate(http_requests_total[5m])", start, end, 15*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ResultType != "matrix" {
		t.Errorf("expected matrix, got %s", result.ResultType)
	}
}

func TestRuntimeInfo(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	info, err := c.RuntimeInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.GoroutineCount != 42 {
		t.Errorf("expected 42 goroutines, got %d", info.GoroutineCount)
	}
	if info.StorageRetention != "15d" {
		t.Errorf("expected 15d retention, got %s", info.StorageRetention)
	}
}

func TestBuildInfo(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	info, err := c.BuildInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Version != "2.53.0" {
		t.Errorf("expected 2.53.0, got %s", info.Version)
	}
}

func TestHealthy(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	healthy, err := c.Healthy(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy")
	}
}

func TestReady(t *testing.T) {
	srv := mockPromServer()
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	ready, err := c.Ready(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Error("expected ready")
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	_, err := c.Rules(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestAPIErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"status":    "error",
			"errorType": "bad_data",
			"error":     "invalid query",
		})
	}))
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	_, err := c.Query(context.Background(), "bad{", nil)
	if err == nil {
		t.Fatal("expected error for error status")
	}
}

func TestUnreachableServer(t *testing.T) {
	c := NewClientWithHTTP("http://127.0.0.1:1", &http.Client{Timeout: 100 * time.Millisecond})
	_, err := c.Rules(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestHealthyUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c := NewClientWithHTTP(srv.URL, srv.Client())
	healthy, err := c.Healthy(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if healthy {
		t.Error("expected unhealthy")
	}
}

func TestBasicAuthSent(t *testing.T) {
	var receivedUser, receivedPass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUser, receivedPass, _ = r.BasicAuth()
		data, _ := json.Marshal(RuntimeInfo{GoroutineCount: 1})
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   json.RawMessage(data),
		})
	}))
	defer srv.Close()

	cfg := PrometheusConfig{URL: srv.URL}
	cfg.BasicAuth.Username = "myuser"
	cfg.BasicAuth.Password = "mypass"
	c := NewClient(cfg)
	c.httpClient = srv.Client()
	_, err := c.RuntimeInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedUser != "myuser" || receivedPass != "mypass" {
		t.Errorf("expected myuser/mypass, got %s/%s", receivedUser, receivedPass)
	}
}

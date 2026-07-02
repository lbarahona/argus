package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(handler http.HandlerFunc) (*httptest.Server, *Client) {
	srv := httptest.NewServer(handler)
	client := NewClientWithHTTP(srv.URL, srv.Client())
	return srv, client
}

func jsonResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestNewClient(t *testing.T) {
	cfg := LokiConfig{
		URL:      "http://localhost:3100",
		TenantID: "test-tenant",
		BasicAuth: BasicAuth{
			Username: "user",
			Password: "pass",
		},
	}
	c := NewClient(cfg)
	if c.baseURL != "http://localhost:3100" {
		t.Errorf("expected baseURL http://localhost:3100, got %s", c.baseURL)
	}
	if c.tenantID != "test-tenant" {
		t.Errorf("expected tenantID test-tenant, got %s", c.tenantID)
	}
	if c.basicAuth == nil {
		t.Fatal("expected basicAuth to be set")
	}
}

func TestNewClient_TrailingSlash(t *testing.T) {
	c := NewClient(LokiConfig{URL: "http://localhost:3100/"})
	if c.baseURL != "http://localhost:3100" {
		t.Errorf("expected trailing slash stripped, got %s", c.baseURL)
	}
}

func TestNewClientWithHTTP(t *testing.T) {
	c := NewClientWithHTTP("http://example.com/", &http.Client{})
	if c.baseURL != "http://example.com" {
		t.Errorf("expected trailing slash stripped, got %s", c.baseURL)
	}
}

func TestHealthy_Success(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(200)
		w.Write([]byte("ready"))
	})
	defer srv.Close()

	healthy, latency, err := client.Healthy(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !healthy {
		t.Error("expected healthy=true")
	}
	if latency <= 0 {
		t.Error("expected positive latency")
	}
}

func TestHealthy_Unhealthy(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	})
	defer srv.Close()

	healthy, _, err := client.Healthy(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if healthy {
		t.Error("expected healthy=false")
	}
}

func TestBuildInfo(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/status/buildinfo" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		jsonResponse(w, BuildInfoResponse{
			Status: "success",
			Data: BuildInfo{
				Version:   "2.9.4",
				Revision:  "abc123",
				Branch:    "main",
				GoVersion: "go1.21.0",
			},
		})
	})
	defer srv.Close()

	info, err := client.BuildInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Version != "2.9.4" {
		t.Errorf("expected version 2.9.4, got %s", info.Version)
	}
	if info.GoVersion != "go1.21.0" {
		t.Errorf("expected GoVersion go1.21.0, got %s", info.GoVersion)
	}
}

func TestBuildInfo_Error(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	})
	defer srv.Close()

	_, err := client.BuildInfo(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQuery(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query") != `{app="test"}` {
			t.Errorf("unexpected query: %s", q.Get("query"))
		}
		if q.Get("limit") != "50" {
			t.Errorf("unexpected limit: %s", q.Get("limit"))
		}
		jsonResponse(w, QueryResult{
			Status: "success",
			Data: ResultData{
				ResultType: "streams",
				Streams: []Stream{
					{
						Labels: map[string]string{"app": "test"},
						Values: [][]string{
							{"1700000000000000000", "log line 1"},
							{"1700000001000000000", "log line 2"},
						},
					},
				},
			},
		})
	})
	defer srv.Close()

	result, err := client.Query(context.Background(), `{app="test"}`, 50, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success status, got %s", result.Status)
	}
	if len(result.Data.Streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(result.Data.Streams))
	}
	if len(result.Data.Streams[0].Values) != 2 {
		t.Errorf("expected 2 entries, got %d", len(result.Data.Streams[0].Values))
	}
}

func TestQuery_WithTime(t *testing.T) {
	at := time.Unix(1700000000, 0)
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("time") == "" {
			t.Error("expected time parameter")
		}
		jsonResponse(w, QueryResult{Status: "success", Data: ResultData{ResultType: "streams"}})
	})
	defer srv.Close()

	_, err := client.Query(context.Background(), `{app="test"}`, 0, at)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQuery_Error(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte("parse error"))
	})
	defer srv.Close()

	_, err := client.Query(context.Background(), "bad query", 0, time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestQueryRange(t *testing.T) {
	now := time.Now()
	start := now.Add(-1 * time.Hour)

	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query") == "" {
			t.Error("expected query parameter")
		}
		if q.Get("start") == "" {
			t.Error("expected start parameter")
		}
		if q.Get("end") == "" {
			t.Error("expected end parameter")
		}
		if q.Get("limit") != "200" {
			t.Errorf("expected limit 200, got %s", q.Get("limit"))
		}
		jsonResponse(w, QueryResult{
			Status: "success",
			Data: ResultData{
				ResultType: "streams",
				Streams: []Stream{
					{
						Labels: map[string]string{"job": "varlogs"},
						Values: [][]string{{"1700000000000000000", "test line"}},
					},
				},
			},
		})
	})
	defer srv.Close()

	result, err := client.QueryRange(context.Background(), `{job="varlogs"}`, start, now, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data.ResultType != "streams" {
		t.Errorf("expected streams, got %s", result.Data.ResultType)
	}
}

func TestLabels(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		jsonResponse(w, LabelNamesResponse{
			Status: "success",
			Data:   []string{"app", "namespace", "pod", "job"},
		})
	})
	defer srv.Close()

	labels, err := client.Labels(context.Background(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(labels) != 4 {
		t.Errorf("expected 4 labels, got %d", len(labels))
	}
}

func TestLabels_WithTimeRange(t *testing.T) {
	now := time.Now()
	start := now.Add(-1 * time.Hour)

	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("start") == "" {
			t.Error("expected start parameter")
		}
		if q.Get("end") == "" {
			t.Error("expected end parameter")
		}
		jsonResponse(w, LabelNamesResponse{Status: "success", Data: []string{"app"}})
	})
	defer srv.Close()

	_, err := client.Labels(context.Background(), start, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLabelValues(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/label/app/values" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		jsonResponse(w, LabelValuesResponse{
			Status: "success",
			Data:   []string{"nginx", "api", "worker"},
		})
	})
	defer srv.Close()

	values, err := client.LabelValues(context.Background(), "app", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 3 {
		t.Errorf("expected 3 values, got %d", len(values))
	}
}

func TestLabelValues_Error(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("server error"))
	})
	defer srv.Close()

	_, err := client.LabelValues(context.Background(), "bad", time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSeries(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/series" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		matchers := q["match[]"]
		if len(matchers) != 2 {
			t.Errorf("expected 2 matchers, got %d", len(matchers))
		}
		jsonResponse(w, SeriesResponse{
			Status: "success",
			Data: []map[string]string{
				{"app": "nginx", "namespace": "prod"},
				{"app": "api", "namespace": "prod"},
			},
		})
	})
	defer srv.Close()

	series, err := client.Series(context.Background(),
		[]string{`{app="nginx"}`, `{app="api"}`}, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 2 {
		t.Errorf("expected 2 series, got %d", len(series))
	}
}

func TestIndexStats(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loki/api/v1/index/stats" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		jsonResponse(w, StatsResponse{
			Streams: 150,
			Chunks:  3200,
			Entries: 500000,
			Bytes:   104857600,
		})
	})
	defer srv.Close()

	stats, err := client.IndexStats(context.Background(), "", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Streams != 150 {
		t.Errorf("expected 150 streams, got %d", stats.Streams)
	}
	if stats.Bytes != 104857600 {
		t.Errorf("expected 104857600 bytes, got %d", stats.Bytes)
	}
}

func TestIndexStats_WithQuery(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != `{app="nginx"}` {
			t.Errorf("expected query parameter, got %s", q.Get("query"))
		}
		jsonResponse(w, StatsResponse{Streams: 10})
	})
	defer srv.Close()

	stats, err := client.IndexStats(context.Background(), `{app="nginx"}`, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.Streams != 10 {
		t.Errorf("expected 10 streams, got %d", stats.Streams)
	}
}

func TestIndexStats_Error(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	})
	defer srv.Close()

	_, err := client.IndexStats(context.Background(), "", time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildSummary(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/loki/api/v1/status/buildinfo", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, BuildInfoResponse{
			Status: "success",
			Data:   BuildInfo{Version: "2.9.4"},
		})
	})
	mux.HandleFunc("/loki/api/v1/labels", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, LabelNamesResponse{
			Status: "success",
			Data:   []string{"app", "namespace", "pod"},
		})
	})
	mux.HandleFunc("/loki/api/v1/series", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, SeriesResponse{
			Status: "success",
			Data: []map[string]string{
				{"app": "nginx"},
				{"app": "api"},
			},
		})
	})
	mux.HandleFunc("/loki/api/v1/index/stats", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, StatsResponse{Streams: 100, Entries: 50000, Bytes: 1048576})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := NewClientWithHTTP(srv.URL, srv.Client())

	summary, err := client.BuildSummary(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !summary.Healthy {
		t.Error("expected healthy=true")
	}
	if summary.Version != "2.9.4" {
		t.Errorf("expected version 2.9.4, got %s", summary.Version)
	}
	if summary.Labels != 3 {
		t.Errorf("expected 3 labels, got %d", summary.Labels)
	}
	if summary.Stats == nil {
		t.Fatal("expected stats to be set")
	}
	if summary.Stats.Entries != 50000 {
		t.Errorf("expected 50000 entries, got %d", summary.Stats.Entries)
	}
}

func TestMatchAllSelector(t *testing.T) {
	if got := matchAllSelector([]string{"filename", "job", "app"}); got != `{job=~".+"}` {
		t.Errorf("prefer job label, got %q", got)
	}
	if got := matchAllSelector([]string{"custom_label"}); got != `{custom_label=~".+"}` {
		t.Errorf("fall back to first label, got %q", got)
	}
	if got := matchAllSelector(nil); got != "" {
		t.Errorf("no labels → empty selector, got %q", got)
	}
}

func TestBuildSummaryUsesRealLabelMatcher(t *testing.T) {
	var seriesMatch, statsQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels"):
			fmt.Fprint(w, `{"status":"success","data":["app","job"]}`)
		case strings.HasSuffix(r.URL.Path, "/series"):
			seriesMatch = r.URL.Query().Get("match[]")
			fmt.Fprint(w, `{"status":"success","data":[{"app":"nginx"}]}`)
		case strings.HasSuffix(r.URL.Path, "/index/stats"):
			statsQuery = r.URL.Query().Get("query")
			fmt.Fprint(w, `{"streams":1,"chunks":2,"entries":3,"bytes":4}`)
		case strings.HasSuffix(r.URL.Path, "/ready"):
			fmt.Fprint(w, "ready")
		default:
			fmt.Fprint(w, `{"status":"success","data":{}}`)
		}
	}))
	defer server.Close()

	c := NewClient(LokiConfig{URL: server.URL})
	s, err := c.BuildSummary(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seriesMatch != `{job=~".+"}` {
		t.Errorf("series matcher = %q, want {job=~\".+\"} (label-derived)", seriesMatch)
	}
	if statsQuery != `{job=~".+"}` {
		t.Errorf("stats query = %q, must not be empty (Loki 400s)", statsQuery)
	}
	if s.Series != 1 {
		t.Errorf("series count = %d, want 1", s.Series)
	}
}

func TestTenantIDHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgID := r.Header.Get("X-Scope-OrgID")
		if orgID != "my-tenant" {
			t.Errorf("expected X-Scope-OrgID=my-tenant, got %q", orgID)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := LokiConfig{URL: srv.URL, TenantID: "my-tenant"}
	client := NewClient(cfg)
	client.httpClient = srv.Client()

	client.Healthy(context.Background())
}

func TestBasicAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("expected basic auth")
		}
		if user != "admin" || pass != "secret" {
			t.Errorf("unexpected auth: %s:%s", user, pass)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cfg := LokiConfig{
		URL:       srv.URL,
		BasicAuth: BasicAuth{Username: "admin", Password: "secret"},
	}
	client := NewClient(cfg)
	client.httpClient = srv.Client()

	client.Healthy(context.Background())
}

func TestParseEntries(t *testing.T) {
	result := &QueryResult{
		Data: ResultData{
			ResultType: "streams",
			Streams: []Stream{
				{
					Labels: map[string]string{"app": "test"},
					Values: [][]string{
						{"1700000000000000000", "first line"},
						{"1700000001000000000", "second line"},
					},
				},
				{
					Labels: map[string]string{"app": "other"},
					Values: [][]string{
						{"1700000002000000000", "third line"},
					},
				},
			},
		},
	}

	entries := ParseEntries(result)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	if entries[0].Line != "first line" {
		t.Errorf("expected 'first line', got %q", entries[0].Line)
	}
	if entries[0].Labels["app"] != "test" {
		t.Errorf("expected label app=test, got %v", entries[0].Labels)
	}
	if entries[2].Line != "third line" {
		t.Errorf("expected 'third line', got %q", entries[2].Line)
	}
}

func TestParseEntries_Nil(t *testing.T) {
	entries := ParseEntries(nil)
	if entries != nil {
		t.Errorf("expected nil entries for nil result, got %v", entries)
	}
}

func TestParseEntries_InvalidTimestamp(t *testing.T) {
	result := &QueryResult{
		Data: ResultData{
			Streams: []Stream{
				{
					Labels: map[string]string{},
					Values: [][]string{
						{"not-a-number", "should be skipped"},
						{"1700000000000000000", "valid line"},
					},
				},
			},
		},
	}

	entries := ParseEntries(result)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (invalid timestamp skipped), got %d", len(entries))
	}
	if entries[0].Line != "valid line" {
		t.Errorf("expected 'valid line', got %q", entries[0].Line)
	}
}

func TestParseEntries_ShortValues(t *testing.T) {
	result := &QueryResult{
		Data: ResultData{
			Streams: []Stream{
				{
					Labels: map[string]string{},
					Values: [][]string{
						{"only-one"},
						{"1700000000000000000", "valid"},
					},
				},
			},
		},
	}

	entries := ParseEntries(result)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (short value skipped), got %d", len(entries))
	}
}

func TestLokiConfig_IsConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  LokiConfig
		want bool
	}{
		{"empty", LokiConfig{}, false},
		{"with URL", LokiConfig{URL: "http://localhost:3100"}, true},
		{"only auth", LokiConfig{BasicAuth: BasicAuth{Username: "u"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}

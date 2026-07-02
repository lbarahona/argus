package loki

import (
	"encoding/json"
	"testing"
)

func TestQueryResult_JSON(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{
				"stream": {"app": "nginx"},
				"values": [["1700000000000000000", "test line"]]
			}]
		}
	}`

	var result QueryResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.Data.ResultType != "streams" {
		t.Errorf("expected streams, got %s", result.Data.ResultType)
	}
	if len(result.Data.Streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(result.Data.Streams))
	}
	stream := result.Data.Streams[0]
	if stream.Labels["app"] != "nginx" {
		t.Errorf("expected app=nginx label")
	}
	if len(stream.Values) != 1 || stream.Values[0][1] != "test line" {
		t.Errorf("unexpected values: %v", stream.Values)
	}
}

func TestBuildInfoResponse_JSON(t *testing.T) {
	raw := `{
		"status": "success",
		"data": {
			"version": "2.9.4",
			"revision": "abc123",
			"branch": "main",
			"buildDate": "2024-01-01",
			"buildUser": "ci",
			"goVersion": "go1.21.0"
		}
	}`

	var resp BuildInfoResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Data.Version != "2.9.4" {
		t.Errorf("expected version 2.9.4, got %s", resp.Data.Version)
	}
	if resp.Data.GoVersion != "go1.21.0" {
		t.Errorf("expected go1.21.0, got %s", resp.Data.GoVersion)
	}
}

func TestStatsResponse_JSON(t *testing.T) {
	raw := `{"streams": 100, "chunks": 5000, "entries": 1000000, "bytes": 1073741824}`
	var stats StatsResponse
	if err := json.Unmarshal([]byte(raw), &stats); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if stats.Streams != 100 {
		t.Errorf("expected 100 streams, got %d", stats.Streams)
	}
	if stats.Bytes != 1073741824 {
		t.Errorf("expected 1073741824 bytes, got %d", stats.Bytes)
	}
}

func TestLabelNamesResponse_JSON(t *testing.T) {
	raw := `{"status": "success", "data": ["app", "namespace", "pod"]}`
	var resp LabelNamesResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Errorf("expected 3 labels, got %d", len(resp.Data))
	}
}

func TestSeriesResponse_JSON(t *testing.T) {
	raw := `{"status": "success", "data": [{"app": "nginx", "pod": "nginx-abc"}]}`
	var resp SeriesResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 series, got %d", len(resp.Data))
	}
	if resp.Data[0]["app"] != "nginx" {
		t.Errorf("expected app=nginx")
	}
}

func TestResultDataDecodesMatrix(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"app":"nginx"},"values":[[1700000000,"42"],[1700000060,"43.5"]]}
	]}}`
	var qr QueryResult
	if err := json.Unmarshal([]byte(payload), &qr); err != nil {
		t.Fatalf("matrix result must decode: %v", err)
	}
	if qr.Data.ResultType != "matrix" || len(qr.Data.Series) != 1 {
		t.Fatalf("unexpected decode: %+v", qr.Data)
	}
	s := qr.Data.Series[0]
	if s.Metric["app"] != "nginx" || len(s.Values) != 2 || s.Values[0].Value != 42 {
		t.Errorf("series decoded wrong: %+v", s)
	}
	if s.Values[0].Timestamp.Unix() != 1700000000 {
		t.Errorf("timestamp = %v, want 1700000000s", s.Values[0].Timestamp.Unix())
	}
}

func TestResultDataDecodesVector(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"app":"nginx"},"value":[1700000000,"7"]}
	]}}`
	var qr QueryResult
	if err := json.Unmarshal([]byte(payload), &qr); err != nil {
		t.Fatalf("vector result must decode: %v", err)
	}
	if len(qr.Data.Series) != 1 || len(qr.Data.Series[0].Values) != 1 || qr.Data.Series[0].Values[0].Value != 7 {
		t.Errorf("vector decoded wrong: %+v", qr.Data)
	}
}

func TestResultDataStreamsStillDecode(t *testing.T) {
	payload := `{"status":"success","data":{"resultType":"streams","result":[
		{"stream":{"app":"nginx"},"values":[["1700000000000000000","hello"]]}
	]}}`
	var qr QueryResult
	if err := json.Unmarshal([]byte(payload), &qr); err != nil {
		t.Fatalf("streams must still decode: %v", err)
	}
	if len(qr.Data.Streams) != 1 || qr.Data.Streams[0].Values[0][1] != "hello" {
		t.Errorf("streams decoded wrong: %+v", qr.Data)
	}
}

func TestLokiConfig_Roundtrip(t *testing.T) {
	cfg := LokiConfig{
		URL:       "http://loki:3100",
		TenantID:  "team-a",
		BasicAuth: BasicAuth{Username: "user", Password: "pass"},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded LokiConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.URL != cfg.URL {
		t.Errorf("URL mismatch: %s vs %s", decoded.URL, cfg.URL)
	}
	if decoded.TenantID != cfg.TenantID {
		t.Errorf("TenantID mismatch: %s vs %s", decoded.TenantID, cfg.TenantID)
	}
	if decoded.BasicAuth.Username != cfg.BasicAuth.Username {
		t.Errorf("Username mismatch")
	}
}

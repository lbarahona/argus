# Tier 2 Dead-Feature Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make six advertised-but-broken features actually work — metrics parsing against real Signoz v3, deps cross-service edge discovery, watch P99 alerts, scorecard trends, the Bedrock AI provider, and runbook execution — plus the calibration/durability follow-ups from Tier 1's final review (SLO burn-rate alerting, atomic config writes, fsync, budget window cap, MCP test pinning).

**Architecture:** Signoz parser fixes stay in `internal/signoz`. Runbook execution becomes a new `Executor` in `internal/runbook` with an injectable `CommandRunner` so tests never shell out; the CLI gains a real `--execute` flag defaulting to the safe dry-run. Bedrock switches from the unparseable eventstream endpoint to the plain-JSON `invoke` endpoint. Watch/scorecard derive latency and trends from data the tool can actually fetch (one unfiltered trace query; bucketed error logs) instead of fields that are always zero.

**Tech Stack:** Go 1.24, stdlib only (`os/exec` for the runbook runner; no new dependencies).

## Global Constraints

- `types.Service.ErrorRate` is a percentage (0–100). `types.TraceEntry.DurationMs()` returns milliseconds.
- The real `signoz.Client.QueryLogs`/`QueryTraces` honor `limit` and return the newest N; `QueryTraces(ctx, "", dur, limit)` means **no service filter**. Mocks must mirror this.
- Do not change the `signoz.SignozQuerier` interface — 10+ packages implement it in mocks.
- `internal/fsutil.WriteFileAtomic(path string, data []byte, perm os.FileMode) error` exists (Tier 1) — use it for every new persistent write.
- Runbook safety: commands NEVER execute unless the user passed `--execute` AND confirmed the step interactively. Dry-run stays the default.
- Run `gofmt` on touched files (repo-wide pre-existing drift is out of scope). Commit after every task.
- Work on branch `feat/tier2-dead-features`, stacked on `fix/tier1-correctness` (created in Task 0).

---

### Task 0: Branch setup

**Files:** none

- [ ] **Step 1: Create the working branch from the Tier 1 branch**

```bash
cd /Users/lbarahona/Projects/argus
git checkout fix/tier1-correctness
git checkout -b feat/tier2-dead-features
```

- [ ] **Step 2: Verify clean baseline**

Run: `go test ./... > /dev/null && echo BASELINE-OK`
Expected: `BASELINE-OK`

---

### Task 1: Signoz — parse real v3 metrics series (object-shaped values)

`parseMetricsResponse` expects `values: [[ts, float]]` tuples, but Signoz v3 returns `values: [{"timestamp": 1708070400000, "value": "42.5"}]` (string values). Real `argus metrics` is always empty.

**Files:**
- Modify: `internal/signoz/client.go` (`parseMetricsResponse`, add `parseMetricPoint`, add `strconv` import)
- Test: `internal/signoz/client_test.go`

**Interfaces:**
- Produces: unexported `parseMetricPoint(raw json.RawMessage) (types.MetricEntry, bool)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/signoz/client_test.go`:

```go
func TestQueryMetricsRealV3ObjectValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","series":[{"labels":{"host":"web-1"},"values":[{"timestamp":1708070400000,"value":"42.5"},{"timestamp":1708070460000,"value":"43.1"}]}]}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryMetrics(context.Background(), "cpu_usage", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Metrics) != 2 {
		t.Fatalf("expected 2 metric points from v3 object-shaped values, got %d", len(result.Metrics))
	}
	if result.Metrics[0].Value != 42.5 {
		t.Errorf("point 0 value = %v, want 42.5", result.Metrics[0].Value)
	}
	if result.Metrics[0].Labels["host"] != "web-1" {
		t.Errorf("labels not preserved: %v", result.Metrics[0].Labels)
	}
	if result.Metrics[0].Timestamp.UnixMilli() != 1708070400000 {
		t.Errorf("timestamp = %v, want 1708070400000ms", result.Metrics[0].Timestamp.UnixMilli())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/signoz/ -run TestQueryMetricsRealV3ObjectValues -v`
Expected: FAIL — `expected 2 metric points ... got 0`

- [ ] **Step 3: Implement**

In `internal/signoz/client.go`, add `"strconv"` to imports. Replace the body of `parseMetricsResponse` (keeping its signature) with:

```go
func parseMetricsResponse(data []byte) ([]types.MetricEntry, error) {
	resultBytes, err := extractResultArray(data)
	if err != nil {
		return nil, fmt.Errorf("parsing metrics response: %w", err)
	}
	if resultBytes == nil {
		return nil, nil
	}

	var items []queryRangeResultItem
	if err := json.Unmarshal(resultBytes, &items); err == nil && isResultItemShape(items) {
		var metrics []types.MetricEntry
		for _, item := range items {
			for _, series := range item.Series {
				var s struct {
					Labels map[string]string `json:"labels"`
					Values []json.RawMessage `json:"values"`
				}
				if err := json.Unmarshal(series, &s); err != nil {
					continue
				}
				for _, raw := range s.Values {
					if entry, ok := parseMetricPoint(raw); ok {
						entry.Labels = s.Labels
						metrics = append(metrics, entry)
					}
				}
			}
		}
		return metrics, nil
	}

	return nil, nil
}

// parseMetricPoint decodes one series point. Signoz v3 marshals points as
// {"timestamp": <epoch-ms>, "value": "<float-as-string>"}; Prometheus-style
// [ts, value] tuples are kept as a fallback for older/proxied responses.
func parseMetricPoint(raw json.RawMessage) (types.MetricEntry, bool) {
	var obj struct {
		Timestamp int64  `json:"timestamp"`
		Value     string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Timestamp != 0 {
		if v, err := strconv.ParseFloat(obj.Value, 64); err == nil {
			return types.MetricEntry{Timestamp: time.UnixMilli(obj.Timestamp), Value: v}, true
		}
	}

	var tuple []interface{}
	if err := json.Unmarshal(raw, &tuple); err == nil && len(tuple) >= 2 {
		ts, _ := tuple[0].(float64)
		switch v := tuple[1].(type) {
		case float64:
			return types.MetricEntry{Timestamp: time.UnixMilli(int64(ts)), Value: v}, true
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return types.MetricEntry{Timestamp: time.UnixMilli(int64(ts)), Value: f}, true
			}
		}
	}
	return types.MetricEntry{}, false
}
```

- [ ] **Step 4: Run signoz tests**

Run: `go test ./internal/signoz/ -v`
Expected: PASS — the pre-existing `TestQueryMetrics` (tuple/float fixture) passes via the fallback branch; the new v3 test passes via the object branch.

- [ ] **Step 5: Commit**

```bash
git add internal/signoz/
git commit -m "fix(signoz): parse real v3 metrics series with object-shaped string values"
```

---

### Task 2: Signoz — normalize numeric trace status codes

The default trace select column declares `statusCode` as `int64` (client.go `defaultSelectColumns`), but `mapToTraceEntry` only accepts strings — numeric codes are dropped and `TraceEntry.StatusCode` is always `""`. Deps edge error rates and correlate error flags depend on it.

**Files:**
- Modify: `internal/signoz/client.go` (`mapToTraceEntry`, add `statusCodeString`)
- Test: `internal/signoz/client_test.go`

**Interfaces:**
- Produces: unexported `statusCodeString(code int64) string` mapping OTel codes: 2→`STATUS_CODE_ERROR`, 1→`STATUS_CODE_OK`, else→`STATUS_CODE_UNSET`.

- [ ] **Step 1: Write the failing test**

Add to `internal/signoz/client_test.go`:

```go
func TestMapToTraceEntryNumericStatusCode(t *testing.T) {
	entry := mapToTraceEntry(map[string]interface{}{
		"traceID":     "abc",
		"serviceName": "api",
		"statusCode":  float64(2), // numeric, as declared int64 in defaultSelectColumns
	})
	if entry.StatusCode != "STATUS_CODE_ERROR" {
		t.Errorf("numeric statusCode 2 should map to STATUS_CODE_ERROR, got %q", entry.StatusCode)
	}

	entry = mapToTraceEntry(map[string]interface{}{"statusCode": float64(1)})
	if entry.StatusCode != "STATUS_CODE_OK" {
		t.Errorf("numeric statusCode 1 should map to STATUS_CODE_OK, got %q", entry.StatusCode)
	}

	entry = mapToTraceEntry(map[string]interface{}{"statusCode": "STATUS_CODE_ERROR"})
	if entry.StatusCode != "STATUS_CODE_ERROR" {
		t.Errorf("string statusCode must pass through unchanged, got %q", entry.StatusCode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/signoz/ -run TestMapToTraceEntryNumericStatusCode -v`
Expected: FAIL — numeric cases yield `""`.

- [ ] **Step 3: Implement**

In `mapToTraceEntry`, replace:

```go
	if v, ok := m["statusCode"].(string); ok {
		entry.StatusCode = v
	} else if v, ok := m["status_code"].(string); ok {
		entry.StatusCode = v
	}
```

with:

```go
	if v, ok := m["statusCode"].(string); ok {
		entry.StatusCode = v
	} else if v, ok := m["status_code"].(string); ok {
		entry.StatusCode = v
	} else if v, ok := m["statusCode"].(float64); ok {
		entry.StatusCode = statusCodeString(int64(v))
	} else if v, ok := m["status_code"].(float64); ok {
		entry.StatusCode = statusCodeString(int64(v))
	}
```

and add:

```go
// statusCodeString maps OTel numeric span status codes to their names, so
// consumers can match on one canonical string form.
func statusCodeString(code int64) string {
	switch code {
	case 2:
		return "STATUS_CODE_ERROR"
	case 1:
		return "STATUS_CODE_OK"
	default:
		return "STATUS_CODE_UNSET"
	}
}
```

- [ ] **Step 4: Run signoz tests, then full suite**

Run: `go test ./internal/signoz/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/signoz/
git commit -m "fix(signoz): map numeric OTel trace status codes to canonical strings"
```

---

### Task 3: deps — cross-service edges via one unfiltered trace query

`Generate` queries traces per service; the real client filters by `serviceName`, so a parent span from another service can never appear in the same result set — `argus deps` always finds zero edges. Fix: one unfiltered query, join across the whole set.

**Files:**
- Modify: `internal/deps/deps.go` (the per-service query loop, ~lines 84-130)
- Modify: `internal/deps/deps_test.go` (fixtures that return multi-service spans for service-filtered queries — a shape the real client cannot produce)

- [ ] **Step 1: Write the failing test**

Add to `internal/deps/deps_test.go` (mirror the file's existing mock type; the key property: the mock must behave like the real client — return spans for ALL services only when `service == ""`, and only single-service spans otherwise):

```go
func TestGenerateFindsEdgesWithRealClientSemantics(t *testing.T) {
	allSpans := []types.TraceEntry{
		{TraceID: "t1", SpanID: "s1", ServiceName: "api-gateway", OperationName: "GET /users", DurationNano: 50_000_000},
		{TraceID: "t1", SpanID: "s2", ParentSpanID: "s1", ServiceName: "user-service", OperationName: "getUser", DurationNano: 30_000_000, StatusCode: "STATUS_CODE_ERROR"},
	}
	mock := newMockQuerier() // adapt to this file's existing mock constructor/fields
	mock.services = []types.Service{{Name: "api-gateway", NumCalls: 100}, {Name: "user-service", NumCalls: 80}}
	mock.queryTracesFunc = func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
		if service != "" {
			// Real client filters serviceName = service: only same-service spans.
			var filtered []types.TraceEntry
			for _, s := range allSpans {
				if s.ServiceName == service {
					filtered = append(filtered, s)
				}
			}
			return &types.QueryResult{Traces: filtered}, nil
		}
		return &types.QueryResult{Traces: allSpans}, nil
	}

	dm, err := Generate(context.Background(), Options{Querier: mock, Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dm.Edges) != 1 {
		t.Fatalf("expected 1 cross-service edge under real client semantics, got %d", len(dm.Edges))
	}
	e := dm.Edges[0]
	if e.From != "api-gateway" || e.To != "user-service" {
		t.Errorf("edge = %s->%s, want api-gateway->user-service", e.From, e.To)
	}
	if e.Errors != 1 {
		t.Errorf("edge errors = %d, want 1 (STATUS_CODE_ERROR span)", e.Errors)
	}
}
```

Adapt mock construction/field names to the existing mock in this file — the behavioral contract above is what matters. If `Options`/`Generate`/`Edge`/`DependencyMap` field names differ, mirror the existing tests' usage.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deps/ -run TestGenerateFindsEdgesWithRealClientSemantics -v`
Expected: FAIL — 0 edges (per-service queries can never join cross-service spans).

- [ ] **Step 3: Implement**

In `internal/deps/deps.go`, replace the per-service loop (`for _, svc := range services { result, err := opts.Querier.QueryTraces(ctx, svc.Name, opts.Duration, 500) ... }`) with a single unfiltered query feeding the same edge-discovery logic:

```go
	// One unfiltered query: a cross-service parent/child pair only joins when
	// both spans are in the same result set, which a per-service filter makes
	// impossible (the real client filters serviceName = <service>).
	result, err := opts.Querier.QueryTraces(ctx, "", opts.Duration, 2000)
	if err != nil {
		return nil, fmt.Errorf("querying traces: %w", err)
	}

	// Build span index: spanID -> trace entry
	spanIndex := make(map[string]*types.TraceEntry)
	for i := range result.Traces {
		t := &result.Traces[i]
		spanIndex[t.SpanID] = t
	}

	// Find cross-service edges by matching parent spans
	for i := range result.Traces {
		span := &result.Traces[i]
		if span.ParentSpanID == "" {
			continue
		}
		parent, ok := spanIndex[span.ParentSpanID]
		if !ok {
			continue
		}
		if parent.ServiceName == span.ServiceName {
			continue // same service, skip
		}

		key := parent.ServiceName + "->" + span.ServiceName
		edge, exists := edgeMap[key]
		if !exists {
			edge = &Edge{
				From: parent.ServiceName,
				To:   span.ServiceName,
			}
			edgeMap[key] = edge
		}
		edge.Calls++
		latMs := span.DurationMs()
		edge.AvgLatency += latMs
		if latMs > edge.P99Latency {
			edge.P99Latency = latMs
		}
		if span.StatusCode == "STATUS_CODE_ERROR" || span.StatusCode == "ERROR" {
			edge.Errors++
		}
	}
```

The node list still comes from `ListServices` and the existing `opts.Service` filtering (`filterForService`) is downstream of edge discovery — leave both as they are.

- [ ] **Step 4: Fix fixtures that encode impossible client behavior**

Run: `go test ./internal/deps/ -v`. Any pre-existing test whose mock returned multi-service spans for a service-filtered `QueryTraces("api-gateway", ...)` call now sees an unfiltered `QueryTraces("", ...)` call instead — update those mocks to serve the full span set for `service == ""` (most fixtures only need their expectation of the `service` argument changed). Expectations about resulting edges should not need to change.

- [ ] **Step 5: Run deps tests and full suite**

Run: `go test ./internal/deps/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/deps/
git commit -m "fix(deps): discover cross-service edges via one unfiltered trace query"
```

---

### Task 4: watch — real P99 latency from traces

`buildSnapshots` builds from `ListServices`, which has no latency field — `ServiceSnapshot.P99` is always 0 and the advertised 2000ms/5000ms thresholds never fire.

**Files:**
- Modify: `internal/watch/watch.go` (`tick`, add `enrichWithLatency` + `percentile` + const)
- Test: `internal/watch/watch_test.go`

**Interfaces:**
- Produces: unexported `(w *Watcher) enrichWithLatency(ctx, snapshots []ServiceSnapshot)` mutating `snapshots[i].P99`; unexported `percentile(vals []float64, p float64) float64`; `const latencyWindowMinutes = 15`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/watch/watch_test.go` (mirror the file's existing Watcher construction — it has a `client SignozQuerier` field; use the existing mock type in this file, extending it with a `queryTracesFunc` if it lacks one):

```go
func TestEnrichWithLatencySetsP99FromTraces(t *testing.T) {
	traces := make([]types.TraceEntry, 100)
	for i := range traces {
		traces[i] = types.TraceEntry{ServiceName: "api", DurationNano: int64(i+1) * 1_000_000} // 1..100ms
	}
	mock := newWatchMock() // adapt to this file's mock
	mock.queryTracesFunc = func(ctx context.Context, service string, durationMinutes, limit int) (*types.QueryResult, error) {
		if service != "" {
			t.Errorf("enrichWithLatency must query unfiltered, got service %q", service)
		}
		return &types.QueryResult{Traces: traces}, nil
	}
	w := newTestWatcher(mock) // adapt to this file's constructor pattern

	snapshots := []ServiceSnapshot{{Name: "api", Calls: 100}}
	w.enrichWithLatency(context.Background(), snapshots)

	if snapshots[0].P99 < 98 || snapshots[0].P99 > 100 {
		t.Errorf("P99 of 1..100ms = %v, want ~99", snapshots[0].P99)
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	if got := percentile(vals, 0.99); got != 100 {
		t.Errorf("p99 of 10..100 = %v, want 100", got)
	}
	if got := percentile(vals, 0.5); got != 50 {
		t.Errorf("p50 of 10..100 = %v, want 50", got)
	}
	if got := percentile([]float64{42}, 0.99); got != 42 {
		t.Errorf("p99 of single value = %v, want 42", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/watch/ -run 'EnrichWithLatency|TestPercentile' -v`
Expected: FAIL — `undefined: percentile` / `enrichWithLatency` (build error).

- [ ] **Step 3: Implement**

Add to `internal/watch/watch.go` (imports: `math` if not present):

```go
// latencyWindowMinutes is the lookback for per-tick latency sampling; kept
// short so P99 reflects current behavior, not the whole 6h service window.
const latencyWindowMinutes = 15

// enrichWithLatency fills per-service P99 from one unfiltered trace query.
// ListServices carries no latency data, so without this every P99 threshold
// is dead. Errors are ignored: latency alerts degrade, error alerts keep
// working.
func (w *Watcher) enrichWithLatency(ctx context.Context, snapshots []ServiceSnapshot) {
	result, err := w.client.QueryTraces(ctx, "", latencyWindowMinutes, 1000)
	if err != nil || result == nil {
		return
	}
	byService := make(map[string][]float64)
	for _, t := range result.Traces {
		if t.ServiceName != "" {
			byService[t.ServiceName] = append(byService[t.ServiceName], t.DurationMs())
		}
	}
	for i := range snapshots {
		if ds := byService[snapshots[i].Name]; len(ds) > 0 {
			snapshots[i].P99 = percentile(ds, 0.99)
		}
	}
}

// percentile returns the p-th percentile (0 < p <= 1) using the
// nearest-rank method. vals is sorted in place.
func percentile(vals []float64, p float64) float64 {
	sort.Float64s(vals)
	idx := int(math.Ceil(p*float64(len(vals)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx]
}
```

In `tick`, insert the enrichment between snapshot building and analysis:

```go
	snapshots := w.buildSnapshots(services)
	w.enrichWithLatency(ctx, snapshots)
	alerts := w.analyze(snapshots)
```

- [ ] **Step 4: Run watch tests and full suite**

Run: `go test ./internal/watch/ -v && go test ./...`
Expected: PASS — pre-existing tests that hand-build `ServiceSnapshot{P99: 6000}` still pass (they test `analyze`, which is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/watch/
git commit -m "fix(watch): compute real per-service P99 from traces so latency alerts can fire"
```

---

### Task 5: scorecard — honest trends from bucketed error logs

The "previous period" is a second identical `ListServices` call seconds later, so `ErrorTrend` is always stable. Replace with a first-half vs second-half comparison of the error logs the scorecard already fetches, and mark the trend unknown when the fetch hit its limit (newest-N truncation makes the older half unreliable).

**Files:**
- Modify: `internal/scorecard/scorecard.go` (remove the `prevServices` block ~lines 128-132; change `scoreService` signature; add `errorTrendFromLogs`)
- Test: `internal/scorecard/scorecard_test.go`

**Interfaces:**
- Changes: `scoreService(svc types.Service, logs []types.LogEntry, traces []types.TraceEntry, windowMinutes int) ServiceScore` (was `..., prev map[string]types.Service`). Both are unexported; the only caller is `Generate` in the same file plus tests.
- Produces: unexported `errorTrendFromLogs(logs []types.LogEntry, windowMinutes, fetchLimit int) Trend`. The existing consts `TrendStable/TrendBetter/TrendWorse/TrendNoData` are reused. `scorecardLogFetchLimit = 100` const documents the existing magic number.

- [ ] **Step 1: Write the failing tests**

Add to `internal/scorecard/scorecard_test.go`:

```go
func TestErrorTrendFromLogs(t *testing.T) {
	now := time.Now()
	mkLogs := func(olderCount, newerCount int) []types.LogEntry {
		var logs []types.LogEntry
		for i := 0; i < olderCount; i++ {
			logs = append(logs, types.LogEntry{Timestamp: now.Add(-50 * time.Minute)})
		}
		for i := 0; i < newerCount; i++ {
			logs = append(logs, types.LogEntry{Timestamp: now.Add(-5 * time.Minute)})
		}
		return logs
	}

	if got := errorTrendFromLogs(mkLogs(10, 30), 60, 100); got != TrendWorse {
		t.Errorf("10 old vs 30 new errors = %v, want TrendWorse", got)
	}
	if got := errorTrendFromLogs(mkLogs(30, 10), 60, 100); got != TrendBetter {
		t.Errorf("30 old vs 10 new errors = %v, want TrendBetter", got)
	}
	if got := errorTrendFromLogs(mkLogs(20, 21), 60, 100); got != TrendStable {
		t.Errorf("20 vs 21 errors (within deadband) = %v, want TrendStable", got)
	}
	if got := errorTrendFromLogs(nil, 60, 100); got != TrendNoData {
		t.Errorf("no logs = %v, want TrendNoData", got)
	}
	if got := errorTrendFromLogs(mkLogs(0, 100), 60, 100); got != TrendNoData {
		t.Errorf("fetch at limit (truncated window) = %v, want TrendNoData", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scorecard/ -run TestErrorTrendFromLogs -v`
Expected: FAIL — `undefined: errorTrendFromLogs`.

- [ ] **Step 3: Implement**

In `internal/scorecard/scorecard.go`:

1. Add near the top:

```go
// scorecardLogFetchLimit is the per-service error-log fetch size used both
// for pattern detection and trend bucketing.
const scorecardLogFetchLimit = 100
```

2. Use the const at the existing fetch site: `client.QueryLogs(ctx, svc.Name, opts.Duration, scorecardLogFetchLimit, "ERROR")`.

3. Delete the dead "previous period" block:

```go
	// Get previous period data for trends
	prevServices, _ := client.ListServices(ctx)
	prevMap := make(map[string]types.Service)
	for _, s := range prevServices {
		prevMap[s.Name] = s
	}
```

4. Change the call site to `score := scoreService(svc, errorLogs[svc.Name], traceData[svc.Name], opts.Duration)` and the signature to `func scoreService(svc types.Service, logs []types.LogEntry, traces []types.TraceEntry, windowMinutes int) ServiceScore`. Replace the old prev-based trend block (the `if prevSvc, ok := prev[svc.Name]; ...` lines ~183-193) with:

```go
	ss.ErrorTrend = errorTrendFromLogs(logs, windowMinutes, scorecardLogFetchLimit)
```

5. Add the helper:

```go
// errorTrendFromLogs compares error counts in the older vs newer half of the
// window. The fetch returns the newest N logs; if it hit its limit the older
// half is unreliable (truncation bias), so the trend is unknown rather than
// fabricated.
func errorTrendFromLogs(logs []types.LogEntry, windowMinutes, fetchLimit int) Trend {
	if len(logs) == 0 || len(logs) >= fetchLimit {
		return TrendNoData
	}
	cutoff := time.Now().Add(-time.Duration(windowMinutes/2) * time.Minute)
	var older, newer float64
	for _, l := range logs {
		if l.Timestamp.Before(cutoff) {
			older++
		} else {
			newer++
		}
	}
	switch {
	case newer > older*1.2:
		return TrendWorse
	case newer < older*0.8:
		return TrendBetter
	default:
		return TrendStable
	}
}
```

Check the `TrendNoData` handling in `computeScore` and the renderers: `TrendNoData` must not receive the ±5/10 trend bonus/penalty (the existing `computeScore` only branches on `TrendWorse`/`TrendBetter`, which is already correct) and `trendSymbol` must render it sensibly — if it lacks a case for `TrendNoData`, add one rendering `"·"` or `"n/a"`.

- [ ] **Step 4: Fix any tests that called scoreService with the old signature**

Run: `go test ./internal/scorecard/ -v` — update direct `scoreService(..., prevMap)` callers in tests to the new signature. A fixture that asserted a trend produced by the prev-map comparison should be rewritten against `errorTrendFromLogs` semantics (timestamped logs), not deleted.

- [ ] **Step 5: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scorecard/
git commit -m "fix(scorecard): derive error trends from bucketed logs; prev-period call was identical data"
```

---

### Task 6: Bedrock — correct anthropic_version and parseable responses

Two fatal bugs: the request body sends `anthropic_version: "2023-06-01"` (the direct-API header value; Bedrock requires body value `bedrock-2023-05-31` → every real call is a ValidationException), and the streaming endpoint returns binary eventstream framing the parser can't read. Fix: correct version constant; switch to the non-streaming `/model/{id}/invoke` endpoint whose response is plain Anthropic-messages JSON.

**Files:**
- Modify: `internal/ai/bedrock.go` (version const, endpoint, response parsing; delete `streamBedrockResponse`)
- Modify: `internal/ai/bedrock_test.go` (asserts the wrong version and SSE format)

- [ ] **Step 1: Write the failing tests**

In `internal/ai/bedrock_test.go`, add (and later update the existing wrong assertions):

```go
func TestBedrockUsesBedrockAnthropicVersionAndInvokeEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"analysis result"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	p := NewBedrockProvider(server.URL, "test-token", "anthropic.claude-3-5-sonnet-20241022-v2:0")
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "why is api failing?", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody["anthropic_version"] != "bedrock-2023-05-31" {
		t.Errorf("anthropic_version = %v, want bedrock-2023-05-31 (Bedrock rejects the direct-API value)", gotBody["anthropic_version"])
	}
	if gotPath != "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/invoke" {
		t.Errorf("path = %q, want the non-streaming invoke endpoint", gotPath)
	}
	if !strings.Contains(buf.String(), "analysis result") {
		t.Errorf("response text not written to writer, got %q", buf.String())
	}
}

func TestBedrockEmptyContentIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	p := NewBedrockProvider(server.URL, "tok", "m")
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "q", &buf); err == nil {
		t.Error("empty content must return an error, not silent empty output")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ai/ -run 'TestBedrock' -v`
Expected: new tests FAIL (wrong version, wrong path, empty output not an error).

- [ ] **Step 3: Implement**

In `internal/ai/bedrock.go`:

1. Add the constant and use it (the old code reused `anthropicVersion` from anthropic.go — a header value, wrong in a Bedrock body):

```go
// bedrockAnthropicVersion is the body-level version Bedrock requires for
// Anthropic models — distinct from the direct API's anthropic-version header.
const bedrockAnthropicVersion = "bedrock-2023-05-31"
```

2. Rewrite `AnalyzeWithSystem`:

```go
func (p *BedrockProvider) AnalyzeWithSystem(ctx context.Context, system string, messages []Message, w io.Writer) error {
	// Bedrock with Anthropic models uses the Anthropic messages format
	reqBody := bedrockRequest{
		AnthropicVersion: bedrockAnthropicVersion,
		MaxTokens:        4096,
		System:           system,
		Messages:         messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	// The invoke-with-response-stream endpoint returns binary AWS
	// eventstream framing; the non-streaming invoke endpoint returns plain
	// Anthropic-messages JSON we can parse without an AWS SDK.
	url := fmt.Sprintf("%s/model/%s/invoke", p.endpoint, p.model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling Bedrock API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bedrock API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decoding Bedrock response: %w", err)
	}

	wrote := false
	for _, c := range out.Content {
		if c.Type == "text" && c.Text != "" {
			fmt.Fprint(w, c.Text)
			wrote = true
		}
	}
	if !wrote {
		return fmt.Errorf("Bedrock response contained no text content")
	}
	fmt.Fprintln(w)
	if out.StopReason == "max_tokens" {
		fmt.Fprintln(w, "[response truncated at max_tokens]")
	}
	return nil
}
```

3. Delete `streamBedrockResponse` entirely and drop the now-unused `bufio`/`strings` imports if nothing else uses them (`strings` is still used by `TrimRight` in the constructor — keep it).

- [ ] **Step 4: Update stale assertions**

In `bedrock_test.go`, the pre-existing test asserting `anthropic_version == "2023-06-01"` and any SSE-format response fixtures must be updated to the new contract (the old assertions cemented the bug). Run: `go test ./internal/ai/ -v` until PASS.

- [ ] **Step 5: Run full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/ai/
git commit -m "fix(ai): Bedrock provider now sends bedrock-2023-05-31 and parses invoke JSON"
```

---

### Task 7: runbook — real execution engine behind --execute

`runbook run --dry-run=false` marks every non-manual step "passed" without executing anything, and the help text references an `--execute` flag that doesn't exist. Implement execution in a new `Executor` (injectable runner, per-step confirmation, timeouts, check commands, on_failure semantics, persisted run logs) and wire the flag.

**Files:**
- Create: `internal/runbook/executor.go`
- Create: `internal/runbook/executor_test.go`
- Modify: `internal/runbook/runbook.go` (add `Store.SaveRunLog`)
- Modify: `cmd/argus/main.go` (runbook `run` command ~lines 1930-2025: `--execute` flag, delegate to Executor, honest help text)

**Interfaces:**
- Produces:
  - `type CommandRunner func(ctx context.Context, command string, timeout time.Duration) (string, error)`
  - `func ShellRunner(ctx context.Context, command string, timeout time.Duration) (string, error)`
  - `type Executor struct { Out io.Writer; In io.Reader; Execute bool; Runner CommandRunner }`
  - `func (e *Executor) Run(ctx context.Context, rb *Runbook) *RunLog`
  - `func (s *Store) SaveRunLog(log *RunLog) (string, error)` — writes `<dir>/runs/<runbookID>-<timestamp>.yaml` via `fsutil.WriteFileAtomic`, returns the path.
- Consumes: `fsutil.WriteFileAtomic` (Tier 1), existing `Runbook`/`Step`/`RunLog`/`StepResult` types (`Step.Command`, `Step.Check`, `Step.Rollback`, `Step.Manual`, `Step.Timeout`; `StepResult.Status` ∈ pending/passed/failed/skipped).

- [ ] **Step 1: Write the failing tests**

Create `internal/runbook/executor_test.go`:

```go
package runbook

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeRunner records commands and returns scripted results.
type fakeRunner struct {
	commands []string
	fail     map[string]bool // command -> should fail
}

func (f *fakeRunner) run(ctx context.Context, command string, timeout time.Duration) (string, error) {
	f.commands = append(f.commands, command)
	if f.fail[command] {
		return "boom", fmt.Errorf("exit status 1")
	}
	return "ok-output", nil
}

func testRunbook() *Runbook {
	return &Runbook{
		ID:   "test-rb",
		Name: "Test Runbook",
		Steps: []Step{
			{Name: "restart", Command: "kubectl rollout restart deploy/api"},
			{Name: "verify", Command: "curl -sf http://api/health", Check: "curl -sf http://api/ready"},
		},
	}
}

func TestExecutorDryRunExecutesNothing(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader(""), Execute: false, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if len(f.commands) != 0 {
		t.Fatalf("dry-run must execute nothing, ran %v", f.commands)
	}
	for _, r := range log.StepResults {
		if r.Status != "skipped" {
			t.Errorf("dry-run step %q status = %q, want skipped", r.StepName, r.Status)
		}
	}
	if log.Status != "completed" {
		t.Errorf("dry-run log status = %q, want completed", log.Status)
	}
}

func TestExecutorRunsConfirmedCommands(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	// Confirm both steps.
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if len(f.commands) != 3 { // step1 command, step2 command, step2 check
		t.Fatalf("expected 3 executed commands (2 commands + 1 check), got %v", f.commands)
	}
	if log.StepResults[0].Status != "passed" || log.StepResults[1].Status != "passed" {
		t.Errorf("confirmed successful steps should pass: %+v", log.StepResults)
	}
	if log.StepResults[0].Output != "ok-output" {
		t.Errorf("step output not captured: %+v", log.StepResults[0])
	}
	if log.StepResults[0].Duration == "" {
		t.Errorf("step duration not recorded")
	}
	if log.Status != "completed" {
		t.Errorf("log status = %q, want completed", log.Status)
	}
}

func TestExecutorDeclinedStepFailsWithoutExecuting(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	// "n" = the operator says the state is wrong: the step fails (and the
	// default on_failure stops the run). "skip" is the way to pass over a step.
	e := &Executor{Out: &out, In: strings.NewReader("n\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "failed" {
		t.Errorf("declined step status = %q, want failed", log.StepResults[0].Status)
	}
	if len(f.commands) != 0 {
		t.Errorf("declined command must not execute, ran %v", f.commands)
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorSkippedStepContinues(t *testing.T) {
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("skip\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "skipped" {
		t.Errorf("skipped step status = %q, want skipped", log.StepResults[0].Status)
	}
	if len(log.StepResults) != 2 {
		t.Errorf("skip must continue to the next step, got %d results", len(log.StepResults))
	}
	if log.StepResults[1].Status != "passed" {
		t.Errorf("second step should run and pass, got %q", log.StepResults[1].Status)
	}
}

func TestExecutorFailedCommandMarksStepFailed(t *testing.T) {
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[0].Status != "failed" {
		t.Errorf("failed command step status = %q, want failed", log.StepResults[0].Status)
	}
	if log.StepResults[0].Error == "" {
		t.Errorf("failure must record the error")
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorFailedCheckFailsStep(t *testing.T) {
	f := &fakeRunner{fail: map[string]bool{"curl -sf http://api/ready": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), testRunbook())

	if log.StepResults[1].Status != "failed" {
		t.Errorf("step with failing check = %q, want failed", log.StepResults[1].Status)
	}
}

func TestExecutorOnFailureEscalateStops(t *testing.T) {
	rb := testRunbook()
	rb.OnFailure = "escalate"
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\ny\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	if len(log.StepResults) != 1 {
		t.Errorf("escalate must stop after the failed step, got %d results", len(log.StepResults))
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorOnFailureRollbackRunsRollbackCommand(t *testing.T) {
	rb := testRunbook()
	rb.OnFailure = "rollback"
	rb.Steps[0].Rollback = "kubectl rollout undo deploy/api"
	f := &fakeRunner{fail: map[string]bool{"kubectl rollout restart deploy/api": true}}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	ranRollback := false
	for _, c := range f.commands {
		if c == "kubectl rollout undo deploy/api" {
			ranRollback = true
		}
	}
	if !ranRollback {
		t.Errorf("on_failure=rollback must run the failed step's rollback command, ran %v", f.commands)
	}
	if log.Status != "failed" {
		t.Errorf("log status = %q, want failed", log.Status)
	}
}

func TestExecutorManualStepPrompts(t *testing.T) {
	rb := &Runbook{ID: "m", Name: "Manual", Steps: []Step{{Name: "check dashboards", Manual: true}}}
	f := &fakeRunner{}
	var out strings.Builder
	e := &Executor{Out: &out, In: strings.NewReader("y\n"), Execute: true, Runner: f.run}

	log := e.Run(context.Background(), rb)

	if log.StepResults[0].Status != "passed" {
		t.Errorf("confirmed manual step = %q, want passed", log.StepResults[0].Status)
	}
	if len(f.commands) != 0 {
		t.Errorf("manual steps must not execute commands")
	}
}

func TestShellRunnerRealCommand(t *testing.T) {
	out, err := ShellRunner(context.Background(), "echo hello", 5*time.Second)
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("output = %q, want hello", out)
	}
}

func TestShellRunnerTimeout(t *testing.T) {
	_, err := ShellRunner(context.Background(), "sleep 5", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

func TestSaveRunLogWritesAtomically(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	store := NewStore()

	log := &RunLog{RunbookID: "rb-1", RunbookName: "RB", StartedAt: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), Status: "completed"}
	path, err := store.SaveRunLog(log)
	if err != nil {
		t.Fatalf("SaveRunLog: %v", err)
	}
	if !strings.Contains(path, "rb-1-20260701-120000.yaml") {
		t.Errorf("unexpected run log path %q", path)
	}
	loaded, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(loaded), "rb-1") {
		t.Errorf("run log not readable: %v", err)
	}
}
```

(Add `"os"` to the test imports for the last test.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/runbook/ -run 'Executor|ShellRunner|SaveRunLog' -v`
Expected: FAIL — `undefined: Executor` etc. (build errors).

- [ ] **Step 3: Implement the executor**

Create `internal/runbook/executor.go`:

```go
package runbook

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// defaultStepTimeout bounds command execution when a step has no timeout.
const defaultStepTimeout = 60 * time.Second

// CommandRunner executes a shell command with a timeout and returns its
// combined output. Injectable so tests never shell out.
type CommandRunner func(ctx context.Context, command string, timeout time.Duration) (string, error)

// ShellRunner runs a command via `sh -c` with a timeout.
func ShellRunner(ctx context.Context, command string, timeout time.Duration) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("timed out after %s", timeout)
	}
	return string(out), err
}

// Executor walks a runbook's steps. Commands only run when Execute is true
// AND the operator confirms each step; otherwise steps are shown and skipped.
type Executor struct {
	Out     io.Writer
	In      io.Reader
	Execute bool
	Runner  CommandRunner // nil = ShellRunner
}

func (e *Executor) runner() CommandRunner {
	if e.Runner != nil {
		return e.Runner
	}
	return ShellRunner
}

func stepTimeout(s Step) time.Duration {
	if s.Timeout != "" {
		if d, err := time.ParseDuration(s.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return defaultStepTimeout
}

// Run executes (or walks) all steps and returns the run log.
func (e *Executor) Run(ctx context.Context, rb *Runbook) *RunLog {
	log := &RunLog{
		RunbookID:   rb.ID,
		RunbookName: rb.Name,
		StartedAt:   time.Now(),
		Status:      "running",
	}
	in := bufio.NewScanner(e.In)
	failed := false

	for i, step := range rb.Steps {
		result := StepResult{StepName: step.Name, StartedAt: time.Now()}
		prefix := fmt.Sprintf("[%d/%d]", i+1, len(rb.Steps))
		icon := "⚡"
		if step.Manual {
			icon = "🖐️"
		}
		fmt.Fprintf(e.Out, "  %s %s %s\n", prefix, icon, step.Name)
		if step.Command != "" {
			fmt.Fprintf(e.Out, "       $ %s\n", step.Command)
		}
		if step.Notes != "" {
			fmt.Fprintf(e.Out, "       💡 %s\n", step.Notes)
		}

		switch {
		case !e.Execute:
			result.Status = "skipped"
			fmt.Fprintln(e.Out, "       (dry-run: skipped)")

		case step.Manual:
			result.Status = e.confirm(in, "Done? (y/n/skip): ")

		case step.Command != "":
			answer := e.confirm(in, "Run? (y/n/skip): ")
			if answer != "passed" {
				result.Status = answer // skipped or failed (declined)
				if answer == "failed" {
					result.Error = "step declined by operator"
				}
				break
			}
			start := time.Now()
			out, err := e.runner()(ctx, step.Command, stepTimeout(step))
			result.Output = strings.TrimSpace(out)
			result.Duration = time.Since(start).Round(time.Millisecond).String()
			if err != nil {
				result.Status = "failed"
				result.Error = err.Error()
				break
			}
			if step.Check != "" {
				checkOut, checkErr := e.runner()(ctx, step.Check, stepTimeout(step))
				if checkErr != nil {
					result.Status = "failed"
					result.Error = fmt.Sprintf("check failed: %v: %s", checkErr, strings.TrimSpace(checkOut))
					break
				}
			}
			result.Status = "passed"

		default:
			// No command, not manual (check-only steps run their check).
			if step.Check != "" {
				start := time.Now()
				out, err := e.runner()(ctx, step.Check, stepTimeout(step))
				result.Output = strings.TrimSpace(out)
				result.Duration = time.Since(start).Round(time.Millisecond).String()
				if err != nil {
					result.Status = "failed"
					result.Error = err.Error()
				} else {
					result.Status = "passed"
				}
			} else {
				result.Status = "skipped"
			}
		}

		log.StepResults = append(log.StepResults, result)

		if result.Status == "failed" {
			failed = true
			switch rb.OnFailure {
			case "rollback":
				if step.Rollback != "" {
					fmt.Fprintf(e.Out, "  ↩️  on_failure=rollback — running rollback for %q\n", step.Name)
					out, err := e.runner()(ctx, step.Rollback, stepTimeout(step))
					if err != nil {
						fmt.Fprintf(e.Out, "       rollback failed: %v: %s\n", err, strings.TrimSpace(out))
					}
				}
				fmt.Fprintln(e.Out, "  ⚠️  stopping after rollback")
			case "continue":
				fmt.Fprintln(e.Out, "  ⚠️  step failed — on_failure=continue, moving on")
				continue
			default: // escalate or unset
				fmt.Fprintln(e.Out, "  ⚠️  step failed — stopping execution")
			}
			break
		}
		fmt.Fprintln(e.Out)
	}

	log.CompletedAt = time.Now()
	if failed {
		log.Status = "failed"
	} else {
		log.Status = "completed"
	}
	return log
}

// confirm prompts and maps the answer to a step status.
func (e *Executor) confirm(in *bufio.Scanner, prompt string) string {
	fmt.Fprintf(e.Out, "       %s", prompt)
	if !in.Scan() {
		return "skipped" // EOF: never execute without an explicit yes
	}
	switch strings.ToLower(strings.TrimSpace(in.Text())) {
	case "y", "yes":
		return "passed"
	case "skip", "s":
		return "skipped"
	default:
		return "failed"
	}
}
```

Note for the `TestExecutorRunsConfirmedCommands` count: confirming a command step consumes one answer; its check runs without a second confirmation.

- [ ] **Step 4: Add SaveRunLog to the store**

In `internal/runbook/runbook.go`, add (imports: `github.com/lbarahona/argus/internal/fsutil`):

```go
// SaveRunLog persists an execution log under <dir>/runs/ and returns the path.
func (s *Store) SaveRunLog(log *RunLog) (string, error) {
	dir := filepath.Join(s.dir, "runs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s.yaml", log.RunbookID, log.StartedAt.Format("20060102-150405"))
	path := filepath.Join(dir, name)
	data, err := yaml.Marshal(log)
	if err != nil {
		return "", fmt.Errorf("marshaling run log: %w", err)
	}
	return path, fsutil.WriteFileAtomic(path, data, 0o644)
}
```

- [ ] **Step 5: Run runbook package tests**

Run: `go test ./internal/runbook/ -v`
Expected: PASS (all new + existing).

- [ ] **Step 6: Wire the CLI**

In `cmd/argus/main.go`, rewrite the runbook `run` command (~lines 1930-2025). Replace the inline step loop with:

```go
	var execute bool
	runCmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Walk through a runbook step-by-step (dry-run by default)",
		Long: `Walk through a runbook step by step. By default this is a dry run:
each step is shown but nothing executes. With --execute, command and check
steps run after a per-step confirmation, with timeouts, captured output,
and a run log saved under ~/.argus/runbooks/runs/.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("\n🚀 Running: %s", rb.Name)
			if !execute {
				fmt.Print(" [DRY RUN]")
			}
			fmt.Printf("\n   %d steps\n\n", len(rb.Steps))

			exec := &runbook.Executor{
				Out:     os.Stdout,
				In:      os.Stdin,
				Execute: execute,
			}
			log := exec.Run(cmd.Context(), rb)

			if path, err := store.SaveRunLog(log); err == nil {
				fmt.Printf("   📝 run log: %s\n", path)
			} else {
				fmt.Printf("   ⚠️  could not save run log: %v\n", err)
			}

			runbook.PrintRunLog(os.Stdout, log)
			if log.Status == "failed" {
				os.Exit(1)
			}
			return nil
		},
	}
	runCmd.Flags().BoolVar(&execute, "execute", false, "Actually execute command/check steps (with per-step confirmation)")
	cmd.AddCommand(runCmd)
```

Delete the old `dryRun` flag/variable for this command if now unused.

- [ ] **Step 7: Build, run full suite, smoke the CLI**

Run: `go build ./... && go test ./... && go run ./cmd/argus runbook run --help`
Expected: builds, tests pass, help shows `--execute` and no stale claims.

- [ ] **Step 8: Commit**

```bash
git add internal/runbook/ cmd/argus/main.go
git commit -m "feat(runbook): real step execution behind --execute with confirmation, timeouts, and run logs"
```

---

### Task 8: slo — burn-rate escalation so long windows can still alert

Tier 1's corrected consumption math left `classifyStatus` nearly untrippable on long windows (a 30d SLO needs ≥60× burn for a warning). Escalate on sustained burn rate, mirroring budget's `classifyAlert` thresholds (6× ticket, 14.4× page).

**Files:**
- Modify: `internal/slo/slo.go` (`checkAvailability`, add `escalateForBurnRate`)
- Test: `internal/slo/slo_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestCheckAvailabilityBurnRateEscalation(t *testing.T) {
	c := &Checker{}
	// 0.7% error rate on a 99.9% / 30d SLO = 7x burn: consumed is tiny
	// (~5.8%) but the budget dies in ~4 days — must not report "ok".
	s := SLO{Name: "avail", Type: "availability", Service: "api", Target: 99.9, Window: "30d"}
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 700}}
	result := c.checkAvailability(s, services)
	if result.Status != "warning" {
		t.Errorf("7x burn should escalate to warning, got %q (consumed %.2f%%)", result.Status, result.BudgetConsumed)
	}

	// 2% error rate = 20x burn: page-level.
	services = []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 2000}}
	result = c.checkAvailability(s, services)
	if result.Status != "critical" {
		t.Errorf("20x burn should escalate to critical, got %q", result.Status)
	}

	// 0.9x burn stays ok (regression guard for Tier 1 behavior).
	services = []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}
	result = c.checkAvailability(s, services)
	if result.Status != "ok" {
		t.Errorf("0.9x burn should stay ok, got %q", result.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slo/ -run TestCheckAvailabilityBurnRateEscalation -v`
Expected: FAIL — 7x and 20x burn report "ok".

- [ ] **Step 3: Implement**

In `checkAvailability`, replace `result.Status = classifyStatus(result.BudgetConsumed)` with:

```go
	result.Status = escalateForBurnRate(classifyStatus(result.BudgetConsumed), result.BurnRate)
```

and add:

```go
// escalateForBurnRate raises an ok/warning status when the burn rate alone
// demands attention. Long SLO windows make consumed-based thresholds nearly
// untrippable from a 6h observation (a 30d SLO needs 60x burn to hit
// "warning"), so sustained burn escalates directly — thresholds mirror the
// budget package's classifyAlert (6x ticket, 14.4x page).
func escalateForBurnRate(status string, burnRate float64) string {
	rank := map[string]int{"ok": 0, "warning": 1, "critical": 2, "exhausted": 3}
	var escalated string
	switch {
	case burnRate >= 14.4:
		escalated = "critical"
	case burnRate >= 6:
		escalated = "warning"
	default:
		return status
	}
	if rank[escalated] > rank[status] {
		return escalated
	}
	return status
}
```

- [ ] **Step 4: Run slo tests and full suite**

Run: `go test ./internal/slo/ -v && go test ./...`
Expected: PASS — pre-existing tests are unaffected (their burns are either <6x with statuses from consumption, or already ≥warning from saturation).

- [ ] **Step 5: Commit**

```bash
git add internal/slo/
git commit -m "fix(slo): escalate status on sustained burn rate so long-window SLOs can alert"
```

---

### Task 9: config — atomic saves; fsutil — fsync before rename

`config.Save` has the same truncate-on-crash risk Tier 1 fixed for incidents/postmortems. Also add `Sync()` to `WriteFileAtomic` (closing the Tier 1 review's durability note) and update its docstring.

**Files:**
- Modify: `internal/config/config.go` (`Save`)
- Modify: `internal/fsutil/fsutil.go` (`WriteFileAtomic`)
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the test (pins behavior)**

Add to `internal/config/config_test.go` (mirror the file's existing HOME-override pattern):

```go
func TestSaveLeavesNoTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &types.Config{DefaultInstance: "prod", Instances: map[string]types.Instance{
		"prod": {URL: "https://signoz.example.com"},
	}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded.DefaultInstance != "prod" {
		t.Errorf("roundtrip lost data: %+v", loaded)
	}

	info, _ := os.Stat(Path())
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config perm = %v, want 0600 (contains API keys)", info.Mode().Perm())
	}
}
```

If the file's existing tests use a helper instead of `t.Setenv`, follow that pattern.

- [ ] **Step 2: Run it (should pass pre-change — it pins the contract)**

Run: `go test ./internal/config/ -run TestSaveLeavesNoTempFiles -v`
Expected: PASS.

- [ ] **Step 3: Swap the write and add fsync**

In `internal/config/config.go` `Save`, replace:

```go
	if err := os.WriteFile(Path(), data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
```

with (add the fsutil import):

```go
	if err := fsutil.WriteFileAtomic(Path(), data, 0o600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
```

In `internal/fsutil/fsutil.go`, add a `Sync` before `Close` and update the docstring:

```go
// WriteFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so readers never observe a partial write and a crash
// mid-write never leaves a truncated file behind. The temp file is fsynced
// before the rename. The rename is atomic on POSIX filesystems.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Run config + fsutil + store tests**

Run: `go test ./internal/config/ ./internal/fsutil/ ./internal/incident/ ./internal/postmortem/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/ internal/fsutil/
git commit -m "fix(config): atomic config writes; fsutil now fsyncs before rename"
```

---

### Task 10: budget — cap computeBurnWindow's window fraction

`BudgetUsed = BurnRate * (mins/WindowMinutes) * 100` is uncapped when the labeled window exceeds the SLO window (a "6h" burn window against a 1h SLO yields fraction 6.0). Cap the fraction at 1 and the result at 100, consistent with `calculateOverallBudget`.

**Files:**
- Modify: `internal/budget/budget.go` (`computeBurnWindow`)
- Test: `internal/budget/budget_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestComputeBurnWindowFractionCapped(t *testing.T) {
	// SLO window (1h) shorter than the 6h burn window: fraction must cap at 1.
	s := slo.SLO{Service: "api", Target: 99.9, Window: "1h"}
	services := []types.Service{{Name: "api", NumCalls: 10000, NumErrors: 5}} // 0.05% = 0.5x burn
	bw := computeBurnWindow(s, services, "6h", 360)
	if bw.BudgetUsed > 100 {
		t.Errorf("BudgetUsed must be capped at 100, got %.1f", bw.BudgetUsed)
	}
	if bw.BudgetUsed < 45 || bw.BudgetUsed > 55 {
		t.Errorf("0.5x burn with capped fraction should use ~50%%, got %.1f", bw.BudgetUsed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/budget/ -run TestComputeBurnWindowFractionCapped -v`
Expected: FAIL — BudgetUsed = 300.

- [ ] **Step 3: Implement**

In `computeBurnWindow`, replace:

```go
	if allowedRate > 0 {
		bw.BurnRate = bw.ErrorRate / allowedRate
		bw.BudgetUsed = bw.BurnRate * (float64(mins) / float64(s.WindowMinutes())) * 100
	}
```

with:

```go
	if allowedRate > 0 {
		bw.BurnRate = bw.ErrorRate / allowedRate
		windowMins := float64(s.WindowMinutes())
		observed := math.Min(float64(mins), windowMins)
		bw.BudgetUsed = math.Min(100, bw.BurnRate*(observed/windowMins)*100)
	}
```

- [ ] **Step 4: Run budget tests**

Run: `go test ./internal/budget/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/budget/
git commit -m "fix(budget): cap burn-window fraction when SLO window is shorter than the observation"
```

---

### Task 11: mcpserver — pin default and active_only am_alerts params

The Tier 1 fix pinned `all=true`; pin the other two paths so the truth table is self-verifying.

**Files:**
- Modify: `internal/mcpserver/server_happypath_test.go` (extend `TestTool_AmAlerts_AllIncludesActive` or add a sibling)

- [ ] **Step 1: Extend the test**

In the existing `TestTool_AmAlerts_AllIncludesActive`, after the `all: true` call-and-assert block, add two more cases inside the same `withMockIntegrations` closure (reset `gotQuery` between calls):

```go
		gotQuery = nil
		result = callTool(t, cs, "argus_am_alerts", map[string]any{})
		if result.IsError {
			t.Fatalf("default call errored: %s", textOf(t, result))
		}
		defaultQuery := gotQuery

		gotQuery = nil
		result = callTool(t, cs, "argus_am_alerts", map[string]any{"active_only": true})
		if result.IsError {
			t.Fatalf("active_only call errored: %s", textOf(t, result))
		}
		activeOnlyQuery := gotQuery
```

then after the closure:

```go
	if defaultQuery.Get("active") != "true" || defaultQuery.Get("silenced") != "false" || defaultQuery.Get("inhibited") != "false" {
		t.Errorf("default params = %v, want active=true silenced=false inhibited=false", defaultQuery)
	}
	if activeOnlyQuery.Get("active") != "true" || activeOnlyQuery.Get("silenced") != "false" || activeOnlyQuery.Get("inhibited") != "false" {
		t.Errorf("active_only params = %v, want active=true silenced=false inhibited=false", activeOnlyQuery)
	}
```

Declare `defaultQuery`/`activeOnlyQuery` (`url.Values`) outside the closure. Adjust variable names to the existing test's structure — keep one test with the full truth table rather than three near-identical tests.

- [ ] **Step 2: Run the test**

Run: `go test ./internal/mcpserver/ -run TestTool_AmAlerts_AllIncludesActive -v`
Expected: PASS (this pins existing behavior).

- [ ] **Step 3: Commit**

```bash
git add internal/mcpserver/
git commit -m "test(mcp): pin full am_alerts flag truth table"
```

---

### Task 12: Full verification sweep

**Files:** none (verification only)

- [ ] **Step 1: Full suite, vet, build**

Run: `go test ./... && go vet ./... && make build`
Expected: all packages ok, vet clean, binary builds.

- [ ] **Step 2: CLI smoke checks**

Run: `go run ./cmd/argus runbook run --help && go run ./cmd/argus watch --help`
Expected: help renders; runbook help mentions `--execute` and makes no false claims.

- [ ] **Step 3: Confirm branch state**

Run: `git status --short && git log --oneline fix/tier1-correctness..HEAD`
Expected: clean tree; ~11 commits on `feat/tier2-dead-features`.

---

## Deferred (explicit non-goals for this plan — Tier 3 candidates)

- Postmortem enrichment windowing (query the incident's absolute time range — needs a new optional windowed-querier interface).
- SLO `checkLatency` sampling/windowing.
- Loki matrix/vector query decoding, `loki summary` series matcher, `loki stats` default query.
- AI streaming robustness: mid-stream error events, `max_tokens` surfacing for Anthropic/OpenAI, SSE 64KB scanner cap, HTTP timeouts, context propagation through `Analyzer`.
- Postmortem AI-response parsing robustness (markdown headers).
- Newest-N truncation bias in diff/forecast/deploy/timeline; deploy empty-bucket latency false positives.
- CLI consolidation, flag standardization, exit-code unification — Tier 4.

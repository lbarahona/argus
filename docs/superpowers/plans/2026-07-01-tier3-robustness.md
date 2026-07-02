# Tier 3 Robustness Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the remaining major bug families: postmortems that record the wrong time window and silently discard AI analysis, diff/forecast/deploy/timeline biased by newest-N truncation, deploy's false latency change-points, Loki metric queries that error out, AI streams that truncate silently, plus a hardening batch (panics, path traversal, ambiguous IDs, UTF-8-splitting truncation, nondeterministic output).

**Architecture:** Absolute time-range querying is added to the Signoz client behind a NEW optional interface (`WindowedQuerier`) so the existing `SignozQuerier` and its 10+ mocks stay untouched; consumers type-assert and degrade honestly (with a rendered caveat) when the querier can't do windows. Loki's result decoding becomes shape-aware via a custom UnmarshalJSON. AI providers get shared transport timeouts and error-event handling; the `Analyzer` gains context-taking variants without breaking existing call sites.

**Tech Stack:** Go 1.24, stdlib only.

## Global Constraints

- Do NOT change the `signoz.SignozQuerier` interface — mocks in 10+ packages implement it. New capability goes on a NEW interface (`WindowedQuerier`) that only `*signoz.Client` implements.
- `types.Service.ErrorRate` is a percentage; `QueryLogs`/`QueryTraces` return newest-N (`limit` honored).
- Degradation must be visible: when a feature falls back to less-correct data (no windowed querier, truncated fetch), the output must say so — never silently pretend.
- Existing exported functions keep their signatures; new behavior arrives via new methods/functions.
- Run `gofmt` on touched files (repo-wide pre-existing drift out of scope). Commit after every task.
- Work on branch `feat/tier3-robustness`, stacked on `feat/tier2-dead-features` (Task 0).

---

### Task 0: Branch setup

- [ ] **Step 1:**

```bash
cd /Users/lbarahona/Projects/argus
git checkout feat/tier2-dead-features
git checkout -b feat/tier3-robustness
go test ./... > /dev/null && echo BASELINE-OK
```

Expected: `BASELINE-OK`

---

### Task 1: signoz — absolute time-range queries behind a new `WindowedQuerier` interface

**Files:**
- Modify: `internal/signoz/client.go`
- Test: `internal/signoz/client_test.go`

**Interfaces (produces — later tasks depend on these exact signatures):**

```go
// WindowedQuerier is implemented by queriers that support absolute
// time-range queries. SignozQuerier's methods always anchor to time.Now();
// consumers that need historical windows type-assert to this interface and
// must degrade visibly when it is unavailable.
type WindowedQuerier interface {
	QueryLogsRange(ctx context.Context, service string, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error)
	QueryTracesRange(ctx context.Context, service string, start, end time.Time, limit int) (*types.QueryResult, error)
	ListServicesRange(ctx context.Context, start, end time.Time) ([]types.Service, error)
}
```

- [ ] **Step 1: Write the failing tests**

Add to `internal/signoz/client_test.go`:

```go
func TestQueryLogsRangeUsesAbsoluteWindow(t *testing.T) {
	var payload QueryRangePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	start := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 11, 0, 0, 0, time.UTC)

	client := New(types.Instance{URL: server.URL})
	if _, err := client.QueryLogsRange(context.Background(), "api", start, end, 50, "ERROR"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload.Start != start.UnixMilli() || payload.End != end.UnixMilli() {
		t.Errorf("payload window = [%d, %d], want [%d, %d]",
			payload.Start, payload.End, start.UnixMilli(), end.UnixMilli())
	}
}

func TestListServicesRangeSendsAbsoluteWindow(t *testing.T) {
	var gotStart, gotEnd string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotStart, gotEnd = body["start"], body["end"]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	start := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 30, 11, 0, 0, 0, time.UTC)

	client := New(types.Instance{URL: server.URL})
	if _, err := client.ListServicesRange(context.Background(), start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotStart != fmt.Sprintf("%d", start.UnixNano()) || gotEnd != fmt.Sprintf("%d", end.UnixNano()) {
		t.Errorf("services window = [%s, %s], want the absolute range in ns", gotStart, gotEnd)
	}
}

func TestClientImplementsWindowedQuerier(t *testing.T) {
	var _ WindowedQuerier = (*Client)(nil)
}
```

- [ ] **Step 2: Verify they fail**

Run: `go test ./internal/signoz/ -run 'Range|WindowedQuerier' -v`
Expected: build errors (`undefined: WindowedQuerier` etc.)

- [ ] **Step 3: Implement**

In `internal/signoz/client.go`:

1. Add `StartTime`/`EndTime` to `QueryRangeParams`:

```go
type QueryRangeParams struct {
	DataSource         string
	PanelType          string // "list" or "graph"
	AggregateOperator  string // "noop", "avg", "sum", etc.
	AggregateAttribute *AggregateAttribute
	Filters            []FilterItem
	OrderBy            []OrderByItem
	SelectColumns      []SelectColumn
	Limit              int
	DurationMinutes    int
	// StartTime/EndTime, when both non-zero, pin the query to an absolute
	// window instead of the DurationMinutes lookback from now.
	StartTime time.Time
	EndTime   time.Time
}
```

2. In `BuildQueryRangePayload`, replace the window computation at the top:

```go
	now := time.Now()
	start := now.Add(-time.Duration(params.DurationMinutes) * time.Minute)
	if !params.StartTime.IsZero() && !params.EndTime.IsZero() {
		start = params.StartTime
		now = params.EndTime
	}
```

(The `step` computation and the trailing `Start: start.UnixMilli(), End: now.UnixMilli()` lines stay as they are — they now use the pinned values.)

3. Extract the request-building bodies of `QueryLogs`/`QueryTraces` so the range variants share them. Concretely, change `QueryLogs` to delegate:

```go
// QueryLogs queries logs from Signoz over the last durationMinutes.
func (c *Client) QueryLogs(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
	return c.queryLogs(ctx, service, durationMinutes, time.Time{}, time.Time{}, limit, severityFilter)
}

// QueryLogsRange queries logs over an absolute [start, end] window.
func (c *Client) QueryLogsRange(ctx context.Context, service string, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error) {
	return c.queryLogs(ctx, service, 0, start, end, limit, severityFilter)
}

func (c *Client) queryLogs(ctx context.Context, service string, durationMinutes int, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error) {
	// ... existing QueryLogs body, with the payload built as:
	payload := BuildQueryRangePayload(QueryRangeParams{
		DataSource:        "logs",
		PanelType:         "list",
		AggregateOperator: "noop",
		Filters:           filters,
		OrderBy:           []OrderByItem{{ColumnName: "timestamp", Order: "desc"}},
		Limit:             limit,
		DurationMinutes:   durationMinutes,
		StartTime:         start,
		EndTime:           end,
	})
	// ... rest unchanged
}
```

Do the same split for `QueryTraces` → `QueryTracesRange`/`queryTraces`.

4. Extract `ListServices`'s body into a range-taking helper:

```go
// ListServices returns services known to Signoz over the default 6h window.
func (c *Client) ListServices(ctx context.Context) ([]types.Service, error) {
	now := time.Now()
	return c.ListServicesRange(ctx, now.Add(-6*time.Hour), now)
}

// ListServicesRange returns services with aggregates over [start, end].
func (c *Client) ListServicesRange(ctx context.Context, start, end time.Time) ([]types.Service, error) {
	// ... existing ListServices body, using start/end for the request fields
}
```

5. Add the `WindowedQuerier` interface (code in the Interfaces block above) plus a compile-time check `var _ WindowedQuerier = (*Client)(nil)`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/signoz/ -v && go test ./...`
Expected: PASS everywhere — `SignozQuerier` is untouched, so no mock breaks.

- [ ] **Step 5: Commit**

```bash
git add internal/signoz/
git commit -m "feat(signoz): absolute time-range queries behind new WindowedQuerier interface"
```

---

### Task 2: postmortem — enrich from the incident's window, not from now

`enrichWithMetrics` computes a lookback anchored to `time.Now()`, so a postmortem generated the day after an incident permanently records today's unrelated traffic (and feeds it to the AI RCA prompt).

**Files:**
- Modify: `internal/postmortem/postmortem.go` (`enrichWithMetrics`; add `DataCaveat string` field to the `Postmortem` struct with yaml/json tags matching the file's style; render it in terminal+markdown renderers)
- Test: `internal/postmortem/postmortem_test.go`

**Interfaces:**
- Consumes: `signoz.WindowedQuerier` (Task 1).

- [ ] **Step 1: Write the failing tests**

Add to `internal/postmortem/postmortem_test.go` — extend the existing `mockQuerier` with a windowed variant:

```go
// windowedMockQuerier implements both SignozQuerier and signoz.WindowedQuerier.
type windowedMockQuerier struct {
	mockQuerier
	gotLogStart, gotLogEnd   time.Time
	gotSvcStart, gotSvcEnd   time.Time
	rangeLogs                []types.LogEntry
	rangeServices            []types.Service
}

func (m *windowedMockQuerier) QueryLogsRange(ctx context.Context, service string, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error) {
	m.gotLogStart, m.gotLogEnd = start, end
	return &types.QueryResult{Logs: m.rangeLogs}, nil
}

func (m *windowedMockQuerier) QueryTracesRange(ctx context.Context, service string, start, end time.Time, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}

func (m *windowedMockQuerier) ListServicesRange(ctx context.Context, start, end time.Time) ([]types.Service, error) {
	m.gotSvcStart, m.gotSvcEnd = start, end
	return m.rangeServices, nil
}

func TestEnrichWithMetricsUsesIncidentWindow(t *testing.T) {
	incidentStart := time.Now().Add(-24 * time.Hour)
	incidentEnd := incidentStart.Add(1 * time.Hour)
	pm := &Postmortem{
		Services:     []string{"api"},
		IncidentTime: IncidentTime{Start: incidentStart, End: incidentEnd, Duration: "1h"},
	}
	mock := &windowedMockQuerier{
		rangeServices: []types.Service{{Name: "api", NumCalls: 1000, NumErrors: 30, ErrorRate: 3.0}},
		rangeLogs:     []types.LogEntry{{Body: "boom", ServiceName: "api"}},
	}

	enrichWithMetrics(context.Background(), pm, mock)

	wantStart := incidentStart.Add(-10 * time.Minute)
	wantEnd := incidentEnd.Add(10 * time.Minute)
	if !mock.gotSvcStart.Equal(wantStart) || !mock.gotSvcEnd.Equal(wantEnd) {
		t.Errorf("service window = [%v, %v], want incident window ±10m [%v, %v]",
			mock.gotSvcStart, mock.gotSvcEnd, wantStart, wantEnd)
	}
	if !mock.gotLogStart.Equal(wantStart) || !mock.gotLogEnd.Equal(wantEnd) {
		t.Errorf("log window = [%v, %v], want [%v, %v]", mock.gotLogStart, mock.gotLogEnd, wantStart, wantEnd)
	}
	if pm.DataCaveat != "" {
		t.Errorf("windowed enrichment must not set a caveat, got %q", pm.DataCaveat)
	}
	if pm.Metrics.PeakErrorRate != 3.0 {
		t.Errorf("metrics not populated from range data: %+v", pm.Metrics)
	}
}

func TestEnrichWithMetricsFallbackSetsCaveat(t *testing.T) {
	incidentStart := time.Now().Add(-24 * time.Hour)
	pm := &Postmortem{
		Services:     []string{"api"},
		IncidentTime: IncidentTime{Start: incidentStart, End: incidentStart.Add(time.Hour), Duration: "1h"},
	}
	// Plain mockQuerier does NOT implement WindowedQuerier.
	mock := &mockQuerier{}

	enrichWithMetrics(context.Background(), pm, mock)

	if pm.DataCaveat == "" {
		t.Error("non-windowed fallback must set DataCaveat so the report is honest about its data")
	}
}
```

Adapt field/constructor names to the file's actual `mockQuerier` and `IncidentTime` struct (check the real field names — e.g. if `IncidentTime` is a different type name, mirror it).

- [ ] **Step 2: Verify they fail**

Run: `go test ./internal/postmortem/ -run EnrichWithMetrics -v`
Expected: FAIL (no DataCaveat field; window not used).

- [ ] **Step 3: Implement**

In `internal/postmortem/postmortem.go`:

1. Add to the `Postmortem` struct: `DataCaveat string \`yaml:"data_caveat,omitempty" json:"data_caveat,omitempty"\`` (match tag style of neighbors).

2. Rework `enrichWithMetrics`:

```go
const enrichPadding = 10 * time.Minute

func enrichWithMetrics(ctx context.Context, pm *Postmortem, q signoz.SignozQuerier) {
	start := pm.IncidentTime.Start.Add(-enrichPadding)
	end := pm.IncidentTime.End.Add(enrichPadding)

	wq, windowed := q.(signoz.WindowedQuerier)
	if !windowed {
		pm.DataCaveat = "Signoz metrics reflect the query time, not the incident window (querier does not support absolute time ranges)."
	}

	// Service metrics
	var services []types.Service
	var err error
	if windowed {
		services, err = wq.ListServicesRange(ctx, start, end)
	} else {
		services, err = q.ListServices(ctx)
	}
	if err != nil {
		return
	}

	// ... existing aggregation over services (unchanged) ...

	// Top errors from affected services
	if len(pm.Services) > 0 {
		dur := pm.IncidentTime.End.Sub(pm.IncidentTime.Start)
		durationMinutes := int(dur.Minutes()) + 20
		if durationMinutes < 30 {
			durationMinutes = 30
		}
		for _, svcName := range pm.Services {
			var result *types.QueryResult
			var qerr error
			if windowed {
				result, qerr = wq.QueryLogsRange(ctx, svcName, start, end, 50, "error")
			} else {
				result, qerr = q.QueryLogs(ctx, svcName, durationMinutes, 50, "error")
			}
			if qerr != nil || result == nil || len(result.Logs) == 0 {
				continue
			}
			// ... existing msgCounts aggregation (unchanged) ...
		}
		// ... existing sort/top-10 (unchanged) ...
	}
}
```

3. Render `DataCaveat` in both the terminal and markdown renderers (a single warning line near the metrics section, e.g. `⚠️  <caveat>` / `> **Data caveat:** <caveat>`), and include it in `buildAIPrompt` when set (one line: `NOTE: <caveat>`), so the AI is not fed wrong-window data unknowingly.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/postmortem/ -v && go test ./...`
Expected: PASS. The CLI passes `*signoz.Client`, which implements `WindowedQuerier`, so real usage gets true incident windows.

- [ ] **Step 5: Commit**

```bash
git add internal/postmortem/
git commit -m "fix(postmortem): enrich metrics from the incident window; honest caveat on fallback"
```

---

### Task 3: postmortem — AI response parsing survives markdown headers; raw fallback

`parseAIResponse` requires literal `ROOT CAUSE:` line prefixes; `**ROOT CAUSE:**`, `## ROOT CAUSE`, or `1. ROOT CAUSE:` silently discard the (already paid-for) analysis, leaving an empty root cause.

**Files:**
- Modify: `internal/postmortem/postmortem.go` (`parseAIResponse`, add `normalizeHeaderLine`)
- Test: `internal/postmortem/postmortem_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestParseAIResponseMarkdownHeaders(t *testing.T) {
	cases := []string{
		"**ROOT CAUSE:** The pool was exhausted.\n\n**LESSONS LEARNED:**\n- Size pools",
		"## ROOT CAUSE\nThe pool was exhausted.\n\n## LESSONS LEARNED\n- Size pools",
		"1. ROOT CAUSE: The pool was exhausted.\n2. LESSONS LEARNED:\n- Size pools",
	}
	for i, response := range cases {
		pm := &Postmortem{}
		parseAIResponse(pm, response)
		if !strings.Contains(pm.RootCause, "pool was exhausted") {
			t.Errorf("case %d: root cause lost from markdown-formatted response, got %q", i, pm.RootCause)
		}
		if len(pm.Lessons) == 0 {
			t.Errorf("case %d: lessons lost", i)
		}
	}
}

func TestParseAIResponseNoHeadersKeepsRawAnalysis(t *testing.T) {
	pm := &Postmortem{}
	parseAIResponse(pm, "The incident was caused by a cache stampede after the deploy.")
	if !strings.Contains(pm.RootCause, "cache stampede") {
		t.Errorf("headerless response must be preserved in RootCause, got %q", pm.RootCause)
	}
}
```

- [ ] **Step 2: Verify they fail**

Run: `go test ./internal/postmortem/ -run ParseAIResponse -v`
Expected: FAIL — sections empty.

- [ ] **Step 3: Implement**

In `parseAIResponse`:

1. Add the normalizer:

```go
// normalizeHeaderLine strips leading markdown/list decoration ("## ", "**",
// "1. ", "> ") and trailing "**"/":" noise so section headers match however
// the model formats them.
func normalizeHeaderLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "#>_ ")
	s = strings.TrimPrefix(s, "**")
	// numbered lists: "1. " / "2) "
	for len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		s = s[1:]
	}
	s = strings.TrimPrefix(s, ".")
	s = strings.TrimPrefix(s, ")")
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "**")
	return s
}
```

2. Change the header-matching to use it. Where the current code computes `upper := strings.ToUpper(trimmed)` and `isSectionLine := !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "*") && !strings.HasPrefix(trimmed, "[")`, replace with:

```go
		normalized := normalizeHeaderLine(line)
		upper := strings.ToUpper(normalized)

		// Bullets ("- x", "* x") are content, but bold ("**X**") is decoration.
		trimmed := strings.TrimSpace(line)
		isSectionLine := !strings.HasPrefix(trimmed, "- ") &&
			!(strings.HasPrefix(trimmed, "* ") && !strings.HasPrefix(trimmed, "**")) &&
			!strings.HasPrefix(trimmed, "[")
```

and match `strings.HasPrefix(upper, h.prefix)` **or** `upper == strings.TrimSuffix(h.prefix, ":")` (so `## ROOT CAUSE` without a colon matches too). When stripping the header from the line, strip from `normalized` (not the raw line).

3. After the parse loop, add the raw fallback:

```go
	if len(sections) == 0 && strings.TrimSpace(response) != "" {
		// No recognizable sections: keep the analysis rather than discarding it.
		pm.RootCause = strings.TrimSpace(response)
		return
	}
```

(Place before the existing per-section assignments; keep the existing `generateBasicActionItems` fallback for the parsed path.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/postmortem/ -v`
Expected: PASS, including all pre-existing parse tests (plain `ROOT CAUSE:` headers still match — normalization is a no-op for them).

- [ ] **Step 5: Commit**

```bash
git add internal/postmortem/
git commit -m "fix(postmortem): parse markdown-formatted AI section headers; keep raw analysis as fallback"
```

---

### Task 4: diff — true two-window comparison with visible truncation

The "previous window" is carved out of one newest-500 query over `2*dur`; ≥500 recent errors make the previous window empty and everything reports "new". Use `WindowedQuerier` for a real window-A query; flag truncation.

**Files:**
- Modify: `internal/diff/diff.go` (`Compare`; add `Truncated bool` + `DataCaveat string` to `DiffResult`; render caveat in both renderers)
- Test: `internal/diff/diff_test.go`

- [ ] **Step 1: Write the failing test**

```go
// windowedDiffMock implements SignozQuerier + signoz.WindowedQuerier.
type windowedDiffMock struct {
	mockSignozClient // adapt to this file's existing mock type name
	rangeCalls []struct{ Start, End time.Time }
	rangeLogs  []types.LogEntry
}

func (m *windowedDiffMock) QueryLogsRange(ctx context.Context, service string, start, end time.Time, limit int, severityFilter string) (*types.QueryResult, error) {
	m.rangeCalls = append(m.rangeCalls, struct{ Start, End time.Time }{start, end})
	return &types.QueryResult{Logs: m.rangeLogs}, nil
}
func (m *windowedDiffMock) QueryTracesRange(ctx context.Context, service string, start, end time.Time, limit int) (*types.QueryResult, error) {
	return &types.QueryResult{}, nil
}
func (m *windowedDiffMock) ListServicesRange(ctx context.Context, start, end time.Time) ([]types.Service, error) {
	return nil, nil
}

func TestCompareUsesWindowedQueryForPreviousWindow(t *testing.T) {
	oldTS := time.Now().Add(-90 * time.Minute)
	mock := &windowedDiffMock{rangeLogs: []types.LogEntry{{ServiceName: "api", Timestamp: oldTS}}}
	// Recent window: plain QueryLogs returns one recent error for api.
	mock.queryLogsFunc = func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
		return &types.QueryResult{Logs: []types.LogEntry{{ServiceName: "api", Timestamp: time.Now().Add(-5 * time.Minute)}}}, nil
	}

	result, err := Compare(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.rangeCalls) != 1 {
		t.Fatalf("expected 1 windowed query for the previous window, got %d", len(mock.rangeCalls))
	}
	// Previous window: [now-2d, now-d]
	span := mock.rangeCalls[0].End.Sub(mock.rangeCalls[0].Start)
	if span != 60*time.Minute {
		t.Errorf("previous window span = %v, want 60m", span)
	}
	// api: 1 before, 1 after → stable, not "new"
	for _, d := range result.Services {
		if d.Name == "api" && d.Status == "new" {
			t.Errorf("api had errors in the previous window; must not be classified new")
		}
	}
}

func TestCompareSetsTruncationCaveat(t *testing.T) {
	logs := make([]types.LogEntry, 500) // == the fetch limit
	for i := range logs {
		logs[i] = types.LogEntry{ServiceName: "api", Timestamp: time.Now().Add(-time.Minute)}
	}
	mock := &mockSignozClient{ // plain, non-windowed mock
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			return &types.QueryResult{Logs: logs}, nil
		},
	}

	result, err := Compare(context.Background(), mock, "test", Options{Duration: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated || result.DataCaveat == "" {
		t.Errorf("fetch at limit must set Truncated + DataCaveat, got %+v / %q", result.Truncated, result.DataCaveat)
	}
}
```

Adapt the mock embedding to this file's actual mock type (check `internal/diff/diff_test.go` — if its mock has no `queryLogsFunc` field, add one following the alert/explain packages' pattern).

- [ ] **Step 2: Verify they fail**

Run: `go test ./internal/diff/ -run 'Windowed|Truncation' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `Compare`:

```go
	const diffFetchLimit = 500

	// Window B (recent): 0..dur minutes ago
	recentLogs, err := client.QueryLogs(ctx, "", dur, diffFetchLimit, "ERROR")
	if err != nil {
		return nil, fmt.Errorf("querying recent logs: %w", err)
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(dur) * time.Minute)

	// Window A (previous): dur..2*dur minutes ago. With a windowed querier
	// this is a real query; otherwise it is carved out of a 2*dur fetch,
	// which loses the older half once the fetch hits its limit.
	var previousWindowLogs []types.LogEntry
	var truncated bool
	if wq, ok := client.(signoz.WindowedQuerier); ok {
		prev, err := wq.QueryLogsRange(ctx, "", now.Add(-time.Duration(2*dur)*time.Minute), cutoff, diffFetchLimit, "ERROR")
		if err != nil {
			return nil, fmt.Errorf("querying previous window: %w", err)
		}
		previousWindowLogs = prev.Logs
		truncated = len(recentLogs.Logs) >= diffFetchLimit || len(prev.Logs) >= diffFetchLimit
	} else {
		previousLogs, err := client.QueryLogs(ctx, "", dur*2, diffFetchLimit, "ERROR")
		if err != nil {
			return nil, fmt.Errorf("querying previous logs: %w", err)
		}
		previousWindowLogs = previousLogs.Logs
		truncated = len(recentLogs.Logs) >= diffFetchLimit || len(previousLogs.Logs) >= diffFetchLimit
	}

	recentErrors := countByService(recentLogs.Logs, cutoff, now)
	previousErrors := countByService(previousWindowLogs, now.Add(-time.Duration(dur*2)*time.Minute), cutoff)
```

Set on the result:

```go
	result.Truncated = truncated
	if truncated {
		result.DataCaveat = fmt.Sprintf("A window hit the %d-log fetch limit; older entries are missing and per-window counts undercount.", diffFetchLimit)
	}
```

Add both fields to `DiffResult` and render the caveat as a warning line in the terminal renderer and markdown renderer (`⚠️ <caveat>`). `Compare`'s `client` parameter type stays `signoz.SignozQuerier` (the assertion is internal). Import `signoz` if not already.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/diff/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/diff/
git commit -m "fix(diff): query the previous window for real via WindowedQuerier; flag truncation"
```

---

### Task 5: forecast/deploy/timeline — visible truncation caveats

Same newest-N bias class as diff, mitigated (not redesigned) by detecting a fetch at its limit and saying so in the output.

**Files:**
- Modify: `internal/forecast/forecast.go`, `internal/deploy/deploy.go`, `internal/timeline/timeline.go` (+ their renderers)
- Test: one test per package

- [ ] **Step 1: For each package, add a `Truncated bool`/`DataCaveat string` to its result struct and set it where logs are fetched**

The pattern, at each fetch site (forecast fetches error logs with limit 1000; deploy with 1000; timeline with 500 — confirm each limit by reading the actual call):

```go
	if len(logResult.Logs) >= fetchLimit {
		result.Truncated = true
		result.DataCaveat = fmt.Sprintf("Log fetch hit the %d-entry limit; older buckets undercount and trends may be skewed toward 'rising'.", fetchLimit)
	}
```

Name each package's existing literal limit as a `const <pkg>FetchLimit` and use it in both the call and the check. Render the caveat in each package's terminal + markdown renderers as a single warning line.

- [ ] **Step 2: One test per package (failing first)**

Each test drives the package's `Generate`/`Analyze`/`Build` entry point with a mock returning exactly `fetchLimit` logs and asserts `Truncated == true` and a non-empty `DataCaveat`; and a second case below the limit asserting no caveat. Mirror each package's existing mock/test conventions.

- [ ] **Step 3: Run and commit**

Run: `go test ./internal/forecast/ ./internal/deploy/ ./internal/timeline/ -v && go test ./...`
Expected: PASS.

```bash
git add internal/forecast/ internal/deploy/ internal/timeline/
git commit -m "fix(forecast,deploy,timeline): surface newest-N truncation instead of silently skewing"
```

---

### Task 6: deploy — empty latency buckets must not fabricate change points

`bucketTraceLatency` leaves `Value=0` for empty buckets and `detectChangePoint` includes those zeros in segment means: a steady 200ms service whose older buckets are empty (newest-500 fetch) produces a false "latency increased" change point on every run.

**Files:**
- Modify: `internal/deploy/deploy.go` (the latency change-point call path)
- Test: `internal/deploy/deploy_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLatencyChangeDetectionIgnoresEmptyBuckets(t *testing.T) {
	// 12 buckets: first 9 empty (no traces — older than the newest-N fetch),
	// last 3 steady at ~200ms. No real change occurred.
	buckets := make([]DataPoint, 12)
	base := time.Now().Add(-6 * time.Hour)
	for i := range buckets {
		buckets[i].Timestamp = base.Add(time.Duration(i) * 30 * time.Minute)
	}
	buckets[9].Value, buckets[10].Value, buckets[11].Value = 200, 205, 198

	cp := detectLatencyChangePoint(buckets, "api", 50, 0.5)
	if cp != nil {
		t.Errorf("steady latency with empty older buckets must not yield a change point, got %+v", cp)
	}
}

func TestLatencyChangeDetectionStillFindsRealShifts(t *testing.T) {
	buckets := make([]DataPoint, 12)
	base := time.Now().Add(-6 * time.Hour)
	for i := range buckets {
		buckets[i].Timestamp = base.Add(time.Duration(i) * 30 * time.Minute)
		if i < 6 {
			buckets[i].Value = 100
		} else {
			buckets[i].Value = 400 // real 4x latency shift
		}
	}

	cp := detectLatencyChangePoint(buckets, "api", 50, 0.5)
	if cp == nil {
		t.Fatal("real 4x latency shift must be detected")
	}
}
```

(Adapt threshold/confidence arguments to the real `detectChangePoint` call site's values for latency — read where it's invoked with the latency buckets, around deploy.go:268, and use the same argument shape. If the production code calls `detectChangePoint(buckets, service, "p99_latency", ...)` directly, implement `detectLatencyChangePoint` as the new wrapper described below and switch the call site to it.)

- [ ] **Step 2: Verify the first test fails** (a change point IS currently produced from the zeros).

- [ ] **Step 3: Implement**

Add a wrapper that filters empty buckets for latency only (zero errors is meaningful; zero latency means "no data"):

```go
// detectLatencyChangePoint runs change-point detection over only the buckets
// that actually contain latency samples. Empty buckets (Value == 0) mean "no
// traces observed", not "0ms" — including them fabricates shifts whenever the
// newest-N trace fetch doesn't reach the oldest buckets.
func detectLatencyChangePoint(buckets []DataPoint, service string, thresholdPct, minConfidence float64) *ChangePoint {
	nonEmpty := make([]DataPoint, 0, len(buckets))
	for _, b := range buckets {
		if b.Value > 0 {
			nonEmpty = append(nonEmpty, b)
		}
	}
	return detectChangePoint(nonEmpty, service, "p99_latency", thresholdPct, minConfidence)
}
```

Switch the latency call site to it (error-count buckets keep calling `detectChangePoint` directly). Match the `metric` string to whatever the current call passes.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/deploy/ -v && go test ./...`
Expected: PASS (the second test passes because 12 non-empty buckets flow through unchanged; `detectChangePoint`'s `len < 4` guard makes sparse data return nil rather than guess).

- [ ] **Step 5: Commit**

```bash
git add internal/deploy/
git commit -m "fix(deploy): exclude empty latency buckets from change-point detection"
```

---

### Task 7: loki — decode matrix/vector results; render metric output

`ResultData.Result` is typed `[]Stream`, so metric LogQL (`rate(...)`, `count_over_time(...)`) — which the command's own help advertises — fails with a JSON decode error (matrix) or silently decodes nothing (vector).

**Files:**
- Modify: `internal/loki/types.go` (shape-aware `ResultData`), `internal/loki/client.go` (`ParseEntries` guard), `internal/loki/render.go` (add `FormatMetricSeries`), `cmd/argus/main.go` (loki `query` command's output switch)
- Test: `internal/loki/types_test.go`, `internal/loki/render_test.go`

**Interfaces:**

```go
type ResultData struct {
	ResultType string         // "streams", "matrix", "vector"
	Streams    []Stream       // populated when ResultType == "streams"
	Series     []MetricSeries // populated for "matrix" and "vector"
}

type MetricSeries struct {
	Metric map[string]string
	Values []SamplePoint
}

type SamplePoint struct {
	Timestamp time.Time
	Value     float64
}
```

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Verify failures** (`go test ./internal/loki/ -run ResultData -v` — matrix/vector fail today).

- [ ] **Step 3: Implement**

In `internal/loki/types.go`, replace `ResultData` with the shape-aware version and a custom unmarshaller:

```go
// ResultData holds the typed result from a Loki query. Loki returns three
// shapes keyed by resultType: log streams, and Prometheus-style matrix or
// vector samples for metric LogQL (rate, count_over_time, ...).
type ResultData struct {
	ResultType string
	Streams    []Stream
	Series     []MetricSeries
}

// MetricSeries is one matrix/vector series.
type MetricSeries struct {
	Metric map[string]string
	Values []SamplePoint
}

// SamplePoint is one sample of a metric series.
type SamplePoint struct {
	Timestamp time.Time
	Value     float64
}

func (d *ResultData) UnmarshalJSON(b []byte) error {
	var raw struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	d.ResultType = raw.ResultType
	if len(raw.Result) == 0 {
		return nil
	}

	switch raw.ResultType {
	case "matrix":
		var rows []struct {
			Metric map[string]string    `json:"metric"`
			Values [][2]json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(raw.Result, &rows); err != nil {
			return fmt.Errorf("decoding matrix result: %w", err)
		}
		for _, r := range rows {
			s := MetricSeries{Metric: r.Metric}
			for _, v := range r.Values {
				if p, ok := samplePoint(v); ok {
					s.Values = append(s.Values, p)
				}
			}
			d.Series = append(d.Series, s)
		}
	case "vector":
		var rows []struct {
			Metric map[string]string  `json:"metric"`
			Value  [2]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw.Result, &rows); err != nil {
			return fmt.Errorf("decoding vector result: %w", err)
		}
		for _, r := range rows {
			s := MetricSeries{Metric: r.Metric}
			if p, ok := samplePoint(r.Value); ok {
				s.Values = append(s.Values, p)
			}
			d.Series = append(d.Series, s)
		}
	default: // "streams" and unknown types that carry stream-shaped results
		if err := json.Unmarshal(raw.Result, &d.Streams); err != nil {
			return fmt.Errorf("decoding streams result: %w", err)
		}
	}
	return nil
}

// samplePoint converts a [unix_seconds, "value"] pair. Loki emits the
// timestamp as a bare float number and the value as a quoted string, so each
// element is parsed from its raw JSON form.
func samplePoint(pair [2]json.RawMessage) (SamplePoint, bool) {
	var ts float64
	if err := json.Unmarshal(pair[0], &ts); err != nil {
		return SamplePoint{}, false
	}
	var valStr string
	if err := json.Unmarshal(pair[1], &valStr); err != nil {
		// tolerate a bare-number value as well
		var valNum float64
		if err2 := json.Unmarshal(pair[1], &valNum); err2 != nil {
			return SamplePoint{}, false
		}
		sec := int64(ts)
		return SamplePoint{Timestamp: time.Unix(sec, int64((ts-float64(sec))*1e9)), Value: valNum}, true
	}
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return SamplePoint{}, false
	}
	sec := int64(ts)
	return SamplePoint{Timestamp: time.Unix(sec, int64((ts-float64(sec))*1e9)), Value: val}, true
}
```

(Add `encoding/json`, `fmt`, `strconv`, and keep `time` in types.go imports.)

Update every reference to `Data.Result` across the package (compiler will list them): `ParseEntries` iterates `result.Data.Streams`; `FormatLogEntries` unchanged.

In `internal/loki/render.go`, add:

```go
// FormatMetricSeries renders matrix/vector results as a compact table:
// one row per series with its labels and latest value (+ sample count).
func FormatMetricSeries(data ResultData) string {
	if len(data.Series) == 0 {
		return "No metric samples found.\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d series (%s)\n\n", len(data.Series), data.ResultType))
	for _, s := range data.Series {
		latest := s.Values[len(s.Values)-1]
		b.WriteString(fmt.Sprintf("  %s\n    latest: %.4g at %s (%d samples)\n",
			formatLabelSet(s.Metric), latest.Value,
			latest.Timestamp.Format("15:04:05"), len(s.Values)))
	}
	return b.String()
}
```

Guard `s.Values` non-empty (skip empty series). In `cmd/argus/main.go`'s loki `query` command, branch on the result type before formatting: if `result.Data.ResultType != "streams"` (and not empty), print `FormatMetricSeries(result.Data)` for terminal format (JSON path unchanged); otherwise the existing `ParseEntries` + `FormatLogEntries` path.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/loki/ -v && go build ./... && go test ./...`
Expected: PASS; pre-existing stream tests keep passing.

- [ ] **Step 5: Commit**

```bash
git add internal/loki/ cmd/argus/main.go
git commit -m "fix(loki): decode matrix/vector metric results and render them"
```

---

### Task 8: loki — summary series matcher and stats default query

`BuildSummary` counts series with `{__name__=~".+"}` — a Prometheus label Loki streams never have (always 0) — and calls `IndexStats` with an empty query, which Loki 400s (silently swallowed in summary; a hard error in `argus loki stats` with default flags).

**Files:**
- Modify: `internal/loki/client.go` (`BuildSummary`, add `matchAllSelector`), `cmd/argus/main.go` (loki `stats` command default)
- Test: `internal/loki/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
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

	c := NewClient(LokiConfig{URL: server.URL}) // adapt to the package's constructor name
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
```

(Adapt constructor and health-endpoint details to the existing tests in this file.)

- [ ] **Step 2: Verify failures.**

- [ ] **Step 3: Implement**

```go
// matchAllSelector builds a match-all LogQL selector from the instance's own
// labels. Loki has no __name__ label (that is Prometheus), so a usable
// selector must reference a label that actually exists.
func matchAllSelector(labels []string) string {
	preferred := []string{"job", "app", "service_name", "namespace"}
	for _, p := range preferred {
		for _, l := range labels {
			if l == p {
				return fmt.Sprintf(`{%s=~".+"}`, p)
			}
		}
	}
	for _, l := range labels {
		if !strings.HasPrefix(l, "__") {
			return fmt.Sprintf(`{%s=~".+"}`, l)
		}
	}
	return ""
}
```

In `BuildSummary`, capture the labels result (it's already fetched) and derive the selector once:

```go
	var labelNames []string
	if labels, err := c.Labels(ctx, start, now); err == nil {
		s.Labels = len(labels)
		labelNames = labels
	}

	if sel := matchAllSelector(labelNames); sel != "" {
		if series, err := c.Series(ctx, []string{sel}, start, now); err == nil {
			s.Series = len(series)
		}
		if stats, err := c.IndexStats(ctx, sel, start, now); err == nil {
			s.Stats = stats
		}
	}
```

In `cmd/argus/main.go`'s loki `stats` command: when the `-q` flag is empty, fetch labels and derive `matchAllSelector` (export it as `MatchAllSelector` if main needs it — exporting is fine) before calling `IndexStats`; if no selector can be derived, return a helpful error (`"no labels found to build a default query; pass -q '{label=~\".+\"}'"`).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/loki/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/loki/ cmd/argus/main.go
git commit -m "fix(loki): derive real match-all selector for summary/stats; stats works with default flags"
```

---

### Task 9: ai — streams fail loudly, big lines survive, transports time out

Mid-stream `{"type":"error"}` events are ignored (half a report presented as success), `max_tokens` truncation is silent, the SSE scanner dies at 64KB lines, and the HTTP clients have no dial/TLS/header timeouts.

**Files:**
- Modify: `internal/ai/anthropic.go`, `internal/ai/openai.go`, `internal/ai/provider.go` (shared transport helper)
- Test: `internal/ai/analyzer_test.go` (or the file holding the existing SSE tests)

- [ ] **Step 1: Write the failing tests**

```go
func TestAnthropicStreamErrorEventFailsLoudly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer server.Close()

	p := NewAnthropicProvider("key", "model")
	p.baseURL = server.URL // adapt to how existing tests point the provider at a test server
	var buf bytes.Buffer
	err := p.Analyze(context.Background(), "q", &buf)
	if err == nil {
		t.Fatal("mid-stream error event must surface as an error, not silent truncation")
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error should carry the API message, got %v", err)
	}
}

func TestAnthropicStreamMaxTokensNoted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n")
	}))
	defer server.Close()

	p := NewAnthropicProvider("key", "model")
	p.baseURL = server.URL
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "q", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "truncated at max_tokens") {
		t.Errorf("max_tokens truncation must be noted in output, got %q", buf.String())
	}
}

func TestAnthropicStreamLongLine(t *testing.T) {
	long := strings.Repeat("x", 200*1024) // > default 64KB scanner cap
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"%s\"}}\n\n", long)
	}))
	defer server.Close()

	p := NewAnthropicProvider("key", "model")
	p.baseURL = server.URL
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "q", &buf); err != nil {
		t.Fatalf("200KB SSE line must not kill the stream: %v", err)
	}
	if len(buf.String()) < 200*1024 {
		t.Errorf("long delta truncated: got %d bytes", len(buf.String()))
	}
}
```

Adapt the provider-pointing mechanism (`p.baseURL = ...`) to whatever the existing provider tests do (check how `analyzer_test.go`/`anthropic_test.go` inject test-server URLs — there may be a constructor or field for it). Mirror the error-event and long-line tests for the OpenAI provider (`finish_reason: "length"` is OpenAI's max-tokens marker; its error events arrive as a non-200 or an `{"error": {...}}` JSON line — handle the JSON-line form).

- [ ] **Step 2: Verify failures.**

- [ ] **Step 3: Implement**

1. In `internal/ai/provider.go` add a shared transport:

```go
// newHTTPClient returns a client suitable for streaming: no overall timeout
// (streams are long-lived) but bounded dial/TLS/first-byte phases so a hung
// endpoint cannot block a CLI or MCP call forever.
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
}
```

Use it in all three providers' constructors (replace `&http.Client{}`).

2. In `streamAnthropicResponse`, extend the event struct and handling:

```go
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	truncated := false
	for scanner.Scan() {
		// ... prefix handling unchanged ...
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch {
		case event.Type == "error":
			fmt.Fprintln(w)
			return fmt.Errorf("stream error from API: %s: %s", event.Error.Type, event.Error.Message)
		case event.Type == "content_block_delta" && event.Delta.Type == "text_delta":
			fmt.Fprint(w, event.Delta.Text)
		case event.Type == "message_delta" && event.Delta.StopReason == "max_tokens":
			truncated = true
		}
	}
	fmt.Fprintln(w)
	if truncated {
		fmt.Fprintln(w, "[response truncated at max_tokens]")
	}
	return scanner.Err()
```

3. Mirror in the OpenAI stream parser: `scanner.Buffer(...)`; a JSON line/data payload with a top-level `"error"` object returns an error; `choices[0].finish_reason == "length"` sets the truncation note.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ai/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ai/
git commit -m "fix(ai): surface stream errors and max_tokens truncation; bound transport phases; raise scanner cap"
```

---

### Task 10: ai — context propagation for TUI and MCP

`Analyzer.Analyze/AnalyzeWithHistory/AnalyzeSync` hardwire `context.Background()`, so MCP request cancellation and TUI Ctrl-C never reach the HTTP call.

**Files:**
- Modify: `internal/ai/analyzer.go` (add ctx variants; existing methods delegate)
- Modify: `internal/mcpserver/server.go` (handlers pass their ctx), `internal/tui/tui.go` (session ctx)
- Test: `internal/ai/analyzer_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAnalyzeContextIsCancellable(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done() // hang until the client gives up
	}))
	defer server.Close()

	p := NewAnthropicProvider("key", "model")
	p.baseURL = server.URL
	a := NewFromProvider(p)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()

	var buf bytes.Buffer
	err := a.AnalyzeContext(ctx, "q", &buf)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context must abort the call, got %v", err)
	}
}
```

- [ ] **Step 2: Verify it fails** (`undefined: AnalyzeContext`).

- [ ] **Step 3: Implement**

```go
// AnalyzeContext is Analyze with caller-controlled cancellation.
func (a *Analyzer) AnalyzeContext(ctx context.Context, prompt string, w io.Writer) error {
	return a.provider.Analyze(ctx, prompt, w)
}

// AnalyzeWithHistoryContext is AnalyzeWithHistory with cancellation.
func (a *Analyzer) AnalyzeWithHistoryContext(ctx context.Context, systemPrompt string, messages []Message, w io.Writer) error {
	return a.provider.AnalyzeWithSystem(ctx, systemPrompt, messages, w)
}

// AnalyzeSyncContext is AnalyzeSync with cancellation.
func (a *Analyzer) AnalyzeSyncContext(ctx context.Context, prompt string) (string, error) {
	var buf bytes.Buffer
	if err := a.AnalyzeContext(ctx, prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
```

Keep the old methods delegating to these with `context.Background()`. Then:
- `internal/mcpserver/server.go`: every handler that calls `analyzer.AnalyzeSync(...)`/`Analyze(...)` switches to the `...Context(ctx, ...)` variant with the handler's `ctx` (grep `AnalyzeSync(`/`Analyze(`/`AnalyzeWithHistory(` in the file).
- `internal/tui/tui.go`: the session's answer call switches to `AnalyzeWithHistoryContext` with the session's context (if the session has none, use `context.Background()` there for now and leave a note — the TUI ctx plumbing is a Tier 4 concern).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ai/ ./internal/mcpserver/ ./internal/tui/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ai/ internal/mcpserver/ internal/tui/
git commit -m "feat(ai): context-taking Analyzer variants; MCP/TUI propagate cancellation"
```

---

### Task 11: slo — honest latency SLOs

`checkLatency` reports a fake healthy "ok" (Current=100) when the trace query errors or returns zero traces, and its `BudgetConsumed` has the same unscaled-window conflation availability had before Tier 1.

**Files:**
- Modify: `internal/slo/slo.go` (`checkLatency`)
- Test: `internal/slo/slo_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestCheckLatencyQueryFailureIsNoData(t *testing.T) {
	mock := &mockSignozClient{ // adapt to this file's mock; make QueryTraces return an error
		queryTracesErr: fmt.Errorf("boom"),
	}
	c := NewChecker(mock, "test")
	s := SLO{Name: "lat", Type: "latency", Service: "api", Target: 99.0, Threshold: 500, Window: "24h"}

	result := c.checkLatency(context.Background(), s, nil)

	if result.Status != "no_data" {
		t.Errorf("failed trace query must be no_data, not fake-ok; got %q", result.Status)
	}
}

func TestCheckLatencyScalesConsumptionByWindow(t *testing.T) {
	// 1000 traces, 20 over threshold → 2% violation on a 1% budget = 2x burn.
	traces := make([]types.TraceEntry, 1000)
	for i := range traces {
		d := int64(100 * 1e6) // 100ms
		if i < 20 {
			d = int64(900 * 1e6) // 900ms, over the 500ms threshold
		}
		traces[i] = types.TraceEntry{DurationNano: d}
	}
	mock := &mockSignozClient{traces: traces} // adapt
	c := NewChecker(mock, "test")
	s := SLO{Name: "lat", Type: "latency", Service: "api", Target: 99.0, Threshold: 500, Window: "30d"}

	result := c.checkLatency(context.Background(), s, nil)

	// Observed window is min(1440, 43200) = 1440 of 43200 → fraction 1/30.
	// BudgetConsumed = 2.0 * (1/30) * 100 ≈ 6.7, so status stays ok (burn 2x < 6x).
	if result.BurnRate < 1.9 || result.BurnRate > 2.1 {
		t.Errorf("burn rate = %.2f, want ~2.0", result.BurnRate)
	}
	if result.BudgetConsumed > 10 {
		t.Errorf("consumed = %.1f, must be scaled by observed/window fraction", result.BudgetConsumed)
	}
	if result.Status != "ok" {
		t.Errorf("2x burn is below the 6x escalation bar; got %q", result.Status)
	}
}
```

Adapt the mock to the file's actual mock structure (add fields for a trace error / canned traces if missing). Note `statusPriority` must know `"no_data"` — check the function; map it to priority 0 (same as ok) but render distinctly.

- [ ] **Step 2: Verify failures.**

- [ ] **Step 3: Implement**

In `checkLatency`, replace the error/empty early-return:

```go
	traceResult, err := c.client.QueryTraces(ctx, service, dur, 1000)
	if err != nil || traceResult == nil || len(traceResult.Traces) == 0 {
		// No data is not "healthy" — a broken trace pipeline must not turn
		// latency SLOs green.
		result.Status = "no_data"
		result.ErrorBudget = 100.0 - slo.Target
		result.BudgetRemain = result.ErrorBudget
		return result
	}
```

and the budget block:

```go
	result.ErrorBudget = 100.0 - slo.Target
	violationRate := 100.0 - result.Current
	if result.ErrorBudget > 0 {
		result.BurnRate = violationRate / result.ErrorBudget
		windowMins := float64(slo.WindowMinutes())
		observed := math.Min(float64(dur), windowMins)
		result.BudgetConsumed = math.Min(100.0, result.BurnRate*(observed/windowMins)*100)
		result.BudgetRemain = math.Max(0, 100-result.BudgetConsumed)
	}

	result.Status = escalateForBurnRate(classifyStatus(result.BudgetConsumed), result.BurnRate)
```

Ensure `statusPriority` and the renderers handle `"no_data"` (render as a neutral/dim marker; priority 0). Update any pre-existing latency test whose expectation depended on the unscaled formula — pin `Window: "24h"` gives fraction 1 (dur is capped at 1440), so most keep their numbers; the "exhausted" test at slo_test.go:273-280 uses 24h → fraction 1440/1440 = 1 → unchanged.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/slo/ -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/slo/
git commit -m "fix(slo): latency SLOs report no_data instead of fake-ok; scale consumption; escalate on burn"
```

---

### Task 12: hardening batch — panics, traversal, ambiguity, TUI history

Six small, independent fixes. Each gets a focused test written first, in its package's existing test style.

**Files/Fixes:**

1. **`internal/alertmanager/render.go` (~line 157):** `s.ID[:8]` panics on short IDs. Replace with:

```go
	shortID := s.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
```

(and use `shortID` in the Sprintf). Test: render a silence with `ID: "abc"` — must not panic.

2. **`internal/grafana/client.go` (`GetDashboard`):** path-escape the UID: `"/api/dashboards/uid/"+url.PathEscape(uid)` (add `net/url` import). Test: UID `"a/b?c"` produces an escaped request path (capture with httptest).

3. **`internal/doctor/doctor.go` (~lines 476 and the anthropic equivalent ~570):** `key[len(key)-4:]` panics on keys shorter than 4. Add helper:

```go
// keySuffix returns the last 4 characters for display, or the whole key if shorter.
func keySuffix(key string) string {
	if len(key) <= 4 {
		return key
	}
	return key[len(key)-4:]
}
```

and use it at every `[len(key)-4:]` site (grep the file). Test: `keySuffix("abc") == "abc"`.

4. **`internal/tui/tui.go` (`trimHistory` + wherever `maxHistory` is set):** `--max-history 1` (or any odd value) disables trimming because `excess++` makes `excess == len` and the `excess < len` guard skips. Normalize at construction: minimum 2, round odd up to even:

```go
	if maxHistory < 2 {
		maxHistory = 2
	}
	if maxHistory%2 != 0 {
		maxHistory++
	}
```

Test: a session with `maxHistory: 1` and 6 history messages trims to ≤2 after `trimHistory` (construct via the session's real constructor so normalization applies).

5. **`internal/runbook/runbook.go` (`Store.Load` + `Delete`):** reject traversal and delete the resolved file:

```go
	if strings.ContainsAny(idOrPartial, "/\\") || strings.Contains(idOrPartial, "..") {
		return nil, fmt.Errorf("invalid runbook ID %q", idOrPartial)
	}
```

at the top of `Load`. For `Delete`, resolve the actual matched filename: factor the exact+partial matching in `Load` into `func (s *Store) resolve(idOrPartial string) (path string, err error)` returning the file path; `Load` reads the resolved path; `Delete` removes the resolved path (not `rb.ID+".yaml"`). Tests: `Load("../evil")` errors; a runbook file whose internal `id:` differs from its filename is deleted by filename.

6. **`internal/incident/incident.go` (`FindByID`):** ambiguous partial matches must error instead of resolving to the first hit. Change the signature used internally — keep `FindByID(id string) *Incident` for exact matches but route partial matching through:

```go
// FindByPartialID resolves a case-insensitive suffix/exact match. It returns
// an error listing candidates when the partial is ambiguous.
func (s *IncidentStore) FindByPartialID(partial string) (*Incident, error) {
	var matches []*Incident
	lower := strings.ToLower(partial)
	for i := range s.Incidents {
		id := strings.ToLower(s.Incidents[i].ID)
		if id == lower || strings.HasSuffix(id, lower) {
			matches = append(matches, &s.Incidents[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("incident %q not found", partial)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return nil, fmt.Errorf("ambiguous incident ID %q matches: %s", partial, strings.Join(ids, ", "))
	}
}
```

Update `FindByID`'s callers that rely on partial behavior (grep `FindByID(` in `cmd/argus/main.go` and `internal/postmortem`) to use `FindByPartialID` and handle the error. Test: two incidents `INC-20260701-001`/`INC-20260701-101`, lookup `"01"` errors listing both; lookup `"101"` resolves.

- [ ] **Steps: For each of the six — failing test → fix → package tests green. Then full suite.**

Run: `go test ./... && go vet ./...`
Expected: PASS.

- [ ] **Commit**

```bash
git add internal/alertmanager/ internal/grafana/ internal/doctor/ internal/tui/ internal/runbook/ internal/incident/ cmd/argus/
git commit -m "fix: hardening batch — panics, UID escaping, path traversal, ambiguous IDs, TUI history clamp"
```

---

### Task 13: UTF-8-safe truncation sweep

Byte-index truncation (`s[:300]+"..."`) splits multibyte runes, producing mojibake in terminal output and invalid UTF-8 in AI prompts.

**Files:**
- Create: `internal/textutil/textutil.go` + test
- Modify (swap sites): `internal/output/formatter.go` (~166, ~295), `internal/loki/render.go` (~92), `internal/prometheus/render.go` (~65, ~250), `internal/alertmanager/render.go` (~103), `internal/grafana/render.go` (~412), `internal/incident/incident.go` (~291), `internal/explain/explain.go` (~107, ~121), `internal/tui/tui.go` (~217), `internal/correlate/correlate.go` (~107 body truncation), `internal/postmortem/postmortem.go` (`truncateString`)

- [ ] **Step 1: Write the helper + test**

`internal/textutil/textutil.go`:

```go
// Package textutil provides small string helpers shared across renderers.
package textutil

import "unicode/utf8"

// Truncate shortens s to at most max runes, appending "..." when truncated.
// Truncation happens on rune boundaries so multibyte characters are never
// split into invalid UTF-8.
func Truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "..."
}
```

Test: ASCII passthrough, ASCII truncation, multibyte (`"héllo wörld"` and an emoji string) truncation produces valid UTF-8 (`utf8.ValidString`), `max <= 0` passthrough.

- [ ] **Step 2: Swap each listed site**

Each site currently looks like `if len(body) > 300 { body = body[:300] + "..." }` — replace with `body = textutil.Truncate(body, 300)` keeping each site's existing length. For `postmortem.truncateString`, keep the function but delegate its body to `textutil.Truncate`. Do not change any length constants. Grep for stragglers: `grep -rn '\[:[0-9]\+\] *+ *"\.\.\."' internal/ | grep -v _test` should return nothing when done.

- [ ] **Step 3: Run the full suite**

Run: `go test ./... && go vet ./...`
Expected: PASS (a render test asserting a byte-truncated string may need its expectation adjusted to rune-truncation — the new behavior is the correct one).

- [ ] **Step 4: Commit**

```bash
git add internal/
git commit -m "fix: rune-safe truncation everywhere via textutil.Truncate"
```

---

### Task 14: deterministic output ordering

Map iteration makes `status`, `dashboard`, and `doctor` print instances/checks in a different order every run; anomaly's non-stable sort reorders equal-severity items.

**Files:**
- Modify: `cmd/argus/main.go` (status + dashboard instance loops), `internal/doctor/doctor.go` (instance checks loop ~131), `internal/anomaly/anomaly.go` (~151)
- Test: `internal/doctor` and `internal/anomaly` package tests

Fix pattern for map loops:

```go
	keys := make([]string, 0, len(cfg.Instances))
	for k := range cfg.Instances {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		inst := cfg.Instances[key]
		// ... existing body ...
	}
```

For anomaly: `sort.Slice` → `sort.SliceStable` with a name tiebreaker when severities are equal (`if a.Severity == b.Severity { return a.Service < b.Service }` — adapt to actual field names).

- [ ] **Steps: failing/pinning test (doctor: two instances always render in lexical order; anomaly: equal-severity anomalies sort by service) → fix → `go test ./... && go vet ./...` → commit**

```bash
git add cmd/argus/main.go internal/doctor/ internal/anomaly/
git commit -m "fix: deterministic ordering for status/dashboard/doctor output and anomaly sorting"
```

---

### Task 15: Full verification sweep

- [ ] **Step 1:** `go test ./... && go vet ./... && make build` — all green.
- [ ] **Step 2:** `grep -rn '\[:[0-9]\+\] *+ *"\.\.\."' internal/ | grep -v _test` — empty.
- [ ] **Step 3:** `git status --short && git log --oneline feat/tier2-dead-features..HEAD` — clean tree, ~14 commits.

---

## Deferred (explicit non-goals — Tier 4/5)

- CLI consolidation, flag standardization, exit-code unification, main.go decomposition — Tier 4.
- CI on PRs, golangci config, gofmt repo sweep, README/CLAUDE.md sync, model default updates — Tier 5.
- watch newest-1000 sampling-bias comment; percentile helper consolidation (4 impls) — Tier 4 cleanup.
- Full historical windowing for forecast/deploy/timeline (beyond caveats) — future work once WindowedQuerier is proven.

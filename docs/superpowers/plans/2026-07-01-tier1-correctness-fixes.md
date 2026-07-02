# Tier 1 Correctness Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the seven confirmed correctness bugs that make Argus report false data: phantom log/trace entries from empty Signoz results, error-rate ×100 double-scaling (alert/explain/postmortem), the `log_errors` limit=1 dead rule, severity-casing query misses, the budget/SLO burn-rate-vs-consumption formula, the MCP `am_alerts all=true` inversion, and non-atomic YAML store writes.

**Architecture:** All fixes are surgical changes inside existing packages. One new tiny package (`internal/fsutil`) provides atomic file writes shared by the incident and postmortem stores. Every fix lands test-first, and each task also corrects the test fixtures that encoded the wrong contract (fractional error rates, ignored query limits) so the bug cannot silently return.

**Tech Stack:** Go 1.24, stdlib `testing` + `net/http/httptest` (no new dependencies).

## Global Constraints

- `types.Service.ErrorRate` is a **percentage** (0–100), set by `signoz.Client.ListServices` as `errors/calls*100` (internal/signoz/client.go:115). Every fix and every fixture must respect this unit.
- The real `signoz.Client.QueryLogs`/`QueryTraces` honor the `limit` parameter and return the **newest N** entries. Mocks must honor `limit` too.
- `ListServices` data always covers a fixed **6h window** (internal/signoz/client.go:84). Budget math must not pretend it covers the SLO window.
- Do not change exported function signatures — `SignozQuerier` is implemented by mocks in 10+ packages.
- Run `gofmt` on every file you touch. Commit after every task.
- Work on branch `fix/tier1-correctness` (created in Task 0).

---

### Task 0: Branch setup

**Files:** none

- [ ] **Step 1: Create the working branch**

```bash
cd /Users/lbarahona/Projects/argus
git checkout -b fix/tier1-correctness
```

- [ ] **Step 2: Verify clean baseline**

Run: `go test ./... > /dev/null && echo BASELINE-OK`
Expected: `BASELINE-OK`

---

### Task 1: `internal/fsutil` — atomic file writes

**Files:**
- Create: `internal/fsutil/fsutil.go`
- Test: `internal/fsutil/fsutil_test.go`

**Interfaces:**
- Produces: `fsutil.WriteFileAtomic(path string, data []byte, perm os.FileMode) error` — used by Task 11.

- [ ] **Step 1: Write the failing test**

Create `internal/fsutil/fsutil_test.go`:

```go
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicCreatesFileWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.yaml")

	if err := WriteFileAtomic(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}

func TestWriteFileAtomicOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestWriteFileAtomicLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.yaml")

	if err := WriteFileAtomic(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "store.yaml" {
		t.Errorf("expected only store.yaml in dir, got %v", entries)
	}
}

func TestWriteFileAtomicSetsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")

	if err := WriteFileAtomic(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fsutil/ -v`
Expected: FAIL — `undefined: WriteFileAtomic` (build error)

- [ ] **Step 3: Write the implementation**

Create `internal/fsutil/fsutil.go`:

```go
// Package fsutil provides small filesystem helpers shared across packages.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path via a temp file in the same directory
// followed by a rename, so a crash mid-write can never leave a truncated
// file behind. The rename is atomic on POSIX filesystems.
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
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fsutil/ -v`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/fsutil/
git commit -m "feat(fsutil): add WriteFileAtomic helper for crash-safe store writes"
```

---

### Task 2: Signoz client — no phantom entries from empty results

The "no data" response shape `{"data":{"result":[{"queryName":"A","list":null}]}}` currently falls through to the flat-records parser, which re-parses the result envelope itself as log/trace records — producing one phantom entry per query. Every consumer that counts entries (alert, top, guard, diff, scorecard, report) sees 1 error on healthy services.

**Files:**
- Modify: `internal/signoz/client.go:507-533` (`extractLogs`), `:599-610` (items branch of `parseTracesResponse`)
- Test: `internal/signoz/client_test.go`

**Interfaces:**
- Produces: unexported `isResultItemShape(items []queryRangeResultItem) bool`, used by both parsers.
- No signature changes.

- [ ] **Step 1: Write the failing tests**

Add to `internal/signoz/client_test.go` (imports needed: `context`, `fmt`, `net/http`, `net/http/httptest`, `testing`, plus `github.com/lbarahona/argus/pkg/types` — most already present; add any missing):

```go
func TestQueryLogsEmptyResultNoPhantomEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryLogs(context.Background(), "api", 60, 10, "ERROR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Logs) != 0 {
		t.Fatalf("expected 0 logs for empty result, got %d: %+v", len(result.Logs), result.Logs)
	}
}

func TestQueryTracesEmptyResultNoPhantomEntries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	result, err := client.QueryTraces(context.Background(), "api", 60, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 0 {
		t.Fatalf("expected 0 traces for empty result, got %d: %+v", len(result.Traces), result.Traces)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/signoz/ -run 'NoPhantomEntries' -v`
Expected: both FAIL with `expected 0 logs for empty result, got 1` / `expected 0 traces for empty result, got 1`

- [ ] **Step 3: Fix the parsers**

In `internal/signoz/client.go`, replace the whole `extractLogs` function with:

```go
func extractLogs(data []byte) ([]types.LogEntry, error) {
	// Try as array of result items with "list" field
	var items []queryRangeResultItem
	if err := json.Unmarshal(data, &items); err == nil && isResultItemShape(items) {
		var logs []types.LogEntry
		for _, item := range items {
			for _, record := range item.List {
				logs = append(logs, mapToLogEntry(record))
			}
		}
		return logs, nil
	}

	// Try as flat array of records
	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err == nil {
		var logs []types.LogEntry
		for _, r := range records {
			logs = append(logs, mapToLogEntry(r))
		}
		return logs, nil
	}

	return nil, nil
}

// isResultItemShape reports whether the parsed array is a query-range result
// envelope ([{queryName, series, list}]) rather than a flat array of records.
// Flat records also unmarshal into queryRangeResultItem (unknown fields are
// ignored, all envelope fields stay zero), so the envelope fields being set
// is what distinguishes the two shapes. An envelope with an empty/null list
// must yield zero entries, not fall through to the flat-record parser.
func isResultItemShape(items []queryRangeResultItem) bool {
	for _, it := range items {
		if it.QueryName != "" || it.Series != nil || it.List != nil {
			return true
		}
	}
	return false
}
```

In `parseTracesResponse`, replace the items branch (currently `if err := json.Unmarshal(resultBytes, &items); err == nil && len(items) > 0 { ... if len(traces) > 0 { return traces, nil } }`) with:

```go
	// Try as array of result items with "list"
	var items []queryRangeResultItem
	if err := json.Unmarshal(resultBytes, &items); err == nil && isResultItemShape(items) {
		var traces []types.TraceEntry
		for _, item := range items {
			for _, record := range item.List {
				traces = append(traces, mapToTraceEntry(record))
			}
		}
		return traces, nil
	}
```

- [ ] **Step 4: Run the full signoz package tests**

Run: `go test ./internal/signoz/ -v`
Expected: PASS including both new tests and all pre-existing ones (the non-empty-list shapes still parse via `isResultItemShape` returning true).

- [ ] **Step 5: Run the full suite (entry-count consumers)**

Run: `go test ./...`
Expected: PASS. If any package fails, its fixture fabricated the fall-through shape — fix the fixture to a real Signoz shape, not the parser.

- [ ] **Step 6: Commit**

```bash
git add internal/signoz/
git commit -m "fix(signoz): stop parsing empty result envelopes as phantom log/trace entries"
```

---

### Task 3: Signoz client — severity filter matches any casing

The filter is an exact `severity_text = <value>` match. Callers are split between `"error"` and `"ERROR"`, so half the features return zero rows depending on how the deployment cases its levels. Fix once in the client with an `in` filter over casing variants.

**Files:**
- Modify: `internal/signoz/client.go:353-359` (severity filter in `QueryLogs`), add helper + `strings` import
- Test: `internal/signoz/client_test.go`

**Interfaces:**
- Produces: unexported `severityCasings(s string) []string`.
- Callers keep passing `"error"` or `"ERROR"` — both now work identically.

- [ ] **Step 1: Write the failing tests**

Add to `internal/signoz/client_test.go` (needs `encoding/json` and `io` imports if not present):

```go
func TestSeverityCasings(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"error", []string{"ERROR", "error", "Error"}},
		{"ERROR", []string{"ERROR", "error", "Error"}},
		{"Error", []string{"ERROR", "error", "Error"}},
		{"warn", []string{"WARN", "warn", "Warn"}},
	}
	for _, tt := range tests {
		got := severityCasings(tt.in)
		if len(got) != len(tt.want) {
			t.Errorf("severityCasings(%q) = %v, want %v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("severityCasings(%q) = %v, want %v", tt.in, got, tt.want)
				break
			}
		}
	}
}

func TestQueryLogsSeverityFilterMatchesAnyCasing(t *testing.T) {
	var payload QueryRangePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"success","data":{"result":[{"queryName":"A","list":null}]}}`)
	}))
	defer server.Close()

	client := New(types.Instance{URL: server.URL})
	if _, err := client.QueryLogs(context.Background(), "", 60, 10, "error"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items := payload.CompositeQuery.BuilderQueries["A"].Filters.Items
	if len(items) != 1 {
		t.Fatalf("expected 1 filter item, got %d", len(items))
	}
	if items[0].Op != "in" {
		t.Errorf("severity filter op = %q, want \"in\"", items[0].Op)
	}
	values, ok := items[0].Value.([]interface{})
	if !ok {
		t.Fatalf("severity filter value is %T, want JSON array", items[0].Value)
	}
	want := map[string]bool{"ERROR": true, "error": true, "Error": true}
	if len(values) != len(want) {
		t.Fatalf("expected %d casing variants, got %v", len(want), values)
	}
	for _, v := range values {
		if s, _ := v.(string); !want[s] {
			t.Errorf("unexpected casing variant %v", v)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/signoz/ -run 'Severity' -v`
Expected: FAIL — `undefined: severityCasings` (build error)

- [ ] **Step 3: Implement**

In `internal/signoz/client.go`, add `"strings"` to the imports, replace the severity filter block in `QueryLogs`:

```go
	if severityFilter != "" {
		filters = append(filters, FilterItem{
			Key:   FilterKey{Key: "severity_text", DataType: "string", Type: "tag", IsColumn: false},
			Op:    "in",
			Value: severityCasings(severityFilter),
		})
	}
```

and add the helper (below `QueryLogs` is fine):

```go
// severityCasings returns the casing variants of a severity level commonly
// stored in severity_text (e.g. ERROR, error, Error). The backend filter is
// an exact match, and deployments differ in how they case their levels, so
// QueryLogs matches all variants via an "in" filter.
func severityCasings(s string) []string {
	lower := strings.ToLower(s)
	upper := strings.ToUpper(s)
	title := lower
	if len(lower) > 0 {
		title = strings.ToUpper(lower[:1]) + lower[1:]
	}
	variants := []string{upper}
	if lower != upper {
		variants = append(variants, lower)
	}
	if title != upper && title != lower {
		variants = append(variants, title)
	}
	return variants
}
```

- [ ] **Step 4: Run signoz tests**

Run: `go test ./internal/signoz/ -v`
Expected: PASS. If a pre-existing test asserts the old `Op: "="` severity filter payload, update that assertion to the new `in` + variants contract (the old contract is the bug).

- [ ] **Step 5: Run the full suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/signoz/
git commit -m "fix(signoz): match severity_text case-insensitively via in-filter"
```

---

### Task 4: alert — error_rate rules stop double-scaling percentages

`checkErrorRate` multiplies `svc.ErrorRate` (already a percentage) by 100: a 1% service evaluates as 100% and fires critical.

**Files:**
- Modify: `internal/alert/alert.go:322`
- Test: `internal/alert/alert_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/alert/alert_test.go`:

```go
func TestCheckErrorRateUsesPercentageAsIs(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			// ErrorRate is a percentage, exactly as signoz.Client.ListServices reports it.
			return []types.Service{
				{Name: "api", NumCalls: 1000, NumErrors: 10, ErrorRate: 1.0},
			}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "errors", Type: "error_rate", Operator: "gt", Warning: 5.0, Critical: 15.0},
		},
	}

	rpt, err := checker.CheckAll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityOK {
		t.Errorf("1%% error rate with 5%%/15%% thresholds should be OK, got %v (value %.2f)",
			rpt.Results[0].Severity, rpt.Results[0].Value)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/alert/ -run TestCheckErrorRateUsesPercentageAsIs -v`
Expected: FAIL — `1% error rate ... should be OK, got critical (value 100.00)`

- [ ] **Step 3: Fix**

In `internal/alert/alert.go` `checkErrorRate`, replace:

```go
		rate := svc.ErrorRate * 100 // Convert to percentage
		if svc.NumCalls > 0 && svc.ErrorRate == 0 {
			rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}
```

with:

```go
		rate := svc.ErrorRate // ListServices already reports a percentage
		if svc.NumCalls > 0 && rate == 0 {
			rate = float64(svc.NumErrors) / float64(svc.NumCalls) * 100
		}
```

- [ ] **Step 4: Run alert tests**

Run: `go test ./internal/alert/ -v`
Expected: PASS — the pre-existing error_rate tests leave `ErrorRate` unset and exercise the fallback, so they keep passing.

- [ ] **Step 5: Commit**

```bash
git add internal/alert/
git commit -m "fix(alert): ErrorRate is already a percentage, stop multiplying by 100"
```

---

### Task 5: alert — log_errors rules query enough logs to evaluate thresholds

`checkLogErrors` queries with `limit=1`, capping the count at 1 against thresholds of 10/50 — the rule type can never fire. Also correct the existing mock so it honors `limit` like the real client.

**Files:**
- Modify: `internal/alert/alert.go:375-380`
- Modify: `internal/alert/alert_test.go` (existing `queryLogsFunc` fixtures that ignore `limit`)
- Test: `internal/alert/alert_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/alert/alert_test.go`:

```go
func TestCheckLogErrorsCountsAboveOne(t *testing.T) {
	mock := &mockSignozClient{
		listServicesFunc: func(ctx context.Context) ([]types.Service, error) {
			return []types.Service{{Name: "api", NumCalls: 100}}, nil
		},
		queryLogsFunc: func(ctx context.Context, service string, durationMinutes, limit int, severityFilter string) (*types.QueryResult, error) {
			// Honor the limit exactly like the real client does.
			available := 60
			n := available
			if limit < n {
				n = limit
			}
			logs := make([]types.LogEntry, n)
			for i := range logs {
				logs[i] = types.LogEntry{Body: "boom", SeverityText: "ERROR"}
			}
			return &types.QueryResult{Logs: logs}, nil
		},
	}

	checker := NewChecker(mock, "test")
	cfg := &AlertConfig{
		Rules: []Rule{
			{Name: "log-errors", Type: "log_errors", Operator: "gt", Warning: 10, Critical: 50},
		},
	}

	rpt, err := checker.CheckAll(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rpt.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(rpt.Results))
	}
	if rpt.Results[0].Severity != SeverityCritical {
		t.Errorf("60 error logs with critical threshold 50 should be critical, got %v (count %.0f)",
			rpt.Results[0].Severity, rpt.Results[0].Value)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/alert/ -run TestCheckLogErrorsCountsAboveOne -v`
Expected: FAIL — count is 1, severity ok.

- [ ] **Step 3: Fix**

In `internal/alert/alert.go`, add a package-level constant near the top of the file (after the existing consts/types):

```go
// logErrorsQueryLimit bounds how many error logs a log_errors rule fetches.
// It must exceed any realistic rule threshold; counts are capped at this
// value, so thresholds above it can never fire.
const logErrorsQueryLimit = 1000
```

and in `checkLogErrors` replace:

```go
		result, err := ch.client.QueryLogs(ctx, svc.Name, duration, 1, "error")
```

with:

```go
		result, err := ch.client.QueryLogs(ctx, svc.Name, duration, logErrorsQueryLimit, "error")
```

(The `"error"` casing is fine — Task 3 made the client casing-insensitive.)

- [ ] **Step 4: Make the existing mock honor limit**

In `internal/alert/alert_test.go`, find the pre-existing log_errors test whose `queryLogsFunc` returns a fixed number of logs regardless of `limit` (around line 232). Change it to cap at `limit` using the same pattern as Step 1 (`if limit < n { n = limit }`). Its expectations stay valid because the new query limit (1000) exceeds the fixture count.

- [ ] **Step 5: Run alert tests**

Run: `go test ./internal/alert/ -v`
Expected: PASS (all).

- [ ] **Step 6: Commit**

```bash
git add internal/alert/
git commit -m "fix(alert): log_errors rules fetched only 1 log, making thresholds unreachable"
```

---

### Task 6: budget — separate burn rate from budget consumed; gate exhaustion prediction

`calculateOverallBudget` sets `BudgetConsumed = errorRate/TotalBudget*100`, which is the burn rate ×100 — it ignores that the observation covers only 6h of the SLO window. A sustainable 0.9× burn on a 30d SLO reports 90% consumed → "critical" and a bogus exhaustion ETA. `predictExhaustion` must also not fire for burn ≤ 1.0 (the rolling window replenishes at that pace).

**Files:**
- Modify: `internal/budget/budget.go:174-212` (`calculateOverallBudget`), `:255-282` (`predictExhaustion`)
- Modify: `internal/budget/budget_test.go` (fixtures that encode the burn-rate-as-consumption model)
- Test: `internal/budget/budget_test.go`

**Interfaces:**
- Produces: package const `observedWindowMins = 360.0` (documented 6h ListServices window).
- `BudgetReport` fields unchanged; only their values change meaning to "consumed during the observed window as a share of the full-window budget".

- [ ] **Step 1: Write the failing tests**

Add to `internal/budget/budget_test.go`:

```go
func TestCalculateOverallBudgetSubUnitBurnIsHealthy(t *testing.T) {
	a := &Analyzer{}
	br := BudgetReport{TotalBudget: 0.1}
	s := slo.SLO{Service: "api", Target: 99.9, Window: "30d"}
	// 0.09% error rate over the observed 6h = 0.9x burn: sustainable forever.
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}

	a.calculateOverallBudget(&br, s, services)

	if br.Status != "healthy" {
		t.Errorf("0.9x burn on a 30d window should be healthy, got %q (consumed %.2f%%)",
			br.Status, br.BudgetConsumed)
	}
	if br.BudgetConsumed > 1.0 {
		t.Errorf("6h at 0.9x burn should consume <1%% of a 30d budget, got %.2f%%", br.BudgetConsumed)
	}
}

func TestPredictExhaustionSubUnitBurnNeverExhausts(t *testing.T) {
	br := BudgetReport{
		SLO:             slo.SLO{Target: 99.9, Window: "30d"},
		TotalBudget:     0.1,
		BudgetConsumed:  50,
		BudgetRemaining: 50,
		BurnRate6h:      0.9,
	}
	predictExhaustion(&br)
	if br.ExhaustionIn != "" {
		t.Errorf("burn rate 0.9 must never predict exhaustion, got %q", br.ExhaustionIn)
	}
	if br.ExhaustionETA != nil {
		t.Errorf("burn rate 0.9 must not set an ETA, got %v", br.ExhaustionETA)
	}
}
```

If the `Analyzer` literal `&Analyzer{}` does not compile (unexported required fields), construct it the same way the surrounding tests in this file do.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/budget/ -run 'SubUnitBurn' -v`
Expected: both FAIL — status "critical"/consumed 90, and a non-empty `ExhaustionIn`.

- [ ] **Step 3: Fix the formula**

In `internal/budget/budget.go`, add near the top of the file:

```go
// observedWindowMins is the window the Signoz ListServices data covers (6h,
// fixed by the client). Budget consumption must be scaled by how much of
// the SLO window this observation actually represents.
const observedWindowMins = 360.0
```

In `calculateOverallBudget`, replace:

```go
	errorRate := float64(totalErrors) / float64(totalCalls) * 100
	br.CurrentAvail = 100.0 - errorRate

	if br.TotalBudget > 0 {
		br.BudgetConsumed = math.Min(100.0, (errorRate/br.TotalBudget)*100)
		br.BudgetRemaining = math.Max(0, 100.0-br.BudgetConsumed)
	}
```

with:

```go
	errorRate := float64(totalErrors) / float64(totalCalls) * 100
	br.CurrentAvail = 100.0 - errorRate

	if br.TotalBudget > 0 {
		burnRate := errorRate / br.TotalBudget
		windowMins := float64(s.WindowMinutes())
		observed := math.Min(observedWindowMins, windowMins)
		// Budget consumed during the observed window, as a share of the
		// full-window budget. A 1.0 burn rate consumes budget exactly at
		// window pace; only the observed fraction of the window is known.
		br.BudgetConsumed = math.Min(100.0, burnRate*(observed/windowMins)*100)
		br.BudgetRemaining = math.Max(0, 100.0-br.BudgetConsumed)
	}
```

In `predictExhaustion`, replace:

```go
	burnRate := br.BurnRate6h
	if burnRate <= 0 {
		return
	}
```

with:

```go
	burnRate := br.BurnRate6h
	if burnRate <= 1.0 {
		// At or below 1.0x the budget is consumed no faster than the rolling
		// SLO window replenishes it — it never exhausts.
		return
	}
```

- [ ] **Step 4: Update fixtures that encoded the old model**

Run: `go test ./internal/budget/ -v 2>&1 | grep -E '^(=== RUN|--- FAIL|FAIL|ok)'`

The `budget burning` / `budget critical` / similar subtests around `internal/budget/budget_test.go:257-275` construct `slo.SLO{Service: "api", Target: 99.9}` with no `Window`, expecting `0.06% error rate = 60% consumed`. Pin their window to the observed window so the numbers keep meaning what the test names say — add `Window: "6h"` to each such SLO literal, e.g.:

```go
		s := slo.SLO{Service: "api", Target: 99.9, Window: "6h"}
```

With `Window: "6h"`, `observed/windowMins = 1` and the existing numeric expectations (60% consumed → "burning", 90% → "critical") hold under the corrected formula. Do the same for any other failing fixture in this file whose expectation depends on consumed == burn×100. If a fixture asserts an exhaustion ETA with burn ≤ 1.0, update it to expect no prediction (that was the bug).

- [ ] **Step 5: Run budget tests**

Run: `go test ./internal/budget/ -v`
Expected: PASS (all).

- [ ] **Step 6: Commit**

```bash
git add internal/budget/
git commit -m "fix(budget): scale consumption by observed window; only predict exhaustion above 1.0x burn"
```

---

### Task 7: slo — same consumption fix for availability SLOs

`checkAvailability` shares the conflated formula: a 1.0× burn reports "exhausted" (exit 2) even though the budget lasts exactly the window.

**Files:**
- Modify: `internal/slo/slo.go:268-277` (`checkAvailability`)
- Modify: `internal/slo/slo_test.go` (availability fixtures using `Window: "24h"`)
- Test: `internal/slo/slo_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/slo/slo_test.go`:

```go
func TestCheckAvailabilitySubUnitBurnIsOK(t *testing.T) {
	c := &Checker{}
	s := SLO{Name: "avail", Type: "availability", Service: "api", Target: 99.9, Window: "30d"}
	// 0.09% error rate over the observed 6h = 0.9x burn.
	services := []types.Service{{Name: "api", NumCalls: 100000, NumErrors: 90}}

	result := c.checkAvailability(s, services)

	if result.Status != "ok" {
		t.Errorf("0.9x burn on a 30d window should be ok, got %q (consumed %.2f%%)",
			result.Status, result.BudgetConsumed)
	}
	if result.BurnRate < 0.85 || result.BurnRate > 0.95 {
		t.Errorf("expected burn rate ~0.9, got %.2f", result.BurnRate)
	}
}
```

If `&Checker{}` does not compile bare, construct it the way other tests in this file do (e.g. `NewChecker(nil, "test")` — `checkAvailability` never touches the client).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/slo/ -run TestCheckAvailabilitySubUnitBurnIsOK -v`
Expected: FAIL — consumed 90 → status "critical".

- [ ] **Step 3: Fix**

In `internal/slo/slo.go`, add near the top of the file (it already imports `math`):

```go
// observedWindowMins is the window the Signoz ListServices data covers (6h,
// fixed by the client).
const observedWindowMins = 360.0
```

In `checkAvailability`, replace:

```go
	result.ErrorBudget = 100.0 - slo.Target // e.g. 0.1% for 99.9%
	if result.ErrorBudget > 0 {
		result.BudgetConsumed = (errorRate / result.ErrorBudget) * 100
		result.BudgetRemain = math.Max(0, 100-result.BudgetConsumed)
		result.BurnRate = errorRate / result.ErrorBudget
	}
```

with:

```go
	result.ErrorBudget = 100.0 - slo.Target // e.g. 0.1% for 99.9%
	if result.ErrorBudget > 0 {
		result.BurnRate = errorRate / result.ErrorBudget
		windowMins := float64(slo.WindowMinutes())
		observed := math.Min(observedWindowMins, windowMins)
		// Consumption during the observed window as a share of the
		// full-window budget (see internal/budget for the same model).
		result.BudgetConsumed = math.Min(100.0, result.BurnRate*(observed/windowMins)*100)
		result.BudgetRemain = math.Max(0, 100-result.BudgetConsumed)
	}
```

- [ ] **Step 4: Update availability fixtures**

Run: `go test ./internal/slo/ -v 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`

Availability fixtures at `internal/slo/slo_test.go:133,164,186,212` use `Window: "24h"` (fraction 0.25 under the new formula). For each failing availability test, change the SLO literal to `Window: "6h"` so its named expectation (ok/warning/critical/exhausted at given error counts) still describes what the test produces. Do not touch the latency SLO tests — `checkLatency` is out of scope (Tier 2).

- [ ] **Step 5: Run slo tests**

Run: `go test ./internal/slo/ -v`
Expected: PASS (all).

- [ ] **Step 6: Commit**

```bash
git add internal/slo/
git commit -m "fix(slo): availability budget consumption now accounts for observed window"
```

---

### Task 8: explain — stop rescaling ErrorRate in AI prompts

`BuildPrompt` multiplies the percentage by 100: a 1.5% service is presented to the AI as 150%.

**Files:**
- Modify: `internal/explain/explain.go:95`
- Test: `internal/explain/explain_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/explain/explain_test.go` (add `"strings"` import if missing):

```go
func TestBuildPromptErrorRateIsNotRescaled(t *testing.T) {
	data := &CorrelatedData{
		Service:  "api",
		Instance: "prod",
		Services: []types.Service{
			{Name: "api", NumCalls: 1000, NumErrors: 15, ErrorRate: 1.5},
		},
	}

	prompt := BuildPrompt(data)

	if !strings.Contains(prompt, "(1.50%)") {
		t.Errorf("prompt should report 1.50%% for ErrorRate=1.5, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "150.00%") {
		t.Errorf("prompt must not multiply the percentage by 100 again:\n%s", prompt)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/explain/ -run TestBuildPromptErrorRateIsNotRescaled -v`
Expected: FAIL — prompt contains `(150.00%)`.

- [ ] **Step 3: Fix**

In `internal/explain/explain.go` `BuildPrompt`, replace:

```go
		rate := s.ErrorRate * 100
		if s.NumCalls > 0 && s.ErrorRate == 0 {
			rate = float64(s.NumErrors) / float64(s.NumCalls) * 100
		}
```

with:

```go
		rate := s.ErrorRate // already a percentage
		if s.NumCalls > 0 && rate == 0 {
			rate = float64(s.NumErrors) / float64(s.NumCalls) * 100
		}
```

- [ ] **Step 4: Run explain tests**

Run: `go test ./internal/explain/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/explain/
git commit -m "fix(explain): ErrorRate is already a percentage in AI prompt"
```

---

### Task 9: postmortem — percentage unit fixes across prompt, threshold, and renders

Six sites multiply the already-percentage values by 100, and the "high error rate" action-item threshold `> 0.1` (meant as 10%) fires at 0.1%. The test fixtures seed fractional rates (`ErrorRate: 0.03`) the real client never produces.

**Files:**
- Modify: `internal/postmortem/postmortem.go:508` (AI prompt), `:683` (threshold), `:689`, `:748`, `:792`, `:880`, `:914` (renders)
- Modify: `internal/postmortem/postmortem_test.go:199-200,221,399,411,555,558` (fixtures)
- Test: `internal/postmortem/postmortem_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/postmortem/postmortem_test.go`:

```go
func TestBuildAIPromptPeakErrorRateNotRescaled(t *testing.T) {
	pm := &Postmortem{
		Title:    "checkout outage",
		Severity: "major",
		Services: []string{"api"},
		Metrics: MetricsSummary{
			TotalErrors:   100,
			PeakErrorRate: 3.0, // percent, as ListServices reports it
		},
	}

	prompt := buildAIPrompt(pm)

	if !strings.Contains(prompt, "Peak error rate: 3.00%") {
		t.Errorf("prompt should report 3.00%% for PeakErrorRate=3.0, got:\n%s", prompt)
	}
}

func TestGenerateBasicActionItemsThresholdIsTenPercent(t *testing.T) {
	pm := &Postmortem{
		Metrics: MetricsSummary{PeakErrorRate: 3.0}, // 3% — below the 10% bar
	}
	items := generateBasicActionItems(pm)
	for _, it := range items {
		if strings.Contains(it.Title, "error rate threshold alerts") {
			t.Errorf("3%% peak error rate must not trigger the high-error-rate action item: %+v", it)
		}
	}
}
```

(If `generateBasicActionItems` returns a different item type/field than `.Title`, mirror the existing `TestGenerateBasicActionItems_HighErrorRate` test's access pattern.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/postmortem/ -run 'NotRescaled|ThresholdIsTenPercent' -v`
Expected: both FAIL — prompt says `300.00%`; the 3% fixture triggers the action item (3.0 > 0.1).

- [ ] **Step 3: Fix all six sites plus the threshold**

In `internal/postmortem/postmortem.go`:

- Line 508: `pm.Metrics.PeakErrorRate*100` → `pm.Metrics.PeakErrorRate`
- Line 683: `if pm.Metrics.PeakErrorRate > 0.1 {` → `if pm.Metrics.PeakErrorRate > 10 { // >10% peak error rate`
- Line 689: `pm.Metrics.PeakErrorRate*100` → `pm.Metrics.PeakErrorRate`
- Line 748: `pm.Metrics.PeakErrorRate*100,` → `pm.Metrics.PeakErrorRate,`
- Line 792: `sm.ErrorRate*100, sm.ErrorCount, sm.Calls,` → `sm.ErrorRate, sm.ErrorCount, sm.Calls,`
- Line 880: `pm.Metrics.PeakErrorRate*100` → `pm.Metrics.PeakErrorRate`
- Line 914: `sm.Service, sm.ErrorRate*100, sm.ErrorCount, sm.Calls))` → `sm.Service, sm.ErrorRate, sm.ErrorCount, sm.Calls))`

(Line numbers may drift by a few lines; the pattern `PeakErrorRate*100` / `ErrorRate*100` identifies every site — after this task `grep -n '\*100' internal/postmortem/postmortem.go` must return nothing.)

- [ ] **Step 4: Correct the fixtures to percent units**

In `internal/postmortem/postmortem_test.go`:

- Line 199: `ErrorRate: 0.03` → `ErrorRate: 3.0` (150/5000 = 3%)
- Line 200: `ErrorRate: 0.027` → `ErrorRate: 2.67` (80/3000 ≈ 2.67%)
- Line 221: `assert.InDelta(t, 0.03, pm.Metrics.PeakErrorRate, 0.001)` → `assert.InDelta(t, 3.0, pm.Metrics.PeakErrorRate, 0.001)`
- Line 399: `MetricsSummary{PeakErrorRate: 0.05}` → `MetricsSummary{PeakErrorRate: 5.0}` (5% — still below the 10% bar, expectation unchanged)
- Line 411 (`TestGenerateBasicActionItems_HighErrorRate`): `MetricsSummary{PeakErrorRate: 0.25}` → `MetricsSummary{PeakErrorRate: 25.0}` (25% — above the bar)
- Lines 555/558: `PeakErrorRate: 0.05` → `PeakErrorRate: 5.0`, `ErrorRate: 0.05` → `ErrorRate: 5.0` — any render assertion checking `5.00%` still passes because the render no longer rescales.

- [ ] **Step 5: Run postmortem tests and fix any remaining unit-dependent assertions**

Run: `go test ./internal/postmortem/ -v`
Expected: PASS. If a render test asserts a string like `"0.05%"`, update it to the percent-unit equivalent (`"5.00%"`) — the fixture, not the code, was wrong.

- [ ] **Step 6: Commit**

```bash
git add internal/postmortem/
git commit -m "fix(postmortem): treat error rates as percentages everywhere; high-error threshold is 10%"
```

---

### Task 10: MCP server — `argus_am_alerts` with `all=true` must keep active alerts

`active := !input.All` inverts the flag: asking for "all" drops every firing alert and returns only silenced/inhibited ones.

**Files:**
- Modify: `internal/mcpserver/server.go:807-814`
- Test: `internal/mcpserver/server_happypath_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/mcpserver/server_happypath_test.go` (add `"net/url"` import):

```go
func TestTool_AmAlerts_AllIncludesActive(t *testing.T) {
	signoz := httptest.NewServer(mockSignozHandler())
	defer signoz.Close()
	aiServer := httptest.NewServer(mockAIHandler())
	defer aiServer.Close()

	var gotQuery url.Values
	amServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer amServer.Close()
	promServer := httptest.NewServer(mockPrometheusHandler())
	defer promServer.Close()
	grafanaServer := httptest.NewServer(mockGrafanaHandler())
	defer grafanaServer.Close()

	withMockIntegrations(t, signoz.URL, aiServer.URL, amServer.URL, promServer.URL, grafanaServer.URL, func() {
		cs := newTestSession(t)
		result := callTool(t, cs, "argus_am_alerts", map[string]any{"all": true})
		if result.IsError {
			t.Fatalf("argus_am_alerts returned error: %s", textOf(t, result))
		}
	})

	if gotQuery.Get("active") != "true" {
		t.Errorf("all=true must keep active=true, got active=%q", gotQuery.Get("active"))
	}
	if gotQuery.Get("silenced") != "true" || gotQuery.Get("inhibited") != "true" {
		t.Errorf("all=true should include silenced and inhibited, got silenced=%q inhibited=%q",
			gotQuery.Get("silenced"), gotQuery.Get("inhibited"))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcpserver/ -run TestTool_AmAlerts_AllIncludesActive -v`
Expected: FAIL — `all=true must keep active=true, got active="false"`.

- [ ] **Step 3: Fix**

In `internal/mcpserver/server.go`, replace:

```go
		active := !input.All
		silenced := input.All
		inhibited := input.All
		if input.ActiveOnly {
			active = true
			silenced = false
			inhibited = false
		}
```

with:

```go
		// Active alerts are always included; "all" additionally includes
		// silenced and inhibited ones, "active_only" excludes them.
		active := true
		silenced := input.All
		inhibited := input.All
		if input.ActiveOnly {
			silenced = false
			inhibited = false
		}
```

- [ ] **Step 4: Run mcpserver tests**

Run: `go test ./internal/mcpserver/ -v`
Expected: PASS (all, including the pre-existing `active_only` happy-path test — its behavior is unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/
git commit -m "fix(mcp): am_alerts all=true no longer drops firing alerts"
```

---

### Task 11: incident/postmortem stores — atomic saves

`os.WriteFile` truncates before writing; a crash mid-write bricks `incidents.yaml`/`postmortems.yaml` for every future command. Swap both `Save` methods to `fsutil.WriteFileAtomic` (Task 1).

**Files:**
- Modify: `internal/incident/incident.go:96-105`
- Modify: `internal/postmortem/postmortem.go:161-171`
- Test: `internal/incident/incident_test.go`, `internal/postmortem/postmortem_test.go`

**Interfaces:**
- Consumes: `fsutil.WriteFileAtomic(path string, data []byte, perm os.FileMode) error` from Task 1.

- [ ] **Step 1: Write the tests**

Add to `internal/incident/incident_test.go` (follow the file's existing HOME-override helper — it sets `os.Setenv("HOME", tmpDir)` and returns a restore func; reuse it):

```go
func TestSaveIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	restore := setTestHome(t) // use the existing helper name in this file
	defer restore()

	store := &IncidentStore{}
	store.Create("db outage", "critical", []string{"api"}, "alice", "primary down")
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	home, _ := os.UserHomeDir()
	entries, err := os.ReadDir(filepath.Join(home, ".argus"))
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
	if len(loaded.Incidents) != 1 {
		t.Errorf("expected 1 incident after reload, got %d", len(loaded.Incidents))
	}
}
```

Adapt the helper call to the actual name in the file (line 17 area) and the `Create` signature if it differs — mirror an existing Save/Load roundtrip test. Add the equivalent test to `internal/postmortem/postmortem_test.go` for the postmortem store (same structure: save, assert no `.tmp-` files in `~/.argus`, reload).

- [ ] **Step 2: Run to verify current behavior passes the roundtrip (tests should pass pre-change)**

Run: `go test ./internal/incident/ ./internal/postmortem/ -run Atomic -v`
Expected: PASS — these tests pin behavior; the implementation change follows.

- [ ] **Step 3: Swap the writes**

In `internal/incident/incident.go`, add import `"github.com/lbarahona/argus/internal/fsutil"` and change `Save`:

```go
// Save writes the incident store to disk.
func (s *IncidentStore) Save() error {
	if err := os.MkdirAll(storeDir(), 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(storePath(), data, 0o644)
}
```

In `internal/postmortem/postmortem.go`, add the same import and change `Save`:

```go
// Save writes the postmortem store to disk.
func (s *PostmortemStore) Save() error {
	dir := storeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating store dir: %w", err)
	}
	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling postmortems: %w", err)
	}
	return fsutil.WriteFileAtomic(storePath(), data, 0o644)
}
```

- [ ] **Step 4: Run both packages' tests**

Run: `go test ./internal/incident/ ./internal/postmortem/ -v`
Expected: PASS (all).

- [ ] **Step 5: Commit**

```bash
git add internal/incident/ internal/postmortem/
git commit -m "fix(stores): write incidents/postmortems atomically via temp file + rename"
```

---

### Task 12: Full verification sweep

**Files:** none (verification only)

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: `ok` for all 32 packages, zero FAIL.

- [ ] **Step 2: Vet and format**

Run: `go vet ./... && gofmt -l . | grep -v '^$' ; echo "VET-FMT-DONE"`
Expected: `VET-FMT-DONE` with no file names listed by gofmt.

- [ ] **Step 3: Build the binary**

Run: `make build`
Expected: `bin/argus` builds without errors.

- [ ] **Step 4: Grep-audit that no double-scaling remains**

Run: `grep -rn 'ErrorRate \* 100\|ErrorRate\*100\|PeakErrorRate\*100\|PeakErrorRate \* 100' internal/ | grep -v _test`
Expected: no output. (`internal/tui/tui.go` and `internal/anomaly` were already correct and must not appear.)

- [ ] **Step 5: Commit anything outstanding and summarize**

```bash
git status --short
git log --oneline main..HEAD
```

Expected: clean tree; ~11 commits on `fix/tier1-correctness`.

---

## Deferred (explicit non-goals for this plan)

- Postmortem enrichment querying the incident's absolute time window (needs new `SignozQuerier` methods) — Tier 2.
- `checkLatency` SLO sampling and windowing — Tier 2.
- Runbook execution, deps trace strategy, metrics v3 parsing, watch P99, scorecard trends, Bedrock — Tier 2 plan.
- Exit-code standardization across alert/slo/budget/guard and all CLI flag work — Tier 4 plan.
- Call-site severity-casing cosmetics (`"error"` vs `"ERROR"`) — harmless after Task 3; normalize during Tier 4.

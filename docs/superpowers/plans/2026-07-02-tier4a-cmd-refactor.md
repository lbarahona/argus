# Tier 4A cmd/argus Structural Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decompose the 4,150-line `cmd/argus/main.go` into per-topic files and extract the four duplicated patterns (config→instance→client boilerplate ×24, render-format switches ×12+, `os.Exit` inside RunE ×6, AI-config checks ×2 sources of truth) so Tier 4B's command consolidation lands on a maintainable base. No command names, flags, or output formats change in this plan — the only user-visible changes are bug fixes already identified in review: swallowed instance-resolution errors now error loudly, unknown `--format` values now error instead of silently printing the default, `--query`/`--ai` without AI config now errors instead of silently ignoring the request, and usage help no longer dumps after runtime errors.

**Architecture:** Everything stays in `package main` under `cmd/argus/` — the split is file organization only, so no import or visibility changes. New shared helpers live in `cmd/argus/helpers.go`. Exit codes flow through a typed `exitError` handled once in `main()`.

**Tech Stack:** Go 1.24, cobra (existing), no new dependencies.

## Global Constraints

- The file split is MECHANICAL: function bodies move verbatim. Acceptance: `go build ./...` green, full test suite green, and no behavioral diff.
- Helper adoption must preserve each site's current error text where users may script against it, except the swallowed-error sites named in Task 2 (fixing those is the point).
- Command names, flag names, defaults, and output shapes are Tier 4B's business — do NOT change them here (two exceptions, explicitly scoped: format validation in Task 3 and AI-config errors in Task 7, both approved bug fixes).
- `cmd/argus` has existing tests (`am_test.go`, `correlate_stack_test.go`, `use_test.go`) — they must keep passing unmodified.
- Run `gofmt` on every new/touched file. Commit after every task.
- Work on branch `refactor/tier4a-cmd-structure`, stacked on `feat/tier3-robustness` (Task 0).

---

### Task 0: Branch setup

- [ ] **Step 1:**

```bash
cd /Users/lbarahona/Projects/argus
git checkout feat/tier3-robustness
git checkout -b refactor/tier4a-cmd-structure
go test ./... > /dev/null && echo BASELINE-OK
```

Expected: `BASELINE-OK`

---

### Task 1: Split main.go into per-topic files

**Files:**
- Create: `cmd/argus/config_cmds.go`, `cmd/argus/signoz_cmds.go`, `cmd/argus/analysis_cmds.go`, `cmd/argus/sre_cmds.go`, `cmd/argus/incident_cmds.go`, `cmd/argus/runbook_cmds.go`, `cmd/argus/postmortem_cmds.go`, `cmd/argus/misc_cmds.go`, `cmd/argus/loki_cmds.go`, `cmd/argus/am_cmds.go`, `cmd/argus/grafana_cmds.go`, `cmd/argus/prom_cmds.go`
- Modify: `cmd/argus/main.go` (shrinks to root wiring + version + shared helpers)

**Move map (function → destination file). Move each function VERBATIM (body untouched); assemble each file's import block to satisfy the compiler:**

| Destination | Functions |
|---|---|
| `config_cmds.go` | `configCmd`, `useCmd`, `instancesCmd` |
| `signoz_cmds.go` | `statusCmd`, `logsCmd`, `servicesCmd`, `tracesCmd`, `metricsCmd`, `dashboardCmd`, `askCmd`, `formatLogsForAI` |
| `analysis_cmds.go` | `reportCmd`, `topCmd`, `diffCmd`, `watchCmd`, `anomalyCmd`, `timelineCmd`, `correlateCmd`, `correlateStackCmd`, `scorecardCmd`, `forecastCmd`, `depsCmd`, `deployCmd`, `explainCmd` |
| `sre_cmds.go` | `alertCmd`, `sloCmd`, `budgetCmd`, `guardCmd` |
| `incident_cmds.go` | `incidentCmd` |
| `runbook_cmds.go` | `runbookCmd`, `validateRunbook` |
| `postmortem_cmds.go` | `postmortemCmd`, `postmortemGenerateCmd`, `postmortemListCmd`, `postmortemShowCmd`, `postmortemExportCmd`, `postmortemDeleteCmd` |
| `misc_cmds.go` | `tuiCmd`, `mcpCmd`, `doctorCmd` |
| `loki_cmds.go` | `getLokiClient`, `lokiCmd`, `lokiQueryCmd`, `lokiLabelsCmd`, `lokiLabelValuesCmd`, `lokiSeriesCmd`, `lokiStatsCmd`, `lokiStatusCmd`, `lokiSummaryCmd` |
| `am_cmds.go` | `getAMClient`, `amCmd`, `amAlertsCmd`, `amSilencesCmd`, `amSilenceCreateCmd`, `amSilenceDeleteCmd`, `amStatusCmd`, `amSummaryCmd`, `parseMatcher` |
| `grafana_cmds.go` | `getGrafanaClient`, `grafanaCmd`, `grafanaDashboardsCmd`, `grafanaDashboardGetCmd`, `grafanaSearchCmd`, `grafanaDatasourcesCmd`, `grafanaFoldersCmd`, `grafanaAlertsCmd`, `grafanaAlertInstancesCmd`, `grafanaStatusCmd`, `grafanaSummaryCmd` |
| `prom_cmds.go` | `getPromClient`, `promCmd`, `promRulesCmd`, `promTargetsCmd`, `promAlertsCmd`, `promQueryCmd`, `promStatusCmd`, `promSummaryCmd` |
| stays in `main.go` | `main`, `getAIProvider`, `hasAIConfig`, `versionCmd`, `jsonMarshal` |

- [ ] **Step 1: Move file-by-file, building after each**

For each destination file: create it with `package main`, cut the listed functions from `main.go` verbatim, run `goimports -w cmd/argus/` (or hand-assemble imports) and `go build ./...` before moving to the next file. Do not reorder statements inside any function.

- [ ] **Step 2: Verify no behavior change**

Run: `go build ./... && go test ./... && gofmt -l cmd/argus/`
Expected: build green, all packages pass (including the three existing `cmd/argus` test files unmodified), no gofmt output for the new files.

Sanity: `wc -l cmd/argus/*.go` — `main.go` should now be ~150 lines.

- [ ] **Step 3: Commit**

```bash
git add cmd/argus/
git commit -m "refactor(cmd): split main.go into per-topic command files (mechanical move)"
```

---

### Task 2: `newSignozContext` helper — one place for config→instance→client

The `config.Load()` → `config.GetInstance(cfg, instance)` → `signoz.New(*inst)` triple is duplicated ~24 times, and three sites swallow resolution errors (`ask` at old main.go:626 `inst, instKey, _ :=`; `dashboard` guarded with `if err == nil`; `postmortem generate` hard-codes `GetInstance(cfg, "")` and swallows). A typo'd `-i staging2` silently produces output with missing data.

**Files:**
- Create: `cmd/argus/helpers.go`
- Create: `cmd/argus/helpers_test.go`
- Modify: every command file from Task 1 that builds a Signoz client

**Interfaces:**

```go
// signozContext bundles what every Signoz-backed command needs.
type signozContext struct {
	cfg     *types.Config
	client  *signoz.Client
	instKey string
	inst    *types.Instance
}

// newSignozContext loads config, resolves the instance (explicit flag value
// or configured default), and builds the client. It is the single path from
// flags to a Signoz client; resolution failures are returned, never swallowed.
func newSignozContext(instanceFlag string) (*signozContext, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	inst, instKey, err := config.GetInstance(cfg, instanceFlag)
	if err != nil {
		return nil, err
	}
	return &signozContext{
		cfg:     cfg,
		client:  signoz.New(*inst),
		instKey: instKey,
		inst:    inst,
	}, nil
}
```

(Adapt the `config.GetInstance` return order/signature to the real one — check `internal/config`.)

- [ ] **Step 1: Write the helper + test**

Test (`helpers_test.go`, using the HOME-override pattern from `use_test.go`): with a config containing instances `prod` (default) and `staging`: `newSignozContext("")` resolves prod; `newSignozContext("staging")` resolves staging; `newSignozContext("nope")` returns an error mentioning the instance name; with NO config file, the error is non-nil (and after Task 6 will point to `argus config init`).

- [ ] **Step 2: Adopt at every site**

Run: `grep -n 'config.Load()' cmd/argus/*.go` — every hit inside a `RunE` that subsequently calls `config.GetInstance` + `signoz.New` becomes:

```go
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
```

with `sctx.client`, `sctx.cfg`, `sctx.instKey` replacing the old locals. THE THREE SWALLOWED-ERROR SITES CHANGE BEHAVIOR (deliberately):
- `askCmd`: `inst, instKey, _ :=` → hard error on bad `-i`.
- `dashboardCmd`: the `if err == nil { ... }` guard around services/logs enrichment keeps its degrade-gracefully behavior for QUERY failures, but instance RESOLUTION failure now returns the error.
- `postmortemGenerateCmd`: gains an `--instance/-i` flag (registered like every other command's) instead of the hard-coded default; resolution errors return instead of silently skipping enrichment. Config-load failure: keep enrichment optional (a postmortem without Signoz data is still valid) but PRINT a warning line instead of silence.

Sites that only need config (no Signoz client — e.g. am/grafana/prom/loki getters, config cmds) are out of scope.

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./... && grep -c 'newSignozContext(' cmd/argus/*.go | grep -v ':0'`
Expected: green; ~24 adoption sites. `grep -n 'GetInstance' cmd/argus/*.go` should show only `helpers.go` (plus `use`/`config` commands if they legitimately resolve differently).

- [ ] **Step 4: Commit**

```bash
git add cmd/argus/
git commit -m "refactor(cmd): single newSignozContext path; bad -i now errors instead of silently degrading"
```

---

### Task 3: `renderOutput` helper + validated formats; retire `correlate --markdown`

Unknown `--format` values silently print the default format today. Add one validated switch used everywhere the terminal/markdown/json pattern appears.

**Files:**
- Modify: `cmd/argus/helpers.go` (+test), all command files with render switches, `analysis_cmds.go` (correlate flag)

**Interfaces:**

```go
// renderOutput validates format and dispatches to the matching renderer.
// Pass nil for a renderer the command does not support; requesting an
// unsupported/unknown format is an error (not a silent default).
func renderOutput(format string, terminal func() error, markdown func() error, jsonValue any) error {
	switch format {
	case "", "terminal", "text", "table":
		if terminal == nil {
			return fmt.Errorf("terminal output not supported here")
		}
		return terminal()
	case "markdown", "md":
		if markdown == nil {
			return fmt.Errorf("markdown output is not supported by this command")
		}
		return markdown()
	case "json":
		if jsonValue == nil {
			return fmt.Errorf("json output is not supported by this command")
		}
		data, err := jsonMarshal(jsonValue)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	default:
		return fmt.Errorf("unknown format %q (valid: terminal, markdown, json)", format)
	}
}
```

- [ ] **Step 1: Helper + table test** (valid dispatches; nil-renderer errors; unknown format errors; `"text"`/`"table"`/`"md"` accepted as synonyms so no existing flag default breaks).

- [ ] **Step 2: Adopt at every render switch**

`grep -n 'case "markdown"\|case "json"\|== "markdown"\|== "json"' cmd/argus/*.go` — convert each site to `renderOutput(format, func() error { ... existing terminal path ...; return nil }, func() error { ... }, jsonV)`. Where a command previously supported only two of the three formats, pass nil for the third — its error message now says so explicitly. Keep each command's flag default string unchanged (`terminal`, `text`, or `table` — all synonyms now).

`correlateCmd`: replace the `--markdown` bool with `--format/-f` (default `terminal`) matching its own `correlate stack` subcommand. This is the one flag change, pre-approved.

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./...` plus a behavior spot-check: `go run ./cmd/argus report -f bogus; echo "exit=$?"` → prints the unknown-format error, exit 1.

- [ ] **Step 4: Commit**

```bash
git add cmd/argus/
git commit -m "refactor(cmd): shared renderOutput with validated formats; correlate uses --format"
```

---

### Task 4: typed exit codes — no `os.Exit` inside RunE

`alert check`, `slo check`, `budget check`, `guard`, `doctor`, and `runbook run` call `os.Exit` inside RunE, bypassing deferred cleanup and making them untestable.

**Files:**
- Modify: `cmd/argus/main.go` (exitError type + handling in `main()`), `sre_cmds.go`, `misc_cmds.go`, `runbook_cmds.go`

**Interfaces:**

```go
// exitError carries a process exit code through RunE without os.Exit,
// so deferred cleanup runs and commands stay testable.
type exitError struct{ code int }

func (e exitError) Error() string { return "" }

// exitCodeFor maps a RunE error to the process exit code.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}
```

In `main()`, replace the current error handling around `rootCmd.Execute()` with:

```go
	if err := rootCmd.Execute(); err != nil {
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, output.ErrorStyle.Render("Error: "+msg))
		}
		os.Exit(exitCodeFor(err))
	}
```

(Match the existing error-print styling in main() — adapt rather than invent.)

- [ ] **Step 1: Write the mapping test** (`exitCodeFor(nil)==0`, plain error → 1, `exitError{2}` → 2, wrapped exitError → 2).

- [ ] **Step 2: Convert the six sites**

Each `os.Exit(n)` inside a RunE (grep `os.Exit` in cmd/argus/, excluding main()) becomes `return exitError{code: n}` — for the check commands that compute `rpt.ExitCode()`, `if code := rpt.ExitCode(); code != 0 { return exitError{code: code} }` after rendering. Exit-code VALUES do not change in this plan (unification is Tier 4B).

- [ ] **Step 3: Verify**

Run: `go build ./... && go test ./...` and `grep -rn 'os.Exit' cmd/argus/ | grep -v main.go` → empty. Behavior check: `go run ./cmd/argus alert check --help; echo $?` → 0.

- [ ] **Step 4: Commit**

```bash
git add cmd/argus/
git commit -m "refactor(cmd): route exit codes through exitError instead of os.Exit in RunE"
```

---

### Task 5: delete dead plumbing — unused Options fields, duplicated watch defaults

**Files:**
- Modify: `internal/report/report.go`, `internal/scorecard/scorecard.go`, `internal/forecast/forecast.go`, `internal/timeline/timeline.go`, `internal/deploy/deploy.go`, `internal/deps/deps.go` (delete the write-only `Format` fields; `deps.Options.Writer` too), and their populate sites in `cmd/argus/`
- Modify: `analysis_cmds.go` (`watchCmd`): the five flag defaults duplicate `watch.DefaultThresholds()` and the `cmd.Flags().Changed(...)` guards are therefore redundant — initialize the flag defaults FROM `watch.DefaultThresholds()` and pass values directly.

- [ ] **Step 1: Verify the fields are truly write-only** — `grep -rn '\.Format' internal/report/ internal/scorecard/ internal/forecast/ internal/timeline/ internal/deploy/ internal/deps/ | grep -v _test | grep -v 'Options{'` and equivalent for `Writer`. Any field with a real read stays (report it in your notes instead of deleting).

- [ ] **Step 2: Delete fields + their assignment sites; simplify watchCmd**

```go
	defaults := watch.DefaultThresholds()
	cmd.Flags().Float64Var(&errWarn, "err-warn", defaults.ErrorRateWarning, "...")
	// ... same for the other four; delete the Changed() guards and assign directly.
```

(Keep each flag's help text.)

- [ ] **Step 3: Verify** — `go build ./... && go test ./...` green (compiler catches stragglers).

- [ ] **Step 4: Commit**

```bash
git add internal/ cmd/argus/
git commit -m "refactor: remove write-only Options fields and duplicated watch threshold defaults"
```

---

### Task 6: quieter failures, better first-run errors

**Files:**
- Modify: `cmd/argus/main.go` (root command), `internal/config/config.go` (`GetInstance` error), `prom_cmds.go` (wrong path in error)

- [ ] **Step 1: Three changes**

1. Root command gets `SilenceUsage: true` (runtime errors no longer dump the full flag list; `--help` still works). Keep `SilenceErrors` OFF unless main() already prints errors itself after Task 4 — if main() prints, set `SilenceErrors: true` to avoid double-printing (check what Task 4 left in main()).
2. `config.GetInstance`'s "no instance specified and no default set" error gains the pointer: `"no instance specified and no default set — run 'argus config init' to create a config"` (only when the config has zero instances; a populated config with no default keeps the current message).
3. `getPromClient`'s error says `~/.argus.yaml` — the real path is `~/.argus/config.yaml`. Fix the string; while there, make the four getter messages (loki/am/grafana/prom) consistent in phrasing: `"<name> is not configured — add '<key>.url' to ~/.argus/config.yaml"`.

- [ ] **Step 2: Tests** — config: zero-instance error mentions `config init` (add to `internal/config/config_test.go`); populated-no-default keeps old message. Getter messages: adjust any existing cmd tests that assert the old strings.

- [ ] **Step 3: Verify** — `go build ./... && go test ./...`; behavior: `HOME=$(mktemp -d) go run ./cmd/argus logs 2>&1 | head -2` → mentions `argus config init`, no usage dump.

- [ ] **Step 4: Commit**

```bash
git add cmd/argus/ internal/config/
git commit -m "fix(cmd): silence usage dumps; first-run errors point to config init; correct config path in prom error"
```

---

### Task 7: one source of truth for AI availability; no silent `--query`/`--ai` no-ops

`hasAIConfig` re-implements `ai.NewProvider`'s validation (two sources of truth), and commands disagree on what happens without AI config: `logs/traces/metrics --query` silently ignores the question; `report/scorecard/forecast/deploy/deps/anomaly --ai` silently skip; `timeline/correlate --ai` hard-error; `postmortem --ai` warns.

**Files:**
- Modify: `cmd/argus/main.go` (delete `hasAIConfig`; add `requireAI`), all `--ai`/`--query` sites

**Interfaces:**

```go
// requireAI returns a configured provider or a uniform, actionable error.
// Every command that was ASKED for AI (--ai flag or a --query argument)
// calls this — a user request for AI must never silently no-op.
func requireAI(cfg *types.Config) (ai.Provider, error) {
	provider, err := getAIProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("AI is not configured: %w — set ANTHROPIC_API_KEY or configure an ai provider in ~/.argus/config.yaml", err)
	}
	return provider, nil
}
```

- [ ] **Step 1: Convert every site**

- `grep -n 'hasAIConfig' cmd/argus/*.go` — each guard becomes: if the user requested AI (flag/query non-empty) → `provider, err := requireAI(sctx.cfg); if err != nil { return err }`; if AI is merely optional decoration and was NOT requested, no call at all.
- `logs/traces/metrics`: when `--query` is set and AI unavailable → return the error (never print raw output pretending the question was answered).
- `report/scorecard/forecast/deploy/deps/anomaly --ai`: same — flag set + no AI = error.
- `timeline/correlate`: switch their bespoke messages to `requireAI` for uniformity.
- `postmortem generate --ai`: keep generate-without-AI possible ONLY when the user didn't pass `--ai`; with `--ai` and no provider, error like everyone else.
- Delete `hasAIConfig`.

- [ ] **Step 2: Verify** — `go build ./... && go test ./...`; `grep -rn 'hasAIConfig' cmd/argus/` → empty. Behavior: `HOME=$(mktemp -d) go run ./cmd/argus report --ai 2>&1 | head -1` → the uniform AI error (after it fails on config, whichever comes first — instance resolution errors are fine too; verify with a valid config + no AI key if quick).

- [ ] **Step 3: Commit**

```bash
git add cmd/argus/
git commit -m "fix(cmd): uniform requireAI — requested AI never silently no-ops"
```

---

### Task 8: Full verification sweep

- [ ] **Step 1:** `go test ./... && go vet ./... && make build` — green.
- [ ] **Step 2:** `wc -l cmd/argus/*.go` — no file over ~900 lines; main.go ~200.
- [ ] **Step 3:** Greps: `os.Exit` only in main.go; `hasAIConfig` gone; `GetInstance` only in helpers/config/use; `--markdown` flag gone from correlate.
- [ ] **Step 4:** Smoke: `./bin/argus --help`, `./bin/argus report -f bogus` (validated error), `HOME=$(mktemp -d) ./bin/argus logs` (config-init pointer, no usage dump).
- [ ] **Step 5:** `git log --oneline feat/tier3-robustness..HEAD` — ~8 commits, clean tree.

---

## Deferred (Tier 4B)

Command consolidation (`analyze` group, top→services, scorecard→report, budget→slo, instances removal, alert→rules, loki `log` alias), flag standardization (-s/-q/-l meanings, human durations, exit-code value unification), shell completions, README/CHANGELOG. Tier 5: CI, lint config, gofmt sweep, docs sync, model defaults.

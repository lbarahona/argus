# Tier 4B CLI Consolidation Implementation Plan (v0.8.0 clean break)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The approved v0.8.0 clean break: consolidate the 35-command surface (~35 → ~20 top-level), standardize flags (`-s` = service everywhere, human durations, one `-q`/`-l` meaning, unified exit codes), add shell completions, and close the input-validation-before-work items from Tier 4A's review. Clean break = old names/flags are removed, not aliased.

**Architecture:** Command constructor functions from Tier 4A's split files are re-wired, not rewritten: the `analyze` group nests existing constructors; `services --sort` delegates to the existing `internal/top` package; `report --grade` delegates to `internal/scorecard`. Flag infrastructure (instance/format/duration registration with completion) lives in `cmd/argus/helpers.go` so every command registers flags the same way. Internal packages do not change except the budget exit-code values and an slo flag.

**Tech Stack:** Go 1.24, cobra (`RegisterFlagCompletionFunc`, `ValidArgsFunction`), pflag custom `Value`.

## Global Constraints

- CLEAN BREAK, pre-approved: removed commands/flags produce cobra's normal unknown-command/flag errors. No hidden aliases except where a task says so.
- The MCP server surface (`argus_top`, `argus_dashboard`, etc.) is UNCHANGED — it calls internal packages directly and is not part of the CLI break.
- Internal analysis packages keep their APIs; only `cmd/argus` wiring, `internal/budget` exit values, and one `internal/slo` addition change.
- Existing cmd tests: `use_test.go` must pass unmodified; `am_test.go` unmodified; `correlate_stack_test.go` may need its command-construction path updated if correlate moves (constructor names stay).
- Every task ends with `go build ./... && go test ./...` green. Run `gofmt` on touched files. Commit per task.
- Work on branch `feat/tier4b-cli-consolidation`, stacked on `refactor/tier4a-cmd-structure` (Task 0).

---

### Task 0: Branch setup

- [ ] **Step 1:**

```bash
cd /Users/lbarahona/Projects/argus
git checkout refactor/tier4a-cmd-structure
git checkout -b feat/tier4b-cli-consolidation
go test ./... > /dev/null && echo BASELINE-OK
```

---

### Task 1: flag infrastructure — instance/format/duration helpers with completion

**Files:**
- Modify: `cmd/argus/helpers.go` (+`helpers_test.go`)

**Interfaces (later tasks consume these exact signatures):**

```go
// addInstanceFlag registers -i/--instance with completion from the config's
// instance names.
func addInstanceFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "instance", "i", "", "Signoz instance name (default: configured default)")
	_ = cmd.RegisterFlagCompletionFunc("instance", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(cfg.Instances))
		for name := range cfg.Instances {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	})
}

// validFormats matches renderOutput's accepted values.
var validFormats = []string{"terminal", "markdown", "json"}

// addFormatFlag registers -f/--format with completion. def preserves each
// command's existing default string.
func addFormatFlag(cmd *cobra.Command, target *string, def string) {
	cmd.Flags().StringVarP(target, "format", "f", def, "Output format: terminal, markdown, json")
	_ = cmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return validFormats, cobra.ShellCompDirectiveNoFileComp
	})
}

// validateFormat rejects unknown formats before any expensive work happens.
// renderOutput re-validates at render time; this is the eager gate.
func validateFormat(format string) error {
	switch format {
	case "", "terminal", "text", "table", "markdown", "md", "json":
		return nil
	default:
		return fmt.Errorf("unknown format %q (valid: terminal, markdown, json)", format)
	}
}

// minutesValue is a pflag.Value accepting bare minutes ("90") or Go-style
// durations ("90m", "2h", "1h30m"), stored as whole minutes.
type minutesValue int

func newMinutesValue(def int, p *int) *minutesValue {
	*p = def
	return (*minutesValue)(p)
}

func (m *minutesValue) String() string { return strconv.Itoa(int(*m)) }
func (m *minutesValue) Type() string   { return "duration" }
func (m *minutesValue) Set(s string) error {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return fmt.Errorf("duration must be positive")
		}
		*m = minutesValue(n)
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q (use minutes like 90, or 90m / 2h)", s)
	}
	if d < 0 {
		return fmt.Errorf("duration must be positive")
	}
	*m = minutesValue(int(d.Minutes()))
	return nil
}

// addDurationFlag registers -d/--duration accepting minutes or human strings.
func addDurationFlag(cmd *cobra.Command, target *int, def int, help string) {
	cmd.Flags().VarP(newMinutesValue(def, target), "duration", "d", help)
}
```

- [ ] **Step 1: Tests first** (`helpers_test.go`): `minutesValue.Set` table — "90"→90, "90m"→90, "2h"→120, "1h30m"→90, "junk"→error, "-5"→error, "-5m"→error; default preserved when unset. `validateFormat`: all synonyms pass, "bogus" fails. Completion funcs: registering on a throwaway command doesn't panic and format completion returns the 3 values.

- [ ] **Step 2: Implement** (code above; imports `sort`, `strconv`, `time`).

- [ ] **Step 3:** `go build ./... && go test ./...` — green (helpers not yet adopted).

- [ ] **Step 4: Commit** — `feat(cmd): flag infrastructure — instance/format completion, human-duration values`

---

### Task 2: adopt the flag helpers everywhere

**Files:** every `cmd/argus/*_cmds.go`

- [ ] **Step 1: Instance flags** — `grep -n '"instance", "i"' cmd/argus/*.go`: every site becomes `addInstanceFlag(cmd, &instance)`. Help-text wording may unify to the helper's.

- [ ] **Step 2: Format flags** — `grep -n '"format", "f"\|"format",' cmd/argus/*.go`: every site becomes `addFormatFlag(cmd, &format, "<existing default>")` (keep each default string; grafana/prom/postmortem sites that used `StringVar` without shorthand gain `-f` — approved standardization).

- [ ] **Step 3: Duration flags** — `grep -n '"duration", "d"' cmd/argus/*.go`: every int-minutes site becomes `addDurationFlag(cmd, &duration, <existing default>, "<existing help> (e.g. 90, 90m, 2h)")`. EXCLUDE `am silence-create` (its `-d` is a real Go duration string — leave untouched). EXCLUDE `incident create -d` (handled in Task 3).

- [ ] **Step 4:** `go build ./... && go test ./...`; spot-checks: `./bin/argus logs -d 2h --help` parses; `go run ./cmd/argus report -d 90m` accepted (fails later only on config/network if absent); `__complete` output for `--format` lists 3 values: `go run ./cmd/argus __complete report --format "" | head -4`.

- [ ] **Step 5: Commit** — `feat(cmd): human durations and completions on every instance/format/duration flag`

---

### Task 3: shorthand cleanup — `-s` means service, `-q` means query, `-l` means limit

Clean-break flag changes (old shorthands removed):

| Command | Old | New |
|---|---|---|
| `logs` | `-s` = severity | `--severity` long-only; `-s` NOT registered (logs takes positional service) |
| `anomaly` | `-s` = sensitivity, `--service` long-only, `-q` = quiet | `--sensitivity` long-only; `--service` gains `-s`; `--quiet` long-only |
| `incident create` | `-s` = severity, `-d` = description | `--severity`, `--description` long-only |
| `incident update` | `-a` = author | `--author` long-only (list keeps `-a` = all) |
| `postmortem list` | `-n` = limit | `-l/--limit` |
| `deploy`/`timeline`/`correlate`/`scorecard`/`forecast`/`deps`/`guard`/`budget` | `-s` = service (already) | unchanged — verify each still has it |

- [ ] **Step 1: Apply the table** (grep each command's flag registrations in its `*_cmds.go` file).

- [ ] **Step 2: Verify** — `go build ./... && go test ./...`; `./bin/argus logs --help | grep -c '\-s,'` → 0; `./bin/argus anomaly --help | grep -- '-s,'` → shows `--service`; `./bin/argus postmortem list --help | grep -- '-l,'` → limit.

- [ ] **Step 3: Commit** — `feat(cmd)!: standardize shorthands — -s is service, -q is query, -l is limit`

---

### Task 4: input validation before work

From Tier 4A's final review:

- [ ] **Step 1:** Eager `validateFormat(format)` as the FIRST statement of RunE in the expensive commands: `report`, `timeline`, `forecast`, `deploy`, `deps`, `budget check`, `guard`, `doctor`, `postmortem generate/show/export`. (Cheap listing commands may keep render-time-only validation.)
- [ ] **Step 2:** `logs`/`traces`/`metrics`: when `--query` is non-empty, call `requireAI(sctx.cfg)` BEFORE the Signoz fetch (keep the provider for use after the fetch).
- [ ] **Step 3:** `dashboard`... (removed in Task 6 — SKIP if Task 6 already landed; this plan orders Task 6 after, so implement: resolve the target instance before the all-instances health sweep). Since Task 6 deletes the command, implement Step 3 only if dashboard still exists at execution time — otherwise note it as obsolete.
- [ ] **Step 4:** `postmortem generate` zero-config: warn-and-continue (skip enrichment) when `--instance` was NOT passed AND the loaded config has zero instances; keep the hard error for an explicit `-i`. Test both paths in a cmd-level test if feasible, else in the report.
- [ ] **Step 5:** Tests/verification: `go test ./...`; `./bin/argus report -f bogus` errors instantly (no spinner). Commit — `fix(cmd): validate inputs before doing work`

---

### Task 5: `services --sort` absorbs `top`

- [ ] **Step 1:** `servicesCmd` gains `--sort <errors|rate|calls|name>` (default `name`) and `--limit/-l` (default 0 = all). When `--sort != name`, delegate to the existing `internal/top` package exactly as `topCmd` does today (same Options mapping); when `name`, keep the current services path.
- [ ] **Step 2:** Delete `topCmd` and its registration. `grep -rn 'topCmd' cmd/argus/` → only deletion.
- [ ] **Step 3:** Completion for `--sort` values via `RegisterFlagCompletionFunc`.
- [ ] **Step 4:** Verify: `./bin/argus services --help` shows sort/limit; `./bin/argus top` → unknown command; full suite green. Commit — `feat(cmd)!: fold top into services --sort`

---

### Task 6: `report --grade` absorbs `scorecard`; drop `dashboard`

- [ ] **Step 1:** `reportCmd` gains `--grade` bool and `--service/-s` string. With `--grade`, delegate to `internal/scorecard` exactly as `scorecardCmd` does today (Options mapping, AI flag reuse, renderers via renderOutput).
- [ ] **Step 2:** Delete `scorecardCmd` + registration; delete `dashboardCmd` + registration (`status` remains the health view; the MCP `argus_dashboard` tool is untouched).
- [ ] **Step 3:** Verify: `./bin/argus report --grade --help`; `./bin/argus scorecard` and `./bin/argus dashboard` → unknown command; suite green. Commit — `feat(cmd)!: report --grade replaces scorecard; drop dashboard command`

---

### Task 7: the `analyze` group

- [ ] **Step 1:** New parent in `analysis_cmds.go`:

```go
func analyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Investigate system behavior: anomalies, timelines, correlations, changes",
		Long: `Analysis commands that scan services, error logs, and traces:

  anomalies   z-score anomaly detection across services
  timeline    chronological incident-style event timeline
  correlate   cross-signal correlation and error propagation
  changes     deployment/change-point detection
  diff        error-count comparison between two time windows`,
	}
	cmd.AddCommand(anomalyCmd(), timelineCmd(), correlateCmd(), deployCmd(), diffCmd())
	return cmd
}
```

- [ ] **Step 2:** Rename each child's `Use` line: `anomaly`→`anomalies`, `deploy`→`changes` (constructor names unchanged; `timeline`/`correlate`/`diff` keep their names). Update each child's `Example` strings to the `argus analyze …` form.
- [ ] **Step 3:** Root registration: remove the five old top-level registrations, add `analyzeCmd()`.
- [ ] **Step 4:** `correlate stack` moves with correlate (nothing to do beyond the parent move) — update `correlate_stack_test.go` ONLY if it builds the command through the root (check first; it likely constructs `correlateStackCmd()` directly and needs nothing).
- [ ] **Step 5:** Verify: `./bin/argus analyze --help` lists 5 subcommands; `./bin/argus anomaly`/`deploy`/`timeline`/`correlate`/`diff` → unknown command; `./bin/argus analyze changes --help` works; suite green. Commit — `feat(cmd)!: group anomaly/timeline/correlate/deploy/diff under analyze`

---

### Task 8: `slo budget` + exit-code unification

- [ ] **Step 1:** Move budget under slo: in `sre_cmds.go`, `sloCmd()` gains `cmd.AddCommand(budgetCmd())` where `budgetCmd`'s `Use` becomes `budget` (it currently wraps a `check` subcommand — flatten: `slo budget` runs what `budget check` ran; keep flags). Remove the top-level budget registration.
- [ ] **Step 2:** Unify exit codes in `internal/budget` (`ExitCode()`): current mapping 1=critical/ticket, 2=exhausted/page → new mapping matching alert/slo: `warning-tier (burning/ticket/watch) = 1`, `critical-tier (critical/exhausted/page) = 2`. Update budget tests asserting the old values (they encoded the inconsistency).
- [ ] **Step 3:** `slo check` gains `--fail-on-no-data` (exit 1 when any result status is `no_data`) — closes the Tier 3 CI-gate gap. Test: report with only no_data results → exit code 1 with the flag, 0 without.
- [ ] **Step 4:** Verify: `./bin/argus slo budget --help`; `./bin/argus budget` → unknown command; budget/slo package tests green. Commit — `feat(cmd)!: budget lives under slo; unified exit codes; slo --fail-on-no-data`

---

### Task 9: renames and removals — `rules`, `instances`, loki alias

- [ ] **Step 1:** `alertCmd`'s `Use` → `rules` with Short "Local alert rules evaluated against Signoz" (subcommands init/list/check unchanged; `~/.argus/alerts.yaml` path unchanged). Root registration comment notes the rename.
- [ ] **Step 2:** Delete `instancesCmd` + registration (`use` with no args already lists instances — verify, it does).
- [ ] **Step 3:** Remove loki's `"log"` alias (one-word `Aliases` entry in `lokiCmd`).
- [ ] **Step 4:** Verify: `./bin/argus rules check --help` works; `./bin/argus alert`/`instances` → unknown command; `./bin/argus log` → unknown command while `./bin/argus loki` works. `use_test.go` still green unmodified. Commit — `feat(cmd)!: alert→rules; drop instances command and loki log alias`

---

### Task 10: ID completions

- [ ] **Step 1:** `ValidArgsFunction` on: `incident update/resolve/timeline` (incident IDs from the store), `runbook show/run/delete/validate` (runbook IDs), `postmortem show/export/delete` (postmortem IDs), `postmortem generate` (incident IDs). Pattern:

```go
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		store, err := incident.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids := make([]string, 0, len(store.Incidents))
		for _, inc := range store.Incidents {
			ids = append(ids, inc.ID)
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	}
```

(adapt per store API; runbook listing via its Store's list method — check the real name.)

- [ ] **Step 2:** Verify with `__complete`: `go run ./cmd/argus __complete incident resolve "" ` lists IDs when a store exists (manual check acceptable; HOME-isolated test optional). Commit — `feat(cmd): shell completion for incident/runbook/postmortem IDs`

---

### Task 11: help-text pass on the changed surface

- [ ] **Step 1:** Root `Long` updated: Argus connects to Signoz **plus Loki, Prometheus, Grafana, and Alertmanager** (mirrors README's accurate line). `mcp` command's stale 13-tool list replaced with a one-liner ("Exposes Argus observability tools over MCP — status, services, logs, traces, metrics, analysis, and the Alertmanager/Prometheus/Grafana integrations") — do NOT enumerate tools that will drift again.
- [ ] **Step 2:** `Example:` blocks added to the frequently used commands that lack them: `logs`, `services`, `traces`, `report`, `analyze` children already updated in Task 7 — add for `rules check`, `slo check`, `slo budget`.
- [ ] **Step 3:** Verify help renders (`--help` for root, analyze, rules, slo, services, report); suite green. Commit — `docs(cmd): accurate root/mcp help; examples for common commands`

---

### Task 12: verification sweep

- [ ] **Step 1:** `go test ./... && go vet ./... && make build` — green.
- [ ] **Step 2:** Removed-surface checks (all must fail as unknown): `top`, `scorecard`, `dashboard`, `anomaly`, `timeline`, `correlate`, `deploy`, `diff`, `budget`, `alert`, `instances`, `log`.
- [ ] **Step 3:** New-surface checks (all must show help): `analyze` (+5 children), `services --sort`, `report --grade`, `slo budget`, `rules`, `use`.
- [ ] **Step 4:** Count top-level commands: `./bin/argus --help | sed -n '/Available Commands/,/Flags:/p' | grep -c '^  [a-z]'` — expect ~20.
- [ ] **Step 5:** Ledger the command-map (old → new) for the Tier 5 CHANGELOG/README task.

---

## Deferred (Tier 5)

README rewrite for the new command tree, CHANGELOG v0.8.0 with the full breaking-change map, CI on PRs + Go version fix, `.golangci.yml`, repo-wide gofmt sweep, CLAUDE.md refresh, AI model default updates, remaining rune-unsafe `body[:N]` prompt sites, caveat-rendering unification, percentile consolidation.

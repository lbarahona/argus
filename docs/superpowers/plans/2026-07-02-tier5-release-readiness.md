# Tier 5 Release Readiness Implementation Plan (v0.8.0)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make v0.8.0 shippable: per-command format sets (the one composed-UX defect left), a small quality batch, repo-wide gofmt + CI on every push/PR, accurate docs (README, CHANGELOG, CLAUDE.md), and current AI model defaults.

**Architecture:** No new packages. `addFormatFlag` gains a supported-set parameter that drives help text, completion, and the eager gate from one place. Docs are rewritten from the binary's own `--help` output as the source of truth — never from memory.

**Tech Stack:** Go 1.24, GitHub Actions, golangci-lint.

## Global Constraints

- The gofmt sweep (Task 3) must be a SEPARATE commit containing formatting-only changes (reviewable with `git diff -w` ≈ empty), and it lands BEFORE the CI task so CI's gofmt gate starts green.
- Docs tasks use `./bin/argus <cmd> --help` as the source of truth for every documented flag/command — no documenting from memory.
- CHANGELOG must state the budget exit-code change in BOTH directions (critical 1→2 AND warn-tier 0→1) — it silently breaks CI gates.
- Run `go build ./... && go test ./...` at the end of every task. Commit per task.
- Work on branch `chore/tier5-release-readiness`, stacked on `feat/tier4b-cli-consolidation` (Task 0).

---

### Task 0: Branch setup

- [ ] **Step 1:**

```bash
cd /Users/lbarahona/Projects/argus
git checkout feat/tier4b-cli-consolidation
git checkout -b chore/tier5-release-readiness
go test ./... > /dev/null && echo BASELINE-OK
```

---

### Task 1: per-command format sets — help, completion, and eager gate from one source

Today every `--format` flag advertises `terminal, markdown, json` even where a command supports only some (7 renderOutput sites pass nil json; ~30 advertise markdown with a nil markdown renderer), so `report -f json` passes the eager gate, does all the work, then errors.

**Files:**
- Modify: `cmd/argus/helpers.go` (+test), every `addFormatFlag` call site (42), the eager `validateFormat` call sites

**Interfaces:**

```go
// formatSet names the render targets a command actually supports.
type formatSet struct {
	Markdown bool
	JSON     bool // terminal is always supported
}

func (fs formatSet) list() []string {
	out := []string{"terminal"}
	if fs.Markdown {
		out = append(out, "markdown")
	}
	if fs.JSON {
		out = append(out, "json")
	}
	return out
}

// validate rejects formats outside the set (synonyms text/table/md accepted).
func (fs formatSet) validate(format string) error {
	switch format {
	case "", "terminal", "text", "table":
		return nil
	case "markdown", "md":
		if fs.Markdown {
			return nil
		}
	case "json":
		if fs.JSON {
			return nil
		}
	default:
		return fmt.Errorf("unknown format %q (valid: %s)", format, strings.Join(fs.list(), ", "))
	}
	return fmt.Errorf("format %q is not supported by this command (valid: %s)", format, strings.Join(fs.list(), ", "))
}
```

`addFormatFlag(cmd *cobra.Command, target *string, def string, fs formatSet)` — help text becomes `"Output format: " + strings.Join(fs.list(), ", ")`, completion returns `fs.list()`.

- [ ] **Step 1: Tests first** — `formatSet.validate` table (in-set passes incl. synonyms, out-of-set says "not supported by this command", unknown says "unknown"); `list()` ordering; help text and completion reflect the set.

- [ ] **Step 2: Implement + classify all 42 sites**

Derive each site's true set from its `renderOutput(...)` call: markdown=true iff a non-nil markdown func is passed, JSON=true iff a non-nil jsonValue. Update the `addFormatFlag` call accordingly. Replace every eager `validateFormat(format)` call (Tier 4B Task 4 + the sre_cmds fix) with the set-aware `fs.validate(format)` — hold the set in a local var used for both the flag registration and the gate. Delete or keep the old `validateFormat` only if something still uses it (delete preferred).

- [ ] **Step 3: Verify** — `go build ./... && go test ./...`; behavior: `./bin/argus report -f json` errors INSTANTLY with "not supported ... (valid: terminal, markdown)"; `./bin/argus __complete report --format ""` lists only terminal+markdown; `./bin/argus incident list -f json` still works.

- [ ] **Step 4: Commit** — `fix(cmd): per-command format sets — help, completion, and eager validation match reality`

---

### Task 2: quality batch

- [ ] **Step 1: minutesValue sub-minute guard** (`cmd/argus/helpers.go`): durations `0 < d < 1m` currently truncate to 0 — reject with `"duration %q is less than a minute"`. `0`/`0m` stay allowed. Test cases added.
- [ ] **Step 2: analyze anomalies + analyze diff get Example blocks** (the only two analyze children without them); examples must parse (bogus-HOME check).
- [ ] **Step 3: requireAI message de-duplication** (`cmd/argus/helpers.go` or main.go): the wrapped provider error already carries remediation, producing "…Run: argus config init — set ANTHROPIC_API_KEY or configure…". Reword to a single remediation: `fmt.Errorf("AI is not configured (%v)", err)` style with ONE actionable suffix. Update any test asserting the old text.
- [ ] **Step 4: rune-safe sweep of the remaining `body[:N]` sites** (no-ellipsis truncations, from Tier 3's final review): `internal/mcpserver/server.go:~436`, `cmd/argus/signoz_cmds.go` (old main.go:660 site — grep `[:200]`/`[:300]`), `internal/anomaly/anomaly.go:~507`, `internal/deploy/deploy.go:~494`, `internal/report/report.go:~138`, `internal/timeline/timeline.go:~561`, `internal/guard/guard.go:~403`, `internal/scorecard/scorecard.go:~301`. Replace `s[:N]` with `textutil.Truncate(s, N)` ONLY where the string feeds prompts/keys (every listed site does); acceptance grep: `grep -rn 'Body\[:' internal/ cmd/ | grep -v _test` → empty (adjust the grep to the real patterns found).
- [ ] **Step 5:** `go build ./... && go test ./...`; commit — `fix: quality batch — sub-minute durations, analyze examples, AI error wording, rune-safe prompt truncation`

---

### Task 3: repo-wide gofmt sweep

- [ ] **Step 1:** `gofmt -l .` to list (~37 files of pre-existing drift), then `gofmt -w .` — formatting only.
- [ ] **Step 2:** Verify: `git diff -w --stat` shows ~nothing (whitespace-only); `go build ./... && go test ./...` green; `gofmt -l .` empty.
- [ ] **Step 3:** Commit — `style: repo-wide gofmt sweep`

---

### Task 4: CI on push/PR + golangci config + release workflow Go version

**Files:**
- Create: `.github/workflows/ci.yaml`, `.golangci.yml`
- Modify: `.github/workflows/release.yaml` (Go 1.23 → 1.24 — go.mod requires 1.24.0)

- [ ] **Step 1: ci.yaml**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - name: gofmt
        run: test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
      - name: vet
        run: go vet ./...
      - name: test
        run: go test ./...
      - name: build
        run: go build ./...
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

- [ ] **Step 2: .golangci.yml** — starter config: default linters plus `misspell`, `unconvert`, `unparam`; exclude generated/none; timeout 5m. Keep it minimal:

```yaml
run:
  timeout: 5m

linters:
  enable:
    - misspell
    - unconvert
    - unparam
```

If `golangci-lint` is installed locally, run it and fix trivial findings or add targeted excludes; if it is NOT installed, note that CI will be the first run and keep the config minimal (do not guess-fix).

- [ ] **Step 3:** release.yaml `go-version: "1.23"` → `"1.24"` (both jobs).
- [ ] **Step 4:** Sanity: `actionlint` if available, else YAML parse check (`python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/ci.yaml"))'`). Commit — `ci: test/lint on push and PR; fix Go version in release workflow`

---

### Task 5: AI default model refresh

**Files:** `internal/ai/provider.go`, `internal/ai/anthropic.go`, related tests

- [ ] **Step 1:** Update the Anthropic defaults: `defaultAnthropicModel` and `DefaultModels["anthropic"]` from `claude-sonnet-4-20250514` → `claude-sonnet-5` (current stable Sonnet alias). Leave the OpenAI and Bedrock defaults as-is (no authoritative newer ID to hand — do NOT guess), but add a comment on each noting the `ai.model` config key overrides them.
- [ ] **Step 2:** Update tests asserting the old model string (grep `claude-sonnet-4-20250514`).
- [ ] **Step 3:** `go build ./... && go test ./...`; commit — `feat(ai): default Anthropic model to claude-sonnet-5`

---

### Task 6: CLAUDE.md refresh

- [ ] **Step 1:** Rewrite the stale sections against the current tree: `internal/mcp` → `internal/mcpserver`; add missing packages (loki, prometheus, grafana, alertmanager, anomaly, doctor, fsutil, textutil); cmd/argus is now split into per-topic files (name them) with shared helpers (`newSignozContext`, `renderOutput`, `requireAI`, flag helpers, `exitError`); the command architecture section reflects the new tree (analyze group, rules, slo budget, services --sort, report --grade); AI section mentions the three providers + `WindowedQuerier`; exit-code conventions (0/1/2 unified, slo --fail-on-no-data); Patterns section updated (fsutil atomic writes, textutil truncation, format sets).
- [ ] **Step 2:** Keep it guidance-dense and accurate — verify every named symbol exists (`grep` each). Commit — `docs: refresh CLAUDE.md for the v0.8.0 architecture`

---

### Task 7: README rewrite for the new surface

- [ ] **Step 1:** Regenerate the commands documentation from `./bin/argus --help` + per-command `--help` (source of truth). Fix all known drift: removed commands (top/scorecard/dashboard/alert/instances/budget + the five analyze children as top-level), new forms (`analyze …`, `rules`, `slo budget`, `services --sort`, `report --grade`), flag corrections (no `loki label-values --start`; `am silence-create`/`silence-delete` and `prom summary` format-support claims match reality — check them), human-duration examples, completion mention (`argus completion bash|zsh|fish`), the previously undocumented commands (use, tui, runbook, forecast, deps, mcp, postmortem, doctor, watch thresholds).
- [ ] **Step 2:** Keep the existing README voice/structure (badges, install, quick start); it's a rewrite of the command sections, not a new document. Every command example must parse (bogus-HOME check for a sample).
- [ ] **Step 3:** Commit — `docs: README for the v0.8.0 command surface`

---

### Task 8: CHANGELOG v0.8.0

- [ ] **Step 1:** Prepend a `## [v0.8.0] - Unreleased` section with subsections:
  - **Breaking (CLI consolidation)** — the full old→new command map (from `.superpowers/sdd/progress.md`'s ledgered map): top→`services --sort`, scorecard→`report --grade`, dashboard→removed, anomaly/timeline/correlate/deploy/diff→`analyze` group (anomalies/changes renames), budget→`slo budget`, alert→`rules`, instances→removed, loki `log` alias removed; flag changes (logs `--severity` long-only; anomaly/incident/postmortem shorthand changes; `-f` added to grafana/prom/postmortem); **exit codes: budget critical 1→2 AND burning/watch 0→1 (CI gates must update); unknown `--format` now exits 1**.
  - **Added** — runbook `--execute` real execution + run logs; human durations; shell completions (flags + IDs); `slo check --fail-on-no-data`; loki metric-query (matrix/vector) support; `WindowedQuerier` historical windows (postmortem/diff); truncation caveats; AI provider hardening (stream errors, timeouts, proxy support, context cancellation); Bedrock working against real AWS.
  - **Fixed** — the Tier 1-3 highlights: phantom log entries, error-rate ×100 family, alert log_errors dead rule, severity-casing misses, budget/SLO burn-rate math, MCP am_alerts all=true, atomic YAML writes, metrics v3 parsing, deps edge discovery, watch P99, scorecard trends, postmortem incident-window + AI parsing, panics/traversal/ambiguity hardening, deterministic output.
- [ ] **Step 2:** Cross-check the breaking list against the ledger and `git log v0.7.0-ish..HEAD` — nothing user-facing missing. Commit — `docs: CHANGELOG for v0.8.0`

---

### Task 9: final verification sweep

- [ ] **Step 1:** `go test ./... && go vet ./... && make build && gofmt -l .` (empty).
- [ ] **Step 2:** README spot-audit: pick 10 documented commands/flags at random and verify each against `--help`.
- [ ] **Step 3:** `git log --oneline feat/tier4b-cli-consolidation..HEAD` — ~9 commits, clean tree.

---

## Out of scope (future work, ledgered)

percentile helper consolidation (4 impls); caveat-rendering unification; correlate --ai vs --format; TUI ctx plumbing beyond current; forecast/deploy/timeline full historical windowing; postmortem zero-config product decision follow-through if requirements change.

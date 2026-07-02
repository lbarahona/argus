# Changelog

All notable changes to Argus will be documented in this file.

## [v0.8.0] - 2026-07-02

### Breaking Changes

#### Command map

| Old | New |
| --- | --- |
| `argus top` | `argus services --sort {errors,rate,calls}` (`--sort name`, the default, is the original listing; add `--limit`/`-l`) |
| `argus scorecard` | `argus report --grade` |
| `argus dashboard` | removed (`argus status` covers multi-instance health; dashboard's services/errors view has no single replacement — use `services` + `logs`) |
| `argus anomaly` | `argus analyze anomalies` |
| `argus timeline` | `argus analyze timeline` |
| `argus correlate` | `argus analyze correlate` (`stack` subcommand moves with it) |
| `argus deploy` | `argus analyze changes` |
| `argus diff` | `argus analyze diff` |
| `argus budget check` | `argus slo budget` (same flags; top-level `budget` command is gone) |
| `argus alert` | `argus rules` (`init`/`list`/`check` subcommands and `~/.argus/alerts.yaml` unchanged) |
| `argus instances` | removed (`argus use` with no arguments already lists configured instances) |
| `argus loki log` (alias) | removed (`argus loki query` still works) |

#### Flag changes

- `logs`: `-s` (severity) removed — use `--severity`
- `analyze anomalies`: `-s` (sensitivity) removed — use `--sensitivity`; `-s` now means `--service`; `-q` (quiet) removed — use `--quiet`
- `incident create`: `-s` (severity) and `-d` (description) removed — use `--severity`/`--description`
- `incident update`: `-a` (author) removed — use `--author` (`incident list` keeps `-a` for `--all`)
- `postmortem list`: `-n` (limit) replaced by `-l`/`--limit`
- `grafana`, `prom`, `postmortem`: `--format` gains the `-f` shorthand (previously long-only on these three)
- Duration flags across the CLI now accept human durations (`90m`, `2h`, `1h30m`) in addition to bare minutes
- Sub-minute duration values (e.g. `-d 30s`) are now rejected with an error — previously they silently truncated to 0 minutes; bare `0` remains allowed

#### Exit codes

- `slo budget` (formerly `budget check`): `critical` status now exits **2** (was 1), and `burning` status / `watch` alerts now exit **1** (was 0); `exhausted`/`page` still exit 2 and `ticket` still exits 1 — matches the `alert`/`slo`/`guard` convention. **CI gates keying off budget's old exit codes must update in both directions.**
- `slo check` gains `--fail-on-no-data`: exits 1 when any SLO result is `no_data`, without downgrading a higher exit code from a real warning/critical finding
- An unrecognized `--format`/`-f` value on any command now exits **1** with a validation error instead of proceeding and failing later (or silently falling back)
- `logs`/`traces`/`metrics` with `-q`/`--query` now hard-error (exit **1**) when no AI provider is configured — previously the query was silently ignored and raw output printed with exit 0. Scripts passing `-q` without AI config will break.

### Added

- `runbook run --execute`: actually executes command/check steps (previously always a dry run), with per-step confirmation, timeouts, captured output, and a run log saved under `~/.argus/runbooks/runs/`; a failed run exits 1
- Shell completion for flags (`--instance`, `--format`, duration flags) and for ID arguments (`incident update`/`resolve`/`timeline`, `runbook show`/`run`/`delete`/`validate`, `postmortem show`/`export`/`delete`/`generate`), degrading gracefully instead of erroring when a store can't be loaded
- `slo check --fail-on-no-data` for CI gates that need to treat a broken data pipeline (SLOs stuck at `no_data`) as a failure instead of a silent pass
- `loki query` decodes and renders matrix/vector metric results (`rate(...)`, `count_over_time(...)`), not just log streams — previously these either failed to decode or were silently dropped
- `WindowedQuerier` (`QueryLogsRange`, `QueryTracesRange`, `ListServicesRange`) for absolute time-range queries; `analyze diff` and `postmortem generate` now query their historical/previous windows for real instead of approximating from the current one
- Truncation caveats surfaced in output wherever results are capped (trace fetches, forecast/deploy/timeline bucketing, diff, log bodies) instead of silently skewing results
- AI provider hardening: SSE stream errors and `max_tokens` truncation are now surfaced instead of swallowed; the AI HTTP transport honors `HTTPS_PROXY`/`HTTP_PROXY` and has bounded per-phase timeouts (with a longer, header-timeout-free path for Bedrock's non-streaming invoke endpoint); context-aware `Analyze`/`AnalyzeWithHistory`/`AnalyzeSync` variants let the MCP server and TUI propagate cancellation
- Amazon Bedrock provider now sends the correct `bedrock-2023-05-31` payload and parses the invoke response, so it actually works against real AWS (previously broken)
- Default Anthropic model updated to `claude-sonnet-5`
- CI now runs tests and lint on every push and pull request (previously untriggered/misconfigured)

### Fixed

Highlights from the Tier 1-3 correctness passes (full detail in commit history):

- Empty Signoz result envelopes no longer parsed as phantom log/trace entries
- Error rates were being multiplied by 100 a second time (already a percentage) in `alert`, `explain`, and `postmortem` — thresholds and AI prompts now see the real value
- `alert`'s `log_errors` rule fetched only 1 log, making its threshold effectively unreachable
- Log severity filtering now matches `severity_text` case-insensitively (was missing lowercase/mixed-case values)
- Budget and SLO burn-rate math: consumption now scales by the observed window (previously over/under-counted on short windows), exhaustion is only predicted above 1.0x burn, and long-window SLOs escalate status on sustained burn instead of staying "ok"
- Latency SLOs reported a fake-healthy `ok` (100% budget remaining) when the trace query failed or returned no data; they now report `no_data` — a previously green `slo check` can show `no_data` for the same input (pair with `--fail-on-no-data` to gate on it in CI)
- `--format` help text, shell completion, and validation now reflect what each command actually supports — e.g. `report -f json` previously passed validation, did the full fetch, then failed; it now errors immediately naming the valid formats
- MCP `am_alerts` with `all=true` no longer drops firing alerts
- Config, incident, postmortem, and runbook stores write atomically (temp file + fsync + rename) instead of risking truncation on a crash mid-write
- Signoz v3 metrics parsing handles real object-shaped string-valued series (was failing on live data)
- `deps` discovers cross-service edges via one unfiltered trace query (previously missed edges depending on filter scope)
- `watch` computes real per-service P99 from traces so latency alerts can actually fire (was a stub)
- `scorecard` error trends are now derived from time-bucketed logs instead of comparing identical data to itself
- `postmortem` enriches metrics from the actual incident window (not an arbitrary recent window) and correctly parses markdown-formatted AI section headers, with a raw-analysis fallback and an honest caveat when enrichment falls back
- Hardening batch: panics, MCP URL/UID escaping, path traversal, and ID-ambiguity issues closed; TUI history clamped; output ordering (status/dashboard/doctor/anomaly) made deterministic; all string truncation is rune-safe (`textutil.Truncate`), no more split multibyte UTF-8

---

## [v0.7.0] - 2026-04-05

### Full Observability Stack Release 🔭

Argus now integrates with the complete observability stack: **Signoz + Alertmanager + Prometheus + Grafana + Loki**.

### Added

#### Alertmanager Integration (`argus am`)
- `alerts` — List firing alerts with severity colors and label filtering
- `silences` — List active/expired silences
- `silence-create` — Create silences with label matchers and duration
- `silence-delete` — Expire silences by ID
- `status` — Check Alertmanager health and cluster info
- `summary` — Quick alert counts by severity and name

#### Prometheus Integration (`argus prom`)
- `rules` — List alerting and recording rules with type filtering
- `targets` — Show scrape targets and their health status
- `alerts` — Show firing and pending alerts
- `query` — Execute instant PromQL queries
- `status` — Show version, health, and runtime info
- `summary` — Quick overview of rules, alerts, and targets

#### Grafana Integration (`argus grafana`)
- `dashboards` — List all dashboards by folder
- `dashboard` — Get detailed dashboard info by UID
- `search` — Search dashboards and folders
- `datasources` — List configured data sources
- `folders` — List dashboard folders
- `alerts` — List alert rules
- `firing` — List firing alert instances
- `status` — Check health and version
- `summary` — Quick overview of Grafana instance

#### Loki Integration (`argus loki`)
- `query` — Query logs with LogQL
- `labels` — List all label names
- `label-values` — List values for a specific label
- `series` — Find matching log series
- `stats` — Show ingestion statistics
- `status` — Check Loki health and version
- `summary` — Quick overview of Loki instance

### Stats
- **28 new commands** across 4 integrations
- **222 new tests** with 91-95% coverage per package
- **~9,000 lines** of new code
- **Total: 69+ commands** across 32 packages

---

## [v0.6.0] - 2026-03-14

### Added
- Multi-provider AI support (Anthropic, OpenAI, Amazon Bedrock)
- `argus use` command to switch default instance
- Low-coverage test sweep across all packages (all 28 above 70%)

---

## [v0.5.0] - 2026-03-07

### Added
- `argus doctor` — Diagnose configuration and connectivity issues
- GoReleaser CI/CD for binary releases
- Homebrew tap (`brew install lbarahona/tap/argus`)
- 38 commands across the CLI

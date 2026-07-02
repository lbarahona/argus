# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
make build          # Build binary to ./bin/argus
make install        # Install via go install
make test           # Run all tests (go test ./... -v)
make lint           # Run golangci-lint
make clean          # Remove bin/

# Run a single test
go test ./internal/signoz/ -run TestQueryPayload -v

# Build with version info (what make build does)
go build -ldflags '-s -w -X main.version=dev -X main.commit=abc -X main.date=now' -o bin/argus ./cmd/argus/
```

## Architecture

Argus is a CLI that connects to Signoz plus Loki, Prometheus, Grafana, and Alertmanager, and uses AI (Anthropic, OpenAI, or Amazon Bedrock) for natural-language analysis of logs, metrics, traces, and alerts. It's built with Cobra for CLI commands and lipgloss for terminal styling.

### Data Flow

The Signoz-backed commands (the majority of the CLI) follow: **Config → Signoz Client → Data → Output (or AI Analysis)**

1. `config.Load()` reads `~/.argus/config.yaml` (multi-instance Signoz config, AI provider config, and optional Loki/Prometheus/Grafana/Alertmanager config)
2. `config.GetInstance()` resolves the target Signoz instance (explicit `-i` flag or configured default)
3. `signoz.New(instance)` creates an HTTP client for that instance, implementing `signoz.SignozQuerier`; commands that need absolute time windows (not just "now minus duration") type-assert it to `signoz.WindowedQuerier`
4. The client queries Signoz via `POST /api/{v3|v5}/query_range` (builder queries) or `GET /api/v1/services`
5. Results are either rendered directly via `output.Print*()`/package `Render*()` functions, or sent to an `ai.Provider` for streaming (or, for Bedrock, buffered) analysis

The Loki/Prometheus/Grafana/Alertmanager commands (`loki`, `prom`, `grafana`, `am`) follow the same shape but each builds its own client independently from `cfg.Loki`/`cfg.Prometheus`/`cfg.Grafana`/`cfg.Alertmanager` (via `getLokiClient()`/`getPromClient()`/`getGrafanaClient()`/`getAMClient()` in their respective `cmd/argus/*_cmds.go` files) — there is no shared multi-instance abstraction for them like `signozContext`.

### Key Packages

- **`cmd/argus/`** — CLI commands, split into per-topic files (no sub-package routing; the whole cobra tree is still wired in one place, `rootCmd.AddCommand(...)` in `main.go`). Each `*Cmd()` function returns a `*cobra.Command`.
  - `main.go` — root command, `exitError`/`exitCodeFor` (carries a process exit code through `RunE` without `os.Exit`), `getAIProvider`/`requireAI`, `versionCmd`.
  - `helpers.go` — shared helpers used across every command file: `signozContext`/`newSignozContext` (config load + instance resolution + client, in one place), `renderOutput` + `formatSet` (single source of truth for a command's `--format` support — help text, shell completion, and rendering all derive from the same set), `addInstanceFlag`/`addFormatFlag`/`addDurationFlag` (the latter via `minutesValue`, a `pflag.Value` accepting bare minutes or Go durations like `90m`/`2h`), `completeIDs` (builds `ValidArgsFunction` for ID-taking commands, degrading to no completions on load failure).
  - `signoz_cmds.go` — `status`, `logs`, `services` (includes the old `top` ranked view via `--sort`), `traces`, `metrics`, `ask`.
  - `analysis_cmds.go` — `report` (includes the old `scorecard` via `--grade`), `analyze` (parent for `anomalies`, `timeline`, `correlate` + `correlate stack`, `changes`), `diff`, `watch`, `explain`, `forecast`, `deps`.
  - `sre_cmds.go` — `rules` (renamed from `alert`), `slo` (parent for `slo budget`), `guard`.
  - `am_cmds.go`, `prom_cmds.go`, `grafana_cmds.go`, `loki_cmds.go` — Alertmanager/Prometheus/Grafana/Loki integrations; each owns a `getXClient()` constructor that builds its client straight from config.
  - `incident_cmds.go`, `postmortem_cmds.go`, `runbook_cmds.go` — local state management commands.
  - `misc_cmds.go` — `tui`, `mcp`, `doctor`.
  - `config_cmds.go` — `config` (`init`, `add-instance`), `use`.
- **`internal/signoz`** — HTTP client for Signoz. Defines `SignozQuerier` (the baseline interface every consumer package depends on) and `WindowedQuerier` (absolute `start`/`end` range queries: `QueryLogsRange`, `QueryTracesRange`, `ListServicesRange`), both implemented by `Client`. All queries use typed v3 payload structs (`BuildQueryRangePayload()`) with composite builder queries (`builderQueries` map, `panelType`, structured `filters`). Response parsing handles multiple response shapes from Signoz (nested `data` fields, camelCase vs snake_case field names).
- **`internal/ai`** — Provider-agnostic AI layer. `Provider` interface (`Analyze`, `AnalyzeWithSystem`, `AnalyzeSync`, `Name`, `Model`) has three implementations: `AnthropicProvider` (SSE streaming, default model `claude-sonnet-5`), `OpenAIProvider` (SSE streaming), and `BedrockProvider` (non-streaming — Bedrock's plain `/invoke` endpoint returns one JSON body only after the full generation completes, so it buffers and writes once). `NewProvider(AIConfig)` selects the implementation from `cfg.Provider` and falls back to `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`/`AWS_BEARER_TOKEN_BEDROCK` env vars. `Analyzer` (used by callers that pre-date the `Provider` interface, e.g. simple single-prompt call sites) wraps a `Provider`: `Analyze`/`AnalyzeWithHistory`/`AnalyzeSync` stream or buffer against a background context, and `AnalyzeContext`/`AnalyzeWithHistoryContext`/`AnalyzeSyncContext` are the context-aware variants. `Message` is the shared conversation-turn type used by both layers and by `tui.Session`.
- **`internal/output`** — All terminal formatting using lipgloss. Exports styled `Print*` functions (`PrintServices`, `PrintLogs`, `PrintTraces`, `PrintDashboard`, `PrintAnomalyResult`, etc.) and reusable style variables (`ErrorStyle`, `SuccessStyle`, `WarningStyle`, `MutedStyle`, `AccentStyle`).
- **`internal/config`** — Reads/writes `~/.argus/config.yaml` (`Load`, `Save`, `RunInit`, `AddInstance`, `GetInstance`, `Exists`, `Path`/`Dir`). Config directory is `~/.argus/`.
- **`internal/fsutil`** — `WriteFileAtomic(path, data, perm)`: writes via a temp file in the same directory, fsyncs, then renames, so a crash mid-write never leaves a truncated file and readers never see a partial write. Used by every package that persists local state (`config`, `incident`, `postmortem`, `runbook`).
- **`internal/textutil`** — `Truncate(s, max)`: shortens a string to at most `max` runes on a rune boundary (never splits multibyte UTF-8), appending `"..."`. Used throughout the renderers (`report`, `guard`, `scorecard`, `timeline`, `correlate`, `deploy`, `explain`, `tui`, `anomaly`, `loki`/`prometheus`/`alertmanager`/`grafana` render, `mcpserver`) to keep table cells and log bodies bounded.
- **`pkg/types`** — Shared domain types (`Config`, `Instance`, `AIConfig`, `BedrockConfig`, `LokiConfig`/`PrometheusConfig`/`GrafanaConfig`/`AlertmanagerConfig`, `LogEntry`, `TraceEntry`, `MetricEntry`, `Service`, `QueryResult`, `HealthStatus`). Used across all packages.

### Feature Packages (internal/)

Each feature command has its own package with a consistent structure: `Options` struct, a `Run`/`Generate`/`Detect`/`Compare` function taking `signoz.SignozQuerier`, and `Render*`/`Format*` output functions.

- **`report`** — Health report generation (`argus report`), terminal and markdown rendering, detects error patterns by grouping log bodies. `--grade` switches the same command to a reliability scorecard (delegates to `scorecard.Generate`) instead of a health report.
- **`scorecard`** — Service reliability scorecard, reached via `argus report --grade` (not a standalone command). Grades each service (A-F) based on error rate, P50/P99 latency, traffic volume, and trends; computes a weighted overall score (by call volume). Optional AI summary via `generateAISummary()`. Scoring: error rate penalty (up to 50pts), latency penalty (up to 30pts), trend bonus/penalty (±5-10pts), no-traffic neutral (50pts).
- **`top`** — Ranked service view, reached via `argus services --sort {errors,rate,calls}` (not a standalone command; plain `--sort name`, the default, stays on the simpler `ListServices` path). Augments service data with recent error log counts.
- **`diff`** — `argus analyze diff`. Compares error counts between two consecutive time windows by splitting log results at a cutoff timestamp.
- **`watch`** — `argus watch`. Continuous polling loop with anomaly detection. Maintains a rolling baseline per service using exponential moving average (EMA, alpha=0.3). Detects error rate thresholds, P99 latency thresholds, error-count spikes vs. baseline, and new errors on previously clean services.
- **`anomaly`** — `argus analyze anomalies`. Z-score based anomaly detection across services (`Detect()`); default sensitivity (z-score threshold) is `2.0`, configurable via `--sensitivity`/`Options.Sensitivity`. Flags error-rate, log-volume, and latency (P50/P95/P99) anomalies plus directional trends; severities are ranked and rolled up per service. Optional AI summary.
- **`alert`** — Declarative alert rules from `~/.argus/alerts.yaml`, evaluated via `argus rules check` (command registers as `rules`, package name is still `alert`). Rule types: `error_rate`, `log_errors`, `service_down`. Exit codes: 0=ok, 1=warning, 2=critical.
- **`slo`** — SLO tracking from `~/.argus/slos.yaml` (`argus slo check`). Computes error budgets, burn rates, and compliance. SLO types: `availability` (from service error rates), `latency` (from trace durations). Exit codes: 0=ok, 1=warning, 2=critical/exhausted; `--fail-on-no-data` additionally forces exit 1 when any SLO is stuck at `no_data`, so a broken data pipeline can't silently read as clean.
- **`budget`** — Error budget burndown analysis (`argus slo budget`). Calculates budget consumption from SLO config + service data, multi-window burn rate alerting (Google SRE style: page/ticket/watch/none), exhaustion prediction, trend analysis, and deployment policy recommendations. Formats: terminal (budget gauge + sparkline), markdown, JSON. Exit codes: 0=healthy, 1=critical, 2=exhausted.
- **`correlate`** — `argus analyze correlate`. Cross-signal correlation across services: collects error logs and traces from all (or a specific) service, groups signals into temporal clusters with severity scoring, and detects error propagation patterns between services. Outputs event clusters, propagation edges, and optional Mermaid diagrams. Supports AI-powered causal chain analysis (`--ai`, via `RunWithAI`). The `stack` subcommand (`argus analyze correlate stack`) is a separate code path (`RunStack`) that correlates active alerts across Alertmanager, Prometheus, Grafana, and Loki directly — it does not touch Signoz.
- **`deploy`** — `argus analyze changes`. Deployment change detection and impact analysis (`Detect()`). Uses change-point detection (binary segmentation / CUSUM-inspired) on time-bucketed error counts and P99 latency to find behavioral shifts that typically correspond to deployments. Detects: error rate spikes/drops, latency shifts, new error patterns. Configurable sensitivity (low/medium/high). Impact scoring from -100 (regression) to +100 (improvement). Terminal and markdown output. Optional AI deployment impact analysis.
- **`timeline`** — `argus analyze timeline`. Incident timeline reconstruction: correlates error spikes (time-bucketed), new error patterns, latency spikes (P99 outliers), and service health into a chronological timeline. Supports terminal and markdown rendering, optional AI narrative. Detection functions: `detectErrorSpikes`, `detectNewErrors`, `detectLatencySpikes`, `detectServiceHealth`.
- **`explain`** — `argus explain`. Collects error logs, recent logs, and traces for a service, then builds a structured prompt for AI root cause analysis.
- **`forecast`** — `argus forecast`. Predictive service health analytics using linear regression. Time-buckets error logs and call volume, fits OLS regression to detect trends (rising/falling/stable with R² goodness-of-fit), and forecasts error rates at a configurable horizon. Risk scoring (0-100) based on current rate, trend slope, and predicted rate. Three risk levels: stable (<30), degrading (30-59), critical (60+). Terminal table and markdown output. Optional AI narrative.
- **`deps`** — `argus deps`. Service dependency mapping from trace data. Analyzes parent/child spans to discover upstream/downstream relationships, call volumes, error rates, and latency between services. ASCII graph and optional Mermaid diagram output. Optional AI architecture analysis.
- **`runbook`** — `argus runbook`. Runbook management stored as YAML in `~/.argus/runbooks/`. Subcommands: `init` (creates samples), `list`, `show`, `search`, `validate`, `run`, `delete`. Steps can be automated (command), check-based, or manual. `run` is a dry run by default (steps are shown, nothing executes); `--execute` actually runs command/check steps after a per-step confirmation, with timeouts, captured output, and a run log saved under `~/.argus/runbooks/runs/`. A failed run log exits 1.
- **`incident`** — `argus incident`. Local incident management with timeline tracking. Stores incidents in `~/.argus/incidents.yaml`. Subcommands: `create`, `list`, `update`, `resolve`, `timeline`. Tracks severity (critical/major/minor), status transitions, affected services, incident commander, and auto-computes duration.
- **`postmortem`** — `argus postmortem` (alias `pm`). Blameless postmortem generation from incidents. Auto-collects incident timeline, Signoz service metrics and error logs, and optionally runs AI root cause analysis. Stores postmortems in `~/.argus/postmortems.yaml`. Subcommands: `generate`, `list`, `show`, `export`, `delete`. Output formats: terminal (styled), markdown, JSON. AI response parser extracts structured sections (root cause, contributing factors, action items with priority/type, lessons learned, impact).
- **`tui`** — `argus tui`. Interactive REPL for multi-turn AI troubleshooting sessions. Uses `bufio.Scanner` + lipgloss (no bubbletea). `Session` struct holds an `ai.Provider`, conversation `history` (as `[]ai.Message`), and a `SignozQuerier` client. Each question automatically gathers live Signoz context (services + recent error logs) and appends it to the user message. Supports `/clear`, `/help`, `/history` commands. History is trimmed in pairs when exceeding `maxHistory` (default 20 messages). I/O is injectable (`stdin`/`stdout` fields) for testing.
- **`guard`** — `argus guard`. CI/CD deployment gate. Runs 5 automated checks (system health, error rates, P99 latency via traces, error spikes from logs, traffic saturation via median-based outlier detection) and returns SHIP/CAUTION/HOLD verdict with 0-100 confidence score. Supports `--strict` mode with tighter thresholds. Configurable error rate/latency/min-call-volume limits. Pipeline-friendly exit codes: 0=ship, 1=caution, 2=hold.
- **`doctor`** — `argus doctor`. Diagnostic checks on config file existence/parseability, Signoz instance connectivity, DNS resolution, internet connectivity, AI provider key configuration, and the Loki/Prometheus/Grafana/Alertmanager integrations (skipped with `StatusSkip` when not configured, not treated as failures). Statuses: PASS/WARN/FAIL/SKIP. Terminal, markdown, and JSON output; `--verbose` adds detail per check. Exits 1 if any check fails.
- **`loki`** — Client + render for Grafana Loki, reached via `argus loki`. `Client` wraps `/loki/api/v1/*`: `Query`/`QueryRange` (LogQL), `Labels`/`LabelValues`, `Series`, `IndexStats`, `Healthy`/`BuildInfo`, plus `BuildSummary` for the `summary` subcommand.
- **`prometheus`** — Client + render for Prometheus, reached via `argus prom` (alias `prometheus`). `Client` wraps `/api/v1/*`: `Rules`, `Alerts`, `Targets`, `Query`/`QueryRange` (PromQL), `RuntimeInfo`/`BuildInfo`, `Healthy`/`Ready`.
- **`grafana`** — Client + render for Grafana, reached via `argus grafana` (alias `graf`). `Client` wraps the Grafana HTTP API: `Search`, `Dashboards`/`GetDashboard`, `Folders`, `Datasources`, `AlertRules`/`AlertInstances`, `Health`/`Org`, plus `BuildSummary`.
- **`alertmanager`** — Client + render for Prometheus Alertmanager, reached via `argus am` (alias `alertmanager`). `Client` wraps `/api/v2/*`: `ListAlerts`/`ListAlertGroups`, `ListSilences`/`GetSilence`/`CreateSilence`/`DeleteSilence`, `ListReceivers`, `Status`/`Healthy`.
- **`mcpserver`** — `argus mcp`. MCP (Model Context Protocol) server over stdio (`mcpserver.Run(ctx, version)`, using `github.com/modelcontextprotocol/go-sdk/mcp`). Exposes ~30 Argus tools (`argus_status`, `argus_services`, `argus_logs`, `argus_traces`, `argus_metrics`, `argus_ask`, `argus_explain`, `argus_dashboard`, `argus_report`, `argus_top`, `argus_diff`, `argus_alert_check`, `argus_slo_check`, `argus_deploy`, `argus_budget`, `argus_guard`, `argus_doctor`, plus `argus_am_*`/`argus_prom_*`/`argus_grafana_*` families) to AI agents and LLM applications like Claude Desktop, Cursor, and other MCP clients.

### Patterns to Follow

- Config files live in `~/.argus/`: `config.yaml` (instances + AI + Loki/Prometheus/Grafana/Alertmanager), `alerts.yaml`, `slos.yaml`, `incidents.yaml`, `postmortems.yaml`, `runbooks/*.yaml` (plus `runbooks/runs/` for `--execute` run logs).
- Consumer packages accept `signoz.SignozQuerier` (or `signoz.WindowedQuerier` when they need absolute time ranges), not concrete `*signoz.Client`, enabling mock-based testing. Instance resolution and client creation happen in `cmd/argus/helpers.go`'s `newSignozContext`.
- `config.GetInstance()` is the single place for Signoz instance resolution (no per-package `getInstanceFromConfig()` duplication); the Loki/Prometheus/Grafana/Alertmanager integrations instead each get a small `getXClient()` in their own `cmd/argus/*_cmds.go` file since they aren't multi-instance.
- The Signoz client handles both camelCase and snake_case field names in responses (e.g., `serviceName` vs `service_name`)
- A command's `--format` support is declared once as a `formatSet{Markdown: ..., JSON: ...}` and passed to both `addFormatFlag` (help text + shell completion) and `renderOutput`/`fs.validate` (actual dispatch) — the two can't drift out of sync.
- Local state writes (config, incidents, postmortems, runbooks) go through `fsutil.WriteFileAtomic` — never `os.WriteFile` directly — so a crash mid-write can't corrupt the file.
- Renderers truncate user-controlled strings (log bodies, error messages) with `textutil.Truncate`, which cuts on rune boundaries rather than byte boundaries.
- Duration flags use `addDurationFlag`, accepting either bare minutes (`90`) or a Go duration string (`90m`, `2h`, `1h30m`); it rejects negative values and anything under a minute.
- Exit codes follow a 0/1/2 convention across the SRE-facing commands (`rules check`, `slo check`, `slo budget`, `guard`, `doctor` uses 0/1 only): 0 = ok/ship, 1 = warning/caution/fail, 2 = critical/exhausted/hold. Commands signal this via `exitError{code}` returned from `RunE`, mapped to a process exit code by `exitCodeFor` in `main()` — never a raw `os.Exit` inside a command.
- Version info is injected via ldflags at build time (`main.version`, `main.commit`, `main.date`)
- Releases use goreleaser triggered by `v*` tags; the GitHub Actions workflow runs tests then builds cross-platform binaries

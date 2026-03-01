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

Argus is a CLI that connects to [Signoz](https://signoz.io) observability instances and uses the Anthropic Claude API for AI-powered analysis. It's built with Cobra for CLI commands and lipgloss for terminal styling.

### Data Flow

All commands follow this pattern: **Config → Signoz Client → Data → Output (or AI Analysis)**

1. `config.Load()` reads `~/.argus/config.yaml` (multi-instance Signoz config + Anthropic key)
2. `config.GetInstance()` resolves the target instance (explicit `-i` flag or default)
3. `signoz.New(instance)` creates an HTTP client for that Signoz instance
4. The client queries Signoz via `POST /api/{v3|v5}/query_range` (builder queries) or `GET /api/v1/services`
5. Results are either rendered directly via `output.Print*()` or sent to `ai.Analyzer` for streaming Claude analysis

### Key Packages

- **`cmd/argus/main.go`** — All CLI commands are defined in a single file. Each command function (e.g., `logsCmd()`, `watchCmd()`, `tuiCmd()`) returns a `*cobra.Command`. No sub-package routing.
- **`internal/signoz`** — HTTP client for Signoz. Defines `SignozQuerier` interface implemented by `Client`. All queries use typed v3 payload structs (`BuildQueryRangePayload()`) with composite builder queries (`builderQueries` map, `panelType`, structured `filters`). Response parsing handles multiple response shapes from Signoz (nested `data` fields, camelCase vs snake_case field names).
- **`internal/ai`** — Thin wrapper around the Anthropic Messages API with SSE streaming. Uses `claude-sonnet-4-20250514`. Exports `Message` type for conversation turns. `Analyze()` streams a single-prompt response to an `io.Writer`; `AnalyzeWithHistory()` accepts a custom system prompt and `[]Message` slice for multi-turn conversations (used by the TUI); `AnalyzeSync()` buffers the full response.
- **`internal/output`** — All terminal formatting using lipgloss. Exports styled `Print*` functions and reusable style variables (`ErrorStyle`, `AccentStyle`, etc.).
- **`internal/config`** — Reads/writes `~/.argus/config.yaml`. Config directory is `~/.argus/`.
- **`pkg/types`** — Shared domain types (`Config`, `Instance`, `LogEntry`, `TraceEntry`, `MetricEntry`, `Service`, `QueryResult`). Used across all packages.

### Feature Packages (internal/)

Each feature command has its own package with a consistent structure: `Options` struct, a `Run`/`Generate`/`Compare` function, and `Render*` output methods.

- **`report`** — Health report generation with terminal and markdown rendering. Detects error patterns by grouping log bodies.
- **`top`** — Ranked service view sorted by errors/rate/calls. Augments service data with recent error log counts.
- **`diff`** — Compares error counts between two consecutive time windows by splitting log results at a cutoff timestamp.
- **`watch`** — Continuous polling loop with anomaly detection. Maintains a rolling baseline using exponential moving average (EMA, alpha=0.3). Detects error rate thresholds, P99 latency, error spikes, and new errors.
- **`alert`** — Declarative alert rules from `~/.argus/alerts.yaml`. Rule types: `error_rate`, `log_errors`, `service_down`. Exit codes: 0=ok, 1=warning, 2=critical.
- **`slo`** — SLO tracking from `~/.argus/slos.yaml`. Computes error budgets, burn rates, and compliance. SLO types: `availability` (from service error rates), `latency` (from trace durations). Exit codes: 0=ok, 1=warning, 2=critical/exhausted.
- **`correlate`** — Cross-signal correlation across services. Collects error logs and traces from all (or a specific) service, groups signals into temporal clusters with severity scoring, and detects error propagation patterns between services. Outputs event clusters, propagation edges, and optional Mermaid diagrams. Supports AI-powered causal chain analysis (`--ai`).
- **`explain`** — Collects error logs, recent logs, and traces for a service, then builds a structured prompt for AI root cause analysis.
- **`tui`** — Interactive REPL for multi-turn AI troubleshooting sessions. Uses `bufio.Scanner` + lipgloss (no bubbletea). `Session` struct holds conversation `history` (as `[]ai.Message`) and a `SignozQuerier` client. Each question automatically gathers live Signoz context (services + recent error logs) and appends it to the user message. Supports `/clear`, `/help`, `/history` commands. History is trimmed in pairs when exceeding `maxHistory` (default 20 messages). I/O is injectable (`stdin`/`stdout` fields) for testing.

### Patterns to Follow

- Config files live in `~/.argus/` (config.yaml, alerts.yaml, slos.yaml)
- Consumer packages accept `signoz.SignozQuerier` interface (not concrete `*signoz.Client`), enabling mock-based testing. Instance resolution and client creation happen in `cmd/argus/main.go`.
- `config.GetInstance()` is the single place for instance resolution (no more per-package `getInstanceFromConfig()` duplication)
- The Signoz client handles both camelCase and snake_case field names in responses (e.g., `serviceName` vs `service_name`)
- Version info is injected via ldflags at build time (`main.version`, `main.commit`, `main.date`)
- Releases use goreleaser triggered by `v*` tags; the GitHub Actions workflow runs tests then builds cross-platform binaries

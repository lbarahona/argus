# 🔭 Argus

**AI-powered observability CLI for SREs.**

Argus connects to your observability stack and uses AI to analyze logs, metrics, and traces with natural language queries. Integrates with **Signoz**, **Prometheus**, **Alertmanager**, **Grafana**, and **Loki**. Supports **Anthropic Claude**, **OpenAI GPT-4o**, and **Amazon Bedrock** as AI providers.

> *"Why is latency high on the payments service?"* — Just ask Argus.

---

## Features

- 🤖 **Natural language queries** — Ask questions about your infrastructure in plain English
- 📡 **Multi-instance support** — Manage multiple Signoz environments (production, staging, etc.)
- 📋 **Real log/trace/metric queries** — Direct integration with Signoz query_range API (v3 + v5)
- 🔧 **Service discovery** — List and rank services by errors, error rate, or call volume
- 🔗 **Cross-signal correlation** — Find error propagation across services, plus a stack-wide view across Alertmanager, Prometheus, Grafana, and Loki
- 📈 **Anomaly, change, and trend detection** — Z-score anomalies, deployment change-point detection, and linear-regression forecasts
- 🛡️ **CI/CD deployment gates** — SHIP/CAUTION/HOLD verdicts for safe deploys
- 🎯 **SLOs and error budgets** — Burn-rate alerting with Google SRE-style multi-window policy
- 📓 **Runbooks, incidents, and postmortems** — Track and execute operational procedures end to end
- 🔌 **Full observability stack** — Signoz + Alertmanager + Prometheus + Grafana + Loki
- 🧩 **MCP server** — Expose Argus as tools for Claude Desktop, Cursor, and other MCP clients
- ⚡ **Streaming AI responses** — Real-time analysis output as tokens arrive
- 🎨 **Beautiful terminal UI** — Severity-colored logs, formatted traces, metric tables
- 🐚 **Shell completions** — bash, zsh, fish, and PowerShell, including instance/format/ID completion

## Installation

### Homebrew (macOS & Linux)

```bash
brew install lbarahona/tap/argus
```

### From source

```bash
go install github.com/lbarahona/argus/cmd/argus@latest
```

### Binary releases

Download from [GitHub Releases](https://github.com/lbarahona/argus/releases).

### Build from source

```bash
git clone https://github.com/lbarahona/argus.git
cd argus
make build
# Binary at ./bin/argus
```

### Shell completions

```bash
# bash (requires bash-completion)
argus completion bash > /etc/bash_completion.d/argus

# zsh
argus completion zsh > "${fpath[1]}/_argus"

# fish
argus completion fish > ~/.config/fish/completions/argus.fish

# powershell
argus completion powershell > argus.ps1
```

Completions cover more than command names — instance names (`-i`), output formats (`-f/--format`), `--sort` values, and incident/runbook/postmortem IDs all complete once you have data on disk.

## Quick Start

```bash
# 1. Initialize configuration
argus config init

# 2. Check instance health
argus status

# 3. List services
argus services

# 4. Query logs with AI analysis
argus logs auth-service --query "any errors in the last hour?"

# 5. View traces
argus traces frontend -d 30

# 6. Quick health report
argus report

# 7. Ask free-form questions
argus ask "why is latency high on the payments service?"
```

## Commands

| Command | Description |
|---------|-------------|
| `argus version` | Print version information |
| `argus config init` | Interactive configuration setup |
| `argus config add-instance` | Add a new Signoz instance |
| `argus use [instance]` | Show or set the default Signoz instance |
| `argus status` | Health check all instances |
| `argus doctor` | Diagnose config, connectivity, and AI key issues |
| `argus services` | List services, optionally ranked by `--sort` |
| `argus logs [service]` | Query and analyze logs |
| `argus traces [service]` | Query distributed traces |
| `argus metrics [metric]` | Query metrics |
| `argus ask [question]` | Free-form AI analysis |
| `argus explain [service]` | AI root cause analysis for one service |
| `argus report` | Health report for shift handoffs (`--grade` for a reliability scorecard) |
| `argus watch` | Continuous monitoring with anomaly detection |
| `argus analyze anomalies` | Z-score anomaly detection across services |
| `argus analyze timeline` | Chronological incident timeline reconstruction |
| `argus analyze correlate` | Cross-signal correlation and error propagation |
| `argus analyze correlate stack` | Correlate alerts across Alertmanager/Prometheus/Grafana/Loki |
| `argus analyze changes` | Deployment/change-point detection and impact analysis |
| `argus analyze diff` | Error-count comparison between two time windows |
| `argus deps` | Map service dependencies from trace data |
| `argus forecast` | Predict service health trends with linear regression |
| `argus guard` | CI/CD deployment gate — SHIP/CAUTION/HOLD verdict |
| `argus rules init/list/check` | Declarative alert rules evaluated against Signoz |
| `argus slo init/list/check` | Evaluate SLOs — error budgets, burn rates, compliance |
| `argus slo budget` | Multi-window burn-rate analysis and exhaustion prediction |
| `argus runbook init/list/show/search/validate/run` | Manage and execute operational runbooks |
| `argus incident create/list/update/resolve/timeline` | Track incidents with a timeline |
| `argus postmortem generate/list/show/export/delete` | Generate postmortems from incidents |
| `argus tui` | Interactive multi-turn AI troubleshooting session |
| `argus mcp` | Start the MCP server (stdio) for AI agents |
| `argus am alerts/silences/silence-create/silence-delete/status/summary` | Alertmanager integration |
| `argus grafana dashboards/dashboard/search/datasources/folders/alerts/firing/status/summary` | Grafana integration |
| `argus prom rules/targets/alerts/query/status/summary` | Prometheus integration |
| `argus loki query/labels/label-values/series/stats/status/summary` | Loki integration |
| `argus completion bash\|zsh\|fish\|powershell` | Generate a shell completion script |

Every duration flag (`-d`/`--duration`) accepts either bare minutes (`-d 90`) or a Go-style duration string (`-d 90m`, `-d 2h`, `-d 1h30m`).

### Logs

```bash
# Query logs for a service
argus logs my-service

# Filter by severity
argus logs my-service --severity ERROR

# With AI analysis
argus logs my-service -q "find authentication failures"

# Specify instance, duration, and limit
argus logs my-service -i staging -d 2h -l 50
```

### Services

```bash
# List all services with error rates
argus services

# Rank by errors, error rate, or call volume (like htop for services)
argus services --sort errors
argus services --sort rate -d 2h
argus services --sort calls --limit 10

# From a specific instance
argus services -i production
```

### Traces

```bash
# Query traces for a service
argus traces frontend

# With duration and limit
argus traces api-gateway -d 2h --limit 50

# With AI analysis
argus traces frontend -q "why are these traces so slow?"
```

### Metrics

```bash
# Query a specific metric
argus metrics cpu_usage

# With AI analysis
argus metrics http_request_duration -q "any anomalies?"
```

### Ask

```bash
# Free-form questions — gathers context from Signoz automatically
argus ask "what services had the most errors today?"
argus ask "is there a correlation between high CPU and slow responses?"
```

### Explain

```bash
# AI root cause analysis — correlates logs, traces, and metrics
argus explain api-service

# Analyze last 30 minutes
argus explain payment-service --duration 30

# Against a specific instance
argus explain auth-service -i production
```

### Report

`argus report` compiles a health report for shift handoffs. Pass `--grade` to
instead generate a service reliability scorecard (A–F grades per service,
weighted by call volume).

```bash
# Generate a health report (terminal format)
argus report

# Include AI-generated summary
argus report --ai -d 2h

# Output as markdown (great for Slack/docs)
argus report --format markdown

# Reliability scorecard instead of a health report
argus report --grade

# Scorecard for a single service
argus report --grade --service api-gateway
```

### Watch

Continuously polls a Signoz instance and alerts on error rate spikes, high
latency, error-count spikes vs. a rolling baseline, and new errors on
previously clean services.

```bash
# Poll every 30s with default thresholds
argus watch

# Custom poll interval
argus watch --interval 60

# Custom error rate thresholds (%)
argus watch --error-rate-warn 3 --error-rate-crit 10

# Custom P99 latency thresholds (ms) against a specific instance
argus watch -i production --p99-warn 1000 --p99-crit 5000

# Error spike multiplier over the rolling baseline (default 3x)
argus watch --spike 4
```

### Analyze

`argus analyze` groups the deeper investigation commands: anomaly detection,
incident timelines, cross-signal correlation, deployment/change detection,
and window-over-window diffing.

```bash
# Z-score anomaly detection across services
argus analyze anomalies
argus analyze anomalies --duration 120 --sensitivity 1.5
argus analyze anomalies --service api-service --ai
argus analyze anomalies --quiet   # only show anomalies, hide healthy services

# Chronological incident timeline
argus analyze timeline
argus analyze timeline --duration 120
argus analyze timeline --service api-service --ai
argus analyze timeline --format markdown > incident-report.md

# Cross-signal correlation (logs + traces + metrics across all services)
argus analyze correlate
argus analyze correlate --service api-gateway
argus analyze correlate --duration 30 --ai
argus analyze correlate --bucket 30 --min-events 5

# Correlate alerts across the whole stack (Alertmanager/Prometheus/Grafana/Loki)
argus analyze correlate stack --duration 30

# Deployment/change-point detection and impact analysis (-100 to +100)
argus analyze changes
argus analyze changes --duration 720 --sensitivity high
argus analyze changes -s payment-api --ai
argus analyze changes -f markdown > deploy-report.md

# Compare the current window against the previous one
argus analyze diff
argus analyze diff --duration 30
argus analyze diff -i production --duration 15
```

### Deps

```bash
# ASCII dependency graph from trace spans
argus deps

# Filter to one service's upstream/downstream deps
argus deps --service api-gateway

# Longer lookback, markdown output, AI architecture analysis
argus deps -d 2h --format markdown --ai
```

### Forecast

```bash
# Forecast error rates and traffic 60 minutes out (default)
argus forecast

# Longer history and horizon
argus forecast --duration 240 --horizon 120

# One service with an AI narrative
argus forecast -s api-service --ai

# Markdown output
argus forecast -f markdown > forecast.md
```

Risk levels: `stable` (score < 30), `degrading` (30–59), `critical` (60+).

### Guard (CI/CD Deployment Gate)

```bash
# Should we deploy? Get a SHIP/CAUTION/HOLD verdict
argus guard

# Strict mode for critical services (lower thresholds, blocks on warnings)
argus guard --strict

# Check specific service before deploying it
argus guard --service api-gateway

# JSON output for CI/CD pipelines
argus guard --format json

# Custom thresholds
argus guard --max-error-rate 2.0 --max-p99 3000

# Include an AI deployment advisory
argus guard --ai

# In CI/CD pipelines (exit code 0=ship, 1=caution, 2=hold)
argus guard --strict --format json || exit 1

# GitHub Actions example:
# - name: Pre-deploy safety check
#   run: argus guard --strict
```

### Rules (alerting)

```bash
# Create sample alert rules
argus rules init

# List configured rules
argus rules list

# Check all rules (colored output)
argus rules check

# JSON output for cron/automation
argus rules check --format json

# Exit codes: 0=ok, 1=warnings, 2=critical
argus rules check && echo "All clear" || echo "Alerts fired!"
```

Rules are defined in `~/.argus/alerts.yaml`:

```yaml
rules:
  - name: high-error-rate
    type: error_rate
    operator: gt
    warning: 5.0
    critical: 15.0
    duration: 5m
    labels:
      team: platform

  - name: api-errors
    service: api-service
    type: error_rate
    operator: gt
    warning: 2.0
    critical: 10.0

  - name: log-errors
    type: log_errors
    operator: gt
    warning: 10
    critical: 50
    duration: 15m

  - name: service-health
    type: service_down
    operator: lt
    warning: 1
    critical: 1
    duration: 10m
```

### SLO

```bash
# Create sample SLO definitions
argus slo init

# List configured SLOs
argus slo list

# Evaluate all SLOs (colored output with budget bars)
argus slo check

# JSON output for dashboards/automation
argus slo check --format json

# Fail (exit 1) if any SLO has no data — useful in CI gates so a broken
# data pipeline doesn't silently report clean
argus slo check --fail-on-no-data

# Exit codes: 0=ok, 1=warning, 2=critical/exhausted
argus slo check && echo "Within budget" || echo "Budget alert!"
```

SLO definitions live in `~/.argus/slos.yaml`:

```yaml
slos:
  - name: "API Availability"
    service: ""        # empty = all services
    type: availability # availability or latency
    target: 99.9       # 99.9%
    window: 24h        # 1h, 6h, 24h, 7d, 30d
    labels:
      team: platform
      tier: "1"

  - name: "API Latency P99"
    type: latency
    target: 99.0       # 99% of requests under threshold
    threshold: 500     # milliseconds
    window: 24h
```

Output includes error budget bars, burn rates, and compliance status:
- ✅ OK — within budget
- ⚠️ Warning — >50% budget consumed
- 🔴 Critical — >80% budget consumed
- 💀 Exhausted — budget blown

#### SLO Budget (burn-rate analysis)

`argus slo budget` requires SLOs to already be configured (`argus slo init`).
It implements Google SRE-style multi-window burn rate alerting:

- **Fast burn**: 1h burn rate >14x AND 6h >6x → PAGE immediately
- **Slow burn**: 6h burn rate >6x → create TICKET
- **Elevated**: 1h burn rate >6x → WATCH closely

```bash
# Error budget burndown analysis
argus slo budget

# Check specific service
argus slo budget --service api-gateway

# JSON or markdown output for automation
argus slo budget --format json
argus slo budget --format markdown

# With AI recommendations
argus slo budget --ai

# Different analysis window (default 6h)
argus slo budget --window 24h
```

Exit codes: 0 = healthy, 1 = warning (burning/ticket/watch), 2 = critical
(critical/exhausted/page).

### Runbooks

Runbooks are stored as YAML in `~/.argus/runbooks/` and can be shared across
teams via version control. Steps can be automated (command), check-based, or
manual.

```bash
# Create sample runbooks to get started
argus runbook init

# List all runbooks
argus runbook list
argus runbook list --category incident-response

# Search by name, description, tags, or category
argus runbook search "database"

# Show full runbook details
argus runbook show <id>

# Validate a runbook's structure
argus runbook validate <id>

# Dry run — walk through steps without executing anything (default)
argus runbook run <id>

# Actually execute command/check steps, with per-step confirmation,
# timeouts, captured output, and a run log under ~/.argus/runbooks/runs/
argus runbook run <id> --execute

# Delete a runbook
argus runbook delete <id>
```

### Incidents

Incidents are stored locally in `~/.argus/incidents.yaml`, entirely
CLI-managed — no external ticketing system required.

```bash
# Create an incident
argus incident create "API returning 500s" --severity critical --services api-service

# List active incidents (or --all for everything)
argus incident list
argus incident list --all --limit 5

# Add a timeline entry as the incident progresses
argus incident update INC-20260222-001 --status identified --message "root cause: bad deploy"

# Resolve
argus incident resolve INC-20260222-001 --message "deployed fix in v2.3.1"

# Show the full timeline
argus incident timeline INC-20260222-001
```

### Postmortems

Auto-collects an incident's timeline, Signoz service metrics, and error
logs, then optionally runs AI root cause analysis to produce a structured,
blameless postmortem. Stored in `~/.argus/postmortems.yaml`. `argus
postmortem` also accepts the alias `argus pm`.

```bash
# Generate a postmortem from an existing incident
argus postmortem generate INC-20260222-001

# With AI-powered root cause analysis and action items
argus postmortem generate INC-20260222-001 --ai

# List, show, export
argus postmortem list
argus postmortem show <postmortem-id>
argus postmortem export <postmortem-id> -o postmortem.md

# Delete
argus postmortem delete <postmortem-id>
```

### TUI (interactive session)

```bash
# Start an interactive multi-turn AI troubleshooting session
argus tui

# Against a specific instance
argus tui -i production

# Retain more conversation history (default 20 messages)
argus tui --max-history 40
```

Each question automatically gathers live Signoz context (services + recent
error logs) before asking the AI. Supports `/clear`, `/help`, and `/history`
inside the session.

### MCP server

```bash
# Start the MCP server over stdio
argus mcp
```

Configure it in Claude Desktop, Cursor, or any MCP client:

```json
{
  "mcpServers": {
    "argus": {
      "command": "argus",
      "args": ["mcp"]
    }
  }
}
```

Exposes status, services, logs, traces, metrics, the analysis commands, and
the Alertmanager/Prometheus/Grafana/Loki integrations as tools.

### Doctor

```bash
# Diagnose config, instance connectivity, AI keys, and network reachability
argus doctor

# Verbose — show detail for every check, not just failures
argus doctor --verbose

# Markdown or JSON output (e.g. for a status page or CI step)
argus doctor --format markdown
argus doctor --format json
```

### Alertmanager

Argus integrates with Prometheus Alertmanager to query firing alerts, manage
silences, and check cluster status. `argus am` also accepts the alias
`argus alertmanager`.

```yaml
# Add to ~/.argus/config.yaml
alertmanager:
  url: http://alertmanager:9093
  basic_auth:       # optional
    username: admin
    password: secret
```

```bash
# List firing alerts
argus am alerts

# Show all alerts (including silenced/inhibited)
argus am alerts --all

# Filter by label
argus am alerts --filter alertname=HighLatency

# Quick summary — counts by severity
argus am summary

# List active silences
argus am silences

# Include expired silences
argus am silences --expired

# Create a 2-hour silence for a specific alert
argus am silence-create -m alertname=HighLatency -d 2h -c "Deploying fix"

# Silence with regex matcher
argus am silence-create -m alertname=~"High.*" -m severity=warning -d 1h

# Expire a silence
argus am silence-delete <silence-id>

# Check Alertmanager health
argus am status

# JSON output — alerts, silences, status, and summary support --format json;
# silence-create and silence-delete do not (they only print a confirmation)
argus am alerts -f json
```

### Grafana

Connect to your Grafana instance to browse dashboards, data sources, and
alert rules. `argus grafana` also accepts the alias `argus graf`.

```yaml
# ~/.argus/config.yaml
grafana:
  url: http://grafana:3000
  api_key: glsa_xxxxxxxxxxxx  # optional: service account token
```

```bash
# List all dashboards grouped by folder
argus grafana dashboards

# Search dashboards or folders
argus grafana search "kubernetes"
argus grafana search "kubernetes" --type dash-db

# Get dashboard details by UID
argus grafana dashboard abc123

# List configured data sources
argus grafana datasources

# List folders
argus grafana folders

# List alert rules
argus grafana alerts

# Show firing alert instances
argus grafana firing

# Quick overview
argus grafana summary

# Health and version
argus grafana status

# JSON output (all grafana subcommands support --format json)
argus grafana dashboards --format json
```

### Prometheus

Query Prometheus directly for rules, targets, alerts, and instant PromQL
queries. `argus prom` also accepts the alias `argus prometheus`.

```bash
# Configure Prometheus URL in ~/.argus/config.yaml:
# prometheus:
#   url: http://localhost:9090
#   basic_auth:          # optional
#     username: admin
#     password: secret

# List all alerting and recording rules
argus prom rules

# Filter by rule type
argus prom rules --type alert
argus prom rules --type record

# Show scrape targets and their health
argus prom targets

# Show firing and pending alerts from Prometheus
argus prom alerts

# Run instant PromQL queries
argus prom query 'up'
argus prom query 'rate(http_requests_total[5m])'

# Quick overview (rules + alerts + targets)
argus prom summary

# Check Prometheus version and health
argus prom status

# JSON output — rules, targets, alerts, query, and status support
# --format json; summary is terminal-only (no --format flag)
argus prom rules --format json
```

### Loki

Query Grafana Loki for log streams, labels, series, and ingestion stats.

```yaml
# ~/.argus/config.yaml
loki:
  url: http://loki:3100
  basic_auth:       # optional
    username: admin
    password: secret
  tenant_id: my-org  # optional, for multi-tenant setups
```

```bash
# Query logs with LogQL
argus loki query '{app="api-gateway"}'
argus loki query '{namespace="production"} |= "error"' --limit 50

# Discover labels and values (label-values takes -d/--duration, not --start)
argus loki labels
argus loki label-values app
argus loki label-values namespace -d 2h

# Find matching log series
argus loki series '{app="api-gateway"}'

# Ingestion statistics
argus loki stats

# Health and version
argus loki status

# Quick overview
argus loki summary

# JSON output (all loki subcommands support --format json)
argus loki query '{app="api"}' --format json
```

## Configuration

Config is stored at `~/.argus/config.yaml`. Run `argus config init` for
interactive setup, or `argus config add-instance` to add another Signoz
instance to an existing config. Use `argus use [instance]` to list configured
instances or switch the default (like `kubectl config get-contexts`).

### Anthropic (default)

```yaml
anthropic_key: sk-ant-...  # legacy root-level key still works
ai:
  provider: anthropic
  anthropic_key: sk-ant-...
  model: auto  # defaults to claude-sonnet-4-20250514
default_instance: production
instances:
  production:
    url: https://signoz.example.com
    api_key: your-signoz-api-key
    name: Production
    api_version: v3
```

### OpenAI

```yaml
ai:
  provider: openai
  openai_key: sk-...
  model: gpt-4o  # or gpt-4o-mini, etc.
default_instance: production
instances:
  production:
    url: https://signoz.example.com
    api_key: your-signoz-api-key
    name: Production
```

### Amazon Bedrock

Bedrock uses bearer token auth (no AWS SDK, no SigV4). Get a bearer token via AWS SSO, Identity Center, or any mechanism that issues bearer tokens for Bedrock.

```yaml
ai:
  provider: bedrock
  bedrock:
    endpoint: https://bedrock-runtime.us-east-1.amazonaws.com
    token: your-bearer-token
    model: anthropic.claude-3-5-sonnet-20241022-v2:0
default_instance: production
instances:
  production:
    url: https://signoz.example.com
    api_key: your-signoz-api-key
    name: Production
```

### Environment Variables

All AI keys can be set via environment variables (they override config values):

| Variable | Provider | Description |
|----------|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic | API key for Claude |
| `OPENAI_API_KEY` | OpenAI | API key for GPT models |
| `AWS_BEARER_TOKEN_BEDROCK` | Bedrock | Bearer token for Bedrock |

### Model Selection

Set `model: auto` (or omit it) to use the best default model for each provider:

| Provider | Default Model |
|----------|--------------|
| Anthropic | `claude-sonnet-4-20250514` |
| OpenAI | `gpt-4o` |
| Bedrock | `anthropic.claude-3-5-sonnet-20241022-v2:0` |

Or specify any model explicitly: `model: claude-3-haiku-20240307`

### API Version

- **v3** (default) — For self-hosted Signoz instances (`/api/v3/query_range`)
- **v5** — For Signoz Cloud (`/api/v5/query_range`)

### Backward Compatibility

The legacy `anthropic_key` at the root level of config.yaml still works. If set, it's used as fallback when `ai.anthropic_key` is empty.

## Requirements

- A [Signoz](https://signoz.io) instance (self-hosted or cloud)
- An AI API key from one of: [Anthropic](https://console.anthropic.com/), [OpenAI](https://platform.openai.com/api-keys), or [Amazon Bedrock](https://aws.amazon.com/bedrock/)

## Contributing

Contributions are welcome! Please open an issue or submit a PR.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

[MIT](LICENSE) © Lester Barahona

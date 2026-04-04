# 🔭 Argus

**AI-powered observability CLI for SREs.**

Argus connects to your [Signoz](https://signoz.io) instances and uses AI to analyze logs, metrics, and traces with natural language queries. Supports **Anthropic Claude**, **OpenAI GPT-4o**, and **Amazon Bedrock** as AI providers.

> *"Why is latency high on the payments service?"* — Just ask Argus.

---

## Features

- 🤖 **Natural language queries** — Ask questions about your infrastructure in plain English
- 📡 **Multi-instance support** — Manage multiple Signoz environments (production, staging, etc.)
- 📋 **Real log/trace/metric queries** — Direct integration with Signoz query_range API (v3 + v5)
- 🔧 **Service discovery** — List services with call counts and error rates
- 📊 **Dashboard view** — Combined overview of health, services, and recent errors
- ⚡ **Streaming AI responses** — Real-time analysis output as tokens arrive
- 🎨 **Beautiful terminal UI** — Severity-colored logs, formatted traces, metric tables
- 🔧 **Simple configuration** — YAML config, multiple profiles, easy setup

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
argus traces frontend --duration 30

# 6. Quick dashboard
argus dashboard

# 7. Ask free-form questions
argus ask "why is latency high on the payments service?"
```

## Commands

| Command | Description |
|---------|-------------|
| `argus version` | Print version information |
| `argus config init` | Interactive configuration setup |
| `argus config add-instance` | Add a new Signoz instance |
| `argus instances` | List configured instances |
| `argus status` | Health check all instances |
| `argus services` | List services with call counts and error rates |
| `argus logs [service]` | Query and analyze logs |
| `argus traces [service]` | Query distributed traces |
| `argus metrics [metric]` | Query metrics |
| `argus incident create` | Create a new incident |
| `argus incident list` | List active incidents |
| `argus incident update` | Update incident status |
| `argus incident resolve` | Resolve an incident |
| `argus incident timeline` | Show incident timeline |
| `argus dashboard` | Combined overview dashboard |
| `argus ask [question]` | Free-form AI analysis |
| `argus report` | Generate health report for shift handoffs |
| `argus top` | Ranked service view (like htop for services) |
| `argus diff` | Compare error rates between time windows |
| `argus watch` | Continuous monitoring with anomaly detection |
| `argus alert` | Declarative alert rules with cron-friendly output |
| `argus explain` | AI root cause analysis (correlates logs + traces) |
| `argus deploy` | Detect deployments from behavioral changes and analyze impact |
| `argus slo` | SLO tracking with error budgets and burn rates |
| `argus budget check` | Error budget burndown with burn rate analysis |
| `argus guard` | CI/CD deployment gate — SHIP/CAUTION/HOLD verdict |
| `argus am alerts` | List firing alerts from Alertmanager |
| `argus am silences` | List active silences |
| `argus am silence-create` | Create a silence with label matchers |
| `argus am silence-delete` | Expire a silence by ID |
| `argus am status` | Check Alertmanager health and version |
| `argus am summary` | Quick alert counts by severity and name |
| `argus grafana dashboards` | List all Grafana dashboards by folder |
| `argus grafana dashboard [uid]` | Get detailed dashboard info by UID |
| `argus grafana search [query]` | Search dashboards and folders |
| `argus grafana datasources` | List configured data sources |
| `argus grafana folders` | List dashboard folders |
| `argus grafana alerts` | List alert rules |
| `argus grafana firing` | List firing alert instances |
| `argus grafana status` | Check Grafana health and version |
| `argus grafana summary` | Quick overview of Grafana instance |
| `argus prom rules` | List alerting and recording rules |
| `argus prom targets` | Show scrape targets and their health |
| `argus prom alerts` | Show firing and pending alerts from Prometheus |
| `argus prom query` | Execute an instant PromQL query |
| `argus prom status` | Show Prometheus version, health, and runtime info |
| `argus prom summary` | Quick overview of rules, alerts, and targets |

### Logs

```bash
# Query logs for a service
argus logs my-service

# Filter by severity
argus logs my-service --severity ERROR

# With AI analysis
argus logs my-service --query "find authentication failures"

# Specify instance, duration, and limit
argus logs my-service -i staging -d 120 -l 50
```

### Services

```bash
# List all services with error rates
argus services

# From a specific instance
argus services -i production
```

### Traces

```bash
# Query traces for a service
argus traces frontend

# With duration and limit
argus traces api-gateway -d 30 -l 50

# With AI analysis
argus traces frontend --query "find slow requests over 1s"
```

### Metrics

```bash
# Query a specific metric
argus metrics cpu_usage

# With AI analysis
argus metrics http_request_duration --query "any anomalies?"
```

### Dashboard

```bash
# Quick overview of everything
argus dashboard

# Look back further for errors
argus dashboard -d 120
```

### Ask

```bash
# Free-form questions — gathers context from Signoz automatically
argus ask "what services had the most errors today?"
argus ask "is there a correlation between high CPU and slow responses?"
```

### Report

```bash
# Generate a health report (terminal format)
argus report

# Include AI-generated summary
argus report --ai

# Output as markdown (great for Slack/docs)
argus report -f markdown

# Cover last 4 hours
argus report -d 240 --ai
```

### Top

```bash
# Show top services ranked by errors (like htop for services)
argus top

# Sort by error rate instead
argus top -s rate

# Sort by call volume
argus top -s calls

# Limit and custom duration
argus top -l 10 -d 120
```

### Diff

```bash
# Compare last hour vs previous hour
argus diff

# Compare last 30 min vs previous 30 min
argus diff -d 30

# Shows which services are degrading, improving, or stable
argus diff -i production
```

### Alert

```bash
# Create sample alert rules
argus alert init

# List configured rules
argus alert list

# Check all rules (colored output)
argus alert check

# JSON output for cron/automation
argus alert check --format json

# Exit codes: 0=ok, 1=warnings, 2=critical
argus alert check && echo "All clear" || echo "Alerts fired!"
```

Alert rules are defined in `~/.argus/alerts.yaml`:

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

### Explain

```bash
# AI root cause analysis — correlates logs, traces, and metrics
argus explain api-service

# Analyze last 30 minutes
argus explain payment-service --duration 30

# Against a specific instance
argus explain auth-service -i production
```

### Budget

```bash
# Error budget burndown analysis
argus budget check

# Check specific service
argus budget check --service api-gateway

# JSON output for automation
argus budget check --format json

# With AI recommendations
argus budget check --ai

# Different analysis windows
argus budget check --window 24h
```

### Guard (CI/CD Deployment Gate)

```bash
# Should we deploy? Get a SHIP/CAUTION/HOLD verdict
argus guard

# Strict mode for critical services (lower thresholds)
argus guard --strict

# Check specific service before deploying it
argus guard --service api-gateway

# JSON output for CI/CD pipelines
argus guard --format json

# Custom thresholds
argus guard --max-error-rate 2.0 --max-p99 3000

# In CI/CD pipelines (exit code 0=ship, 1=caution, 2=hold)
argus guard --strict --format json || exit 1

# GitHub Actions example:
# - name: Pre-deploy safety check
#   run: argus guard --strict
```

### Alertmanager

Argus integrates with Prometheus Alertmanager to query firing alerts, manage silences, and check cluster status.

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

# JSON output (all subcommands support --format json)
argus am alerts -f json
```

### Grafana

Connect to your Grafana instance to browse dashboards, data sources, and alert rules:

```yaml
# ~/.argus/config.yaml
grafana:
  url: http://grafana:3000
  api_key: glsa_xxxxxxxxxxxx  # optional: service account token
```

```bash
# List all dashboards grouped by folder
argus grafana dashboards

# Search dashboards
argus grafana search "kubernetes"

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

# JSON output (all subcommands support --format json)
argus grafana dashboards --format json
```

### Prometheus

Query Prometheus directly for rules, targets, alerts, and instant PromQL queries.

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

# Show firing alerts from Prometheus
argus prom alerts

# Run instant PromQL queries
argus prom query 'up'
argus prom query 'rate(http_requests_total[5m])'

# Quick overview (rules + alerts + targets)
argus prom summary

# Check Prometheus version and health
argus prom status

# JSON output (all subcommands support --format json)
argus prom rules --format json
```

## Configuration

Config is stored at `~/.argus/config.yaml`. Run `argus config init` for interactive setup.

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

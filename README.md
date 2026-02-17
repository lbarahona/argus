# 🔭 Argus

**AI-powered observability CLI for SREs.**

Argus connects to your [Signoz](https://signoz.io) instances and uses Anthropic Claude to analyze logs, metrics, and traces with natural language queries.

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
| `argus dashboard` | Combined overview dashboard |
| `argus ask [question]` | Free-form AI analysis |

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

## Configuration

Config is stored at `~/.argus/config.yaml`:

```yaml
anthropic_key: sk-ant-...
default_instance: production
instances:
  production:
    url: https://signoz.example.com
    api_key: your-signoz-api-key
    name: Production
    api_version: v3  # v3 for self-hosted, v5 for Signoz Cloud
  staging:
    url: https://signoz-staging.example.com
    api_key: your-staging-key
    name: Staging
    api_version: v5
```

### API Version

- **v3** (default) — For self-hosted Signoz instances (`/api/v3/query_range`)
- **v5** — For Signoz Cloud (`/api/v5/query_range`)

## Requirements

- A [Signoz](https://signoz.io) instance (self-hosted or cloud)
- An [Anthropic API key](https://console.anthropic.com/) for AI analysis features

## Contributing

Contributions are welcome! Please open an issue or submit a PR.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

[MIT](LICENSE) © Lester Barahona

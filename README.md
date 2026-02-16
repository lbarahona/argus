# 🔭 Argus

**AI-powered observability CLI for SREs.**

Argus connects to your [Signoz](https://signoz.io) instances and uses Anthropic Claude to analyze logs, metrics, and traces with natural language queries.

> *"Why is latency high on the payments service?"* — Just ask Argus.

---

## Features

- 🤖 **Natural language queries** — Ask questions about your infrastructure in plain English
- 📡 **Multi-instance support** — Manage multiple Signoz environments (production, staging, etc.)
- ⚡ **Streaming AI responses** — Real-time analysis output as tokens arrive
- 🎨 **Beautiful terminal UI** — Clean, colorful output designed for SREs
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

# 3. Query logs with AI analysis
argus logs auth-service --query "any errors in the last hour?"

# 4. Ask free-form questions
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
| `argus logs [service]` | Query and analyze logs |
| `argus ask [question]` | Free-form AI analysis |

### Logs

```bash
# Query logs for a service
argus logs my-service

# With AI analysis
argus logs my-service --query "find authentication failures"

# Specify instance and duration
argus logs my-service -i staging -d 120
```

### Ask

```bash
# Free-form questions about your infrastructure
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
  staging:
    url: https://signoz-staging.example.com
    api_key: your-staging-key
    name: Staging
```

## Requirements

- A [Signoz](https://signoz.io) instance (self-hosted or cloud)
- An [Anthropic API key](https://console.anthropic.com/) for AI analysis

## Contributing

Contributions are welcome! Please open an issue or submit a PR.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'feat: add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

[MIT](LICENSE) © Lester Barahona

# Changelog

All notable changes to Argus will be documented in this file.

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

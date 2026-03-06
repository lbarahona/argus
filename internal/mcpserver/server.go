package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/alert"
	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/deploy"
	"github.com/lbarahona/argus/internal/diff"
	"github.com/lbarahona/argus/internal/explain"
	"github.com/lbarahona/argus/internal/report"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/internal/slo"
	topkg "github.com/lbarahona/argus/internal/top"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run starts the MCP server on stdio.
func Run(ctx context.Context, version string) error {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "argus",
			Version: version,
		},
		&mcp.ServerOptions{},
	)

	registerTools(server)

	return server.Run(ctx, &mcp.StdioTransport{})
}

// Tool input types

type InstanceInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"the Signoz instance name to query (uses default if empty)"`
}

type StatusInput struct{}

type ServicesInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"the Signoz instance name to query"`
}

type LogsInput struct {
	Service  string `json:"service,omitempty" jsonschema:"service name to filter logs"`
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to look back (default 60)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max log entries to return (default 100)"`
	Severity string `json:"severity,omitempty" jsonschema:"filter by severity: ERROR, WARN, INFO, DEBUG"`
	Query    string `json:"query,omitempty" jsonschema:"natural language query for AI analysis of the logs"`
}

type TracesInput struct {
	Service  string `json:"service,omitempty" jsonschema:"service name to filter traces"`
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to look back (default 60)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max traces to return (default 100)"`
	Query    string `json:"query,omitempty" jsonschema:"natural language query for AI analysis"`
}

type MetricsInput struct {
	Metric   string `json:"metric,omitempty" jsonschema:"metric name to query"`
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to look back (default 60)"`
	Query    string `json:"query,omitempty" jsonschema:"natural language query for AI analysis"`
}

type AskInput struct {
	Question string `json:"question" jsonschema:"natural language question about your infrastructure"`
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name for context"`
}

type ExplainInput struct {
	Service  string `json:"service" jsonschema:"service name to analyze"`
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to analyze (default 60)"`
}

type DashboardInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to look back for errors (default 60)"`
}

type ReportInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to cover (default 60)"`
	WithAI   bool   `json:"with_ai,omitempty" jsonschema:"include AI-generated summary"`
}

type TopInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Limit    int    `json:"limit,omitempty" jsonschema:"number of services to show (default 20)"`
	SortBy   string `json:"sort_by,omitempty" jsonschema:"sort by: errors, rate, calls, name (default errors)"`
	Duration int    `json:"duration,omitempty" jsonschema:"minutes to look back (default 60)"`
}

type DiffInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Duration int    `json:"duration,omitempty" jsonschema:"duration per window in minutes (default 60)"`
}

type AlertCheckInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
}

type SLOCheckInput struct {
	Instance string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
}

type DeployInput struct {
	Instance    string `json:"instance,omitempty" jsonschema:"Signoz instance name"`
	Service     string `json:"service,omitempty" jsonschema:"filter to specific service"`
	Duration    int    `json:"duration,omitempty" jsonschema:"time window in minutes (default 360 = 6h)"`
	Sensitivity string `json:"sensitivity,omitempty" jsonschema:"detection sensitivity: low, medium, or high (default medium)"`
}

// Helper to load config and get client
func getClient(instance string) (*signoz.Client, string, *types.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", nil, fmt.Errorf("loading config: %w", err)
	}
	inst, instKey, err := config.GetInstance(cfg, instance)
	if err != nil {
		return nil, "", nil, err
	}
	return signoz.New(*inst), instKey, cfg, nil
}

func defaults(val, def int) int {
	if val <= 0 {
		return def
	}
	return val
}

func textResult(text string) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil
}

func errorResult(err error) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Error: %v", err)}},
		IsError: true,
	}, nil
}

func jsonText(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func registerTools(server *mcp.Server) {
	// status
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_status",
		Description: "Check health of all configured Signoz instances",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input StatusInput) (*mcp.CallToolResult, struct{}, error) {
		cfg, err := config.Load()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if len(cfg.Instances) == 0 {
			r, _ := textResult("No instances configured.")
			return r, struct{}{}, nil
		}
		var statuses []types.HealthStatus
		for key, inst := range cfg.Instances {
			client := signoz.New(inst)
			healthy, latency, healthErr := client.Health(ctx)
			s := types.HealthStatus{
				InstanceName: inst.Name, InstanceKey: key, URL: inst.URL,
				Healthy: healthy, Latency: latency,
			}
			if healthErr != nil {
				s.Message = healthErr.Error()
			}
			statuses = append(statuses, s)
		}
		r, _ := textResult(jsonText(statuses))
		return r, struct{}{}, nil
	})

	// services
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_services",
		Description: "List services from Signoz with call counts and error rates",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ServicesInput) (*mcp.CallToolResult, struct{}, error) {
		client, _, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		services, err := client.ListServices(ctx)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		r, _ := textResult(jsonText(services))
		return r, struct{}{}, nil
	})

	// logs
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_logs",
		Description: "Query logs from Signoz, optionally filtered by service and severity. Can include AI analysis.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input LogsInput) (*mcp.CallToolResult, struct{}, error) {
		client, instKey, cfg, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		result, err := client.QueryLogs(ctx, input.Service, defaults(input.Duration, 60), defaults(input.Limit, 100), input.Severity)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if input.Query != "" && cfg.AnthropicKey != "" {
			dataCtx := result.Raw
			if len(result.Logs) > 0 {
				dataCtx = formatLogsForAI(result.Logs)
			}
			prompt := fmt.Sprintf("User query: %s\n\nObservability data from Signoz instance %q:\n%s", input.Query, instKey, dataCtx)
			analyzer := ai.New(cfg.AnthropicKey)
			analysis, err := analyzer.AnalyzeSync(prompt)
			if err != nil {
				r, _ := errorResult(err)
				return r, struct{}{}, nil
			}
			r, _ := textResult(analysis)
			return r, struct{}{}, nil
		}
		r, _ := textResult(jsonText(result.Logs))
		return r, struct{}{}, nil
	})

	// traces
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_traces",
		Description: "Query distributed traces from Signoz, optionally filtered by service",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input TracesInput) (*mcp.CallToolResult, struct{}, error) {
		client, instKey, cfg, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		result, err := client.QueryTraces(ctx, input.Service, defaults(input.Duration, 60), defaults(input.Limit, 100))
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if input.Query != "" && cfg.AnthropicKey != "" {
			prompt := fmt.Sprintf("User query: %s\n\nTrace data from Signoz instance %q:\n%s", input.Query, instKey, result.Raw)
			analyzer := ai.New(cfg.AnthropicKey)
			analysis, err := analyzer.AnalyzeSync(prompt)
			if err != nil {
				r, _ := errorResult(err)
				return r, struct{}{}, nil
			}
			r, _ := textResult(analysis)
			return r, struct{}{}, nil
		}
		r, _ := textResult(jsonText(result.Traces))
		return r, struct{}{}, nil
	})

	// metrics
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_metrics",
		Description: "Query metrics from Signoz by metric name",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input MetricsInput) (*mcp.CallToolResult, struct{}, error) {
		client, instKey, cfg, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		result, err := client.QueryMetrics(ctx, input.Metric, defaults(input.Duration, 60))
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if input.Query != "" && cfg.AnthropicKey != "" {
			prompt := fmt.Sprintf("User query: %s\n\nMetric data from Signoz instance %q:\n%s", input.Query, instKey, result.Raw)
			analyzer := ai.New(cfg.AnthropicKey)
			analysis, err := analyzer.AnalyzeSync(prompt)
			if err != nil {
				r, _ := errorResult(err)
				return r, struct{}{}, nil
			}
			r, _ := textResult(analysis)
			return r, struct{}{}, nil
		}
		r, _ := textResult(jsonText(result.Metrics))
		return r, struct{}{}, nil
	})

	// ask
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_ask",
		Description: "Ask a free-form question about your infrastructure. AI analyzes live Signoz data to answer.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AskInput) (*mcp.CallToolResult, struct{}, error) {
		cfg, err := config.Load()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if cfg.AnthropicKey == "" {
			r, _ := errorResult(fmt.Errorf("Anthropic API key not configured"))
			return r, struct{}{}, nil
		}
		client, instKey, _, err := getClient(input.Instance)
		contextInfo := ""
		if err == nil {
			if services, err := client.ListServices(ctx); err == nil && len(services) > 0 {
				contextInfo += fmt.Sprintf("\n\nServices in %s:\n", instKey)
				for _, svc := range services {
					contextInfo += fmt.Sprintf("- %s (calls: %d, errors: %d, error rate: %.1f%%)\n",
						svc.Name, svc.NumCalls, svc.NumErrors, svc.ErrorRate)
				}
			}
			if result, err := client.QueryLogs(ctx, "", 30, 20, "ERROR"); err == nil && len(result.Logs) > 0 {
				contextInfo += "\nRecent errors:\n"
				for _, log := range result.Logs {
					body := log.Body
					if len(body) > 200 {
						body = body[:200]
					}
					contextInfo += fmt.Sprintf("- [%s] %s: %s\n",
						log.Timestamp.Format("15:04:05"), log.ServiceName, body)
				}
			}
		}
		prompt := input.Question + contextInfo
		analyzer := ai.New(cfg.AnthropicKey)
		analysis, err := analyzer.AnalyzeSync(prompt)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		r, _ := textResult(analysis)
		return r, struct{}{}, nil
	})

	// explain
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_explain",
		Description: "AI-powered root cause analysis: correlates logs, traces, and metrics for a service",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ExplainInput) (*mcp.CallToolResult, struct{}, error) {
		cfg, err := config.Load()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		if cfg.AnthropicKey == "" {
			r, _ := errorResult(fmt.Errorf("Anthropic API key not configured"))
			return r, struct{}{}, nil
		}
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		data, err := explain.Collect(ctx, client, instKey, explain.Options{
			Service:      input.Service,
			Duration:     defaults(input.Duration, 60),
			AnthropicKey: cfg.AnthropicKey,
		})
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		prompt := explain.BuildPrompt(data)
		analyzer := ai.New(cfg.AnthropicKey)
		analysis, err := analyzer.AnalyzeSync(prompt)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		summary := fmt.Sprintf("Collected: %d error logs, %d recent logs, %d traces\n\n%s",
			len(data.ErrorLogs), len(data.RecentLogs), len(data.Traces), analysis)
		r, _ := textResult(summary)
		return r, struct{}{}, nil
	})

	// dashboard
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_dashboard",
		Description: "Quick overview: instance health, top services, and recent errors",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DashboardInput) (*mcp.CallToolResult, struct{}, error) {
		cfg, err := config.Load()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		var statuses []types.HealthStatus
		for key, inst := range cfg.Instances {
			client := signoz.New(inst)
			healthy, latency, healthErr := client.Health(ctx)
			s := types.HealthStatus{
				InstanceName: inst.Name, InstanceKey: key, URL: inst.URL,
				Healthy: healthy, Latency: latency,
			}
			if healthErr != nil {
				s.Message = healthErr.Error()
			}
			statuses = append(statuses, s)
		}
		client, _, _, err := getClient(input.Instance)
		var services []types.Service
		var errorLogs []types.LogEntry
		if err == nil {
			if svcs, err := client.ListServices(ctx); err == nil {
				services = svcs
			}
			if result, err := client.QueryLogs(ctx, "", defaults(input.Duration, 60), 20, "ERROR"); err == nil {
				errorLogs = result.Logs
			}
		}
		out := map[string]any{
			"health":       statuses,
			"services":     services,
			"recent_errors": errorLogs,
		}
		r, _ := textResult(jsonText(out))
		return r, struct{}{}, nil
	})

	// report
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_report",
		Description: "Generate a health report for shift handoffs with optional AI summary",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ReportInput) (*mcp.CallToolResult, struct{}, error) {
		cfg, err := config.Load()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		rpt, err := report.Generate(ctx, client, instKey, report.Options{
			Duration:     defaults(input.Duration, 60),
			WithAI:       input.WithAI,
			Format:       "markdown",
			AnthropicKey: cfg.AnthropicKey,
		})
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		var sb strings.Builder
		rpt.RenderMarkdown(&sb)
		r, _ := textResult(sb.String())
		return r, struct{}{}, nil
	})

	// top
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_top",
		Description: "Show top services ranked by errors, error rate, or call volume",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input TopInput) (*mcp.CallToolResult, struct{}, error) {
		var sf topkg.SortField
		switch strings.ToLower(input.SortBy) {
		case "rate":
			sf = topkg.SortByErrorRate
		case "calls":
			sf = topkg.SortByCalls
		case "name":
			sf = topkg.SortByName
		default:
			sf = topkg.SortByErrors
		}
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		result, err := topkg.Run(ctx, client, instKey, topkg.Options{
			Limit:    defaults(input.Limit, 20),
			SortBy:   sf,
			Duration: defaults(input.Duration, 60),
		})
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		var sb strings.Builder
		result.RenderTerminal(&sb)
		r, _ := textResult(sb.String())
		return r, struct{}{}, nil
	})

	// diff
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_diff",
		Description: "Compare error rates between two time windows to detect anomalies",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DiffInput) (*mcp.CallToolResult, struct{}, error) {
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		result, err := diff.Compare(ctx, client, instKey, diff.Options{
			Duration: defaults(input.Duration, 60),
		})
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		var sb strings.Builder
		result.RenderTerminal(&sb)
		r, _ := textResult(sb.String())
		return r, struct{}{}, nil
	})

	// alert check
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_alert_check",
		Description: "Evaluate all configured alert rules against Signoz and report results",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input AlertCheckInput) (*mcp.CallToolResult, struct{}, error) {
		alertCfg, err := alert.LoadAlerts()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		checker := alert.NewChecker(client, instKey)
		rpt, err := checker.CheckAll(ctx, alertCfg)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		out, err := alert.FormatJSON(rpt)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		r, _ := textResult(out)
		return r, struct{}{}, nil
	})

	// slo check
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_slo_check",
		Description: "Evaluate all configured SLOs and report error budgets, burn rates, and compliance",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SLOCheckInput) (*mcp.CallToolResult, struct{}, error) {
		sloCfg, err := slo.LoadSLOs()
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		checker := slo.NewChecker(client, instKey)
		rpt, err := checker.CheckAll(ctx, sloCfg)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		out, err := slo.FormatJSON(rpt)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		r, _ := textResult(out)
		return r, struct{}{}, nil
	})

	// deploy
	mcp.AddTool(server, &mcp.Tool{
		Name:        "argus_deploy",
		Description: "Detect deployment-like behavioral changes in services and analyze impact. Uses change point detection on error rates and latency to find when services changed behavior.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeployInput) (*mcp.CallToolResult, struct{}, error) {
		client, instKey, _, err := getClient(input.Instance)
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		duration := input.Duration
		if duration <= 0 {
			duration = 360
		}
		sensitivity := input.Sensitivity
		if sensitivity == "" {
			sensitivity = "medium"
		}
		result, err := deploy.Detect(ctx, client, instKey, deploy.Options{
			Duration:    duration,
			Buckets:     12,
			Service:     input.Service,
			Sensitivity: sensitivity,
		})
		if err != nil {
			r, _ := errorResult(err)
			return r, struct{}{}, nil
		}
		r, _ := textResult(jsonText(result))
		return r, struct{}{}, nil
	})
}

func formatLogsForAI(logs []types.LogEntry) string {
	var sb strings.Builder
	for _, log := range logs {
		sb.WriteString(fmt.Sprintf("[%s] %s [%s] %s\n",
			log.Timestamp.Format("2006-01-02 15:04:05"),
			log.SeverityText,
			log.ServiceName,
			log.Body,
		))
	}
	return sb.String()
}

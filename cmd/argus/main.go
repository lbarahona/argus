package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/alert"
	"github.com/lbarahona/argus/internal/anomaly"
	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/correlate"
	"github.com/lbarahona/argus/internal/deps"
	"github.com/lbarahona/argus/internal/diff"
	"github.com/lbarahona/argus/internal/incident"
	"github.com/lbarahona/argus/internal/mcpserver"
	"github.com/lbarahona/argus/internal/deploy"
	pmlib "github.com/lbarahona/argus/internal/postmortem"
	"github.com/lbarahona/argus/internal/explain"
	"github.com/lbarahona/argus/internal/forecast"
	"github.com/lbarahona/argus/internal/runbook"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/report"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/internal/slo"
	topkg "github.com/lbarahona/argus/internal/top"
	"github.com/lbarahona/argus/internal/scorecard"
	"github.com/lbarahona/argus/internal/timeline"
	"github.com/lbarahona/argus/internal/tui"
	"github.com/lbarahona/argus/internal/watch"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/spf13/cobra"
	"os/signal"
	"time"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "argus",
		Short: "AI-powered observability CLI for SREs",
		Long:  "Argus connects to Signoz instances and uses Anthropic AI to analyze logs, metrics, and traces with natural language queries.",
	}

	rootCmd.AddCommand(
		versionCmd(),
		configCmd(),
		instancesCmd(),
		statusCmd(),
		logsCmd(),
		askCmd(),
		servicesCmd(),
		tracesCmd(),
		metricsCmd(),
		dashboardCmd(),
		reportCmd(),
		topCmd(),
		diffCmd(),
		watchCmd(),
		alertCmd(),
		explainCmd(),
		sloCmd(),
		anomalyCmd(),
		timelineCmd(),
		tuiCmd(),
		correlateCmd(),
		incidentCmd(),
		runbookCmd(),
		scorecardCmd(),
		forecastCmd(),
		depsCmd(),
		mcpCmd(),
		postmortemCmd(),
		deployCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			output.PrintVersion(version, commit, date)
		},
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Argus configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize configuration interactively",
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.Exists() {
				fmt.Printf("⚠️  Config already exists at %s\n", config.Path())
				fmt.Print("Overwrite? (y/N): ")
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(answer) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			_, err := config.RunInit()
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add-instance",
		Short: "Add a new Signoz instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return config.AddInstance(cfg)
		},
	})

	return cmd
}

func instancesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "instances",
		Short: "List configured Signoz instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			output.PrintInstances(cfg.Instances, cfg.DefaultInstance)
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check health of all configured instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.Instances) == 0 {
				fmt.Println(output.WarningStyle.Render("No instances configured. Run: argus config init"))
				return nil
			}

			ctx := context.Background()
			var statuses []types.HealthStatus

			for key, inst := range cfg.Instances {
				client := signoz.New(inst)
				healthy, latency, healthErr := client.Health(ctx)

				s := types.HealthStatus{
					InstanceName: inst.Name,
					InstanceKey:  key,
					URL:          inst.URL,
					Healthy:      healthy,
					Latency:      latency,
				}
				if healthErr != nil {
					s.Message = healthErr.Error()
				}
				statuses = append(statuses, s)
			}

			output.PrintHealthStatuses(statuses)
			return nil
		},
	}
}

func logsCmd() *cobra.Command {
	var query string
	var instance string
	var duration int
	var limit int
	var severity string

	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Query and analyze logs",
		Long:  "Query logs from Signoz and optionally analyze them with AI.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Querying logs from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))

			result, err := client.QueryLogs(ctx, service, duration, limit, severity)
			if err != nil {
				return fmt.Errorf("querying logs: %w", err)
			}

			// If we have a query, send to AI for analysis
			if query != "" && cfg.AnthropicKey != "" {
				output.PrintAnalyzing(query)

				dataContext := result.Raw
				if len(result.Logs) > 0 {
					dataContext = formatLogsForAI(result.Logs)
				}

				prompt := fmt.Sprintf("User query: %s\n\nObservability data from Signoz instance %q:\n%s",
					query, instKey, dataContext)

				analyzer := ai.New(cfg.AnthropicKey)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			// Print formatted logs
			output.PrintLogs(result.Logs)
			return nil
		},
	}

	cmd.Flags().StringVarP(&query, "query", "q", "", "Natural language query for AI analysis")
	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query (default: default instance)")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().IntVarP(&limit, "limit", "l", 100, "Maximum number of log entries")
	cmd.Flags().StringVarP(&severity, "severity", "s", "", "Filter by severity (ERROR, WARN, INFO, DEBUG)")

	return cmd
}

func servicesCmd() *cobra.Command {
	var instance string

	cmd := &cobra.Command{
		Use:   "services",
		Short: "List services from Signoz",
		Long:  "List all services discovered by Signoz with call counts and error rates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Fetching services from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))

			services, err := client.ListServices(ctx)
			if err != nil {
				return fmt.Errorf("listing services: %w", err)
			}

			output.PrintServices(services)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")

	return cmd
}

func tracesCmd() *cobra.Command {
	var instance string
	var duration int
	var limit int
	var query string

	cmd := &cobra.Command{
		Use:   "traces [service]",
		Short: "Query traces from Signoz",
		Long:  "Query distributed traces from Signoz, optionally filtered by service.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Querying traces from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))

			result, err := client.QueryTraces(ctx, service, duration, limit)
			if err != nil {
				return fmt.Errorf("querying traces: %w", err)
			}

			// If we have a query, send to AI
			if query != "" && cfg.AnthropicKey != "" {
				output.PrintAnalyzing(query)

				prompt := fmt.Sprintf("User query: %s\n\nTrace data from Signoz instance %q:\n%s",
					query, instKey, result.Raw)

				analyzer := ai.New(cfg.AnthropicKey)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			output.PrintTraces(result.Traces)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().IntVarP(&limit, "limit", "l", 100, "Maximum number of traces")
	cmd.Flags().StringVarP(&query, "query", "q", "", "Natural language query for AI analysis")

	return cmd
}

func metricsCmd() *cobra.Command {
	var instance string
	var duration int
	var query string

	cmd := &cobra.Command{
		Use:   "metrics [metric_name]",
		Short: "Query metrics from Signoz",
		Long:  "Query metrics from Signoz by metric name.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			metricName := ""
			if len(args) > 0 {
				metricName = args[0]
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Querying metrics from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))

			result, err := client.QueryMetrics(ctx, metricName, duration)
			if err != nil {
				return fmt.Errorf("querying metrics: %w", err)
			}

			if query != "" && cfg.AnthropicKey != "" {
				output.PrintAnalyzing(query)

				prompt := fmt.Sprintf("User query: %s\n\nMetric data from Signoz instance %q:\n%s",
					query, instKey, result.Raw)

				analyzer := ai.New(cfg.AnthropicKey)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			output.PrintMetrics(result.Metrics)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().StringVarP(&query, "query", "q", "", "Natural language query for AI analysis")

	return cmd
}

func dashboardCmd() *cobra.Command {
	var instance string
	var duration int

	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Quick overview dashboard",
		Long:  "Display a combined view of instance health, top services, and recent errors.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			ctx := context.Background()

			// Collect health statuses from all instances
			var statuses []types.HealthStatus
			for key, inst := range cfg.Instances {
				client := signoz.New(inst)
				healthy, latency, healthErr := client.Health(ctx)
				s := types.HealthStatus{
					InstanceName: inst.Name,
					InstanceKey:  key,
					URL:          inst.URL,
					Healthy:      healthy,
					Latency:      latency,
				}
				if healthErr != nil {
					s.Message = healthErr.Error()
				}
				statuses = append(statuses, s)
			}

			// Get services and recent errors from the target instance
			inst, _, err := config.GetInstance(cfg, instance)
			var services []types.Service
			var recentLogs []types.LogEntry

			if err == nil {
				client := signoz.New(*inst)

				if svcs, err := client.ListServices(ctx); err == nil {
					services = svcs
				}

				if result, err := client.QueryLogs(ctx, "", duration, 20, "ERROR"); err == nil {
					recentLogs = result.Logs
				}
			}

			output.PrintDashboard(statuses, services, recentLogs)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance for services/logs")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back for errors")

	return cmd
}

func askCmd() *cobra.Command {
	var instance string

	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask a free-form question about your infrastructure",
		Long:  "Use AI to analyze your observability data and answer questions about your infrastructure.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if cfg.AnthropicKey == "" {
				return fmt.Errorf("Anthropic API key not configured. Run: argus config init")
			}

			question := strings.Join(args, " ")
			output.PrintAnalyzing(question)

			// Gather context from Signoz
			inst, instKey, _ := config.GetInstance(cfg, instance)
			contextInfo := ""
			if inst != nil {
				client := signoz.New(*inst)
				ctx := context.Background()

				// Try to get services for context
				if services, err := client.ListServices(ctx); err == nil && len(services) > 0 {
					contextInfo += fmt.Sprintf("\n\nServices in %s:\n", instKey)
					for _, svc := range services {
						contextInfo += fmt.Sprintf("- %s (calls: %d, errors: %d, error rate: %.1f%%)\n",
							svc.Name, svc.NumCalls, svc.NumErrors, svc.ErrorRate)
					}
				}

				// Try to get recent error logs
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

				if contextInfo == "" {
					contextInfo = fmt.Sprintf("\n\nConnected Signoz instance: %s (%s)", instKey, inst.URL)
				}
			}

			prompt := question + contextInfo

			analyzer := ai.New(cfg.AnthropicKey)
			return analyzer.Analyze(prompt, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance for context")

	return cmd
}

func reportCmd() *cobra.Command {
	var instance string
	var duration int
	var withAI bool
	var format string

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a health report for shift handoffs",
		Long:  "Compile a comprehensive health report including service status, error patterns, and optional AI summary. Perfect for shift handoffs and incident reviews.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()
			fmt.Printf("%s Generating health report...\n", output.MutedStyle.Render("⏳"))

			r, err := report.Generate(ctx, client, instKey, report.Options{
				Duration:     duration,
				WithAI:       withAI,
				Format:       format,
				AnthropicKey: cfg.AnthropicKey,
			})
			if err != nil {
				return err
			}

			if format == "markdown" {
				r.RenderMarkdown(os.Stdout)
			} else {
				r.RenderTerminal(os.Stdout)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to report on")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to cover")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-generated summary (uses Anthropic API)")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output format: terminal or markdown")

	return cmd
}

func topCmd() *cobra.Command {
	var instance string
	var limit int
	var sortBy string
	var duration int

	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show top services by errors, like htop for your services",
		Long:  "Display a ranked view of services sorted by errors, error rate, or call volume. Quick triage tool for on-call SREs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			var sf topkg.SortField
			switch strings.ToLower(sortBy) {
			case "errors":
				sf = topkg.SortByErrors
			case "rate":
				sf = topkg.SortByErrorRate
			case "calls":
				sf = topkg.SortByCalls
			case "name":
				sf = topkg.SortByName
			default:
				sf = topkg.SortByErrors
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()
			fmt.Printf("%s Fetching service data...\n", output.MutedStyle.Render("⏳"))

			result, err := topkg.Run(ctx, client, instKey, topkg.Options{
				Limit:    limit,
				SortBy:   sf,
				Duration: duration,
			})
			if err != nil {
				return err
			}

			result.RenderTerminal(os.Stdout)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&limit, "limit", "l", 20, "Number of services to show")
	cmd.Flags().StringVarP(&sortBy, "sort", "s", "errors", "Sort by: errors, rate, calls, name")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes for recent error lookup")

	return cmd
}

func diffCmd() *cobra.Command {
	var instance string
	var duration int

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare error rates between two time windows",
		Long:  "Compare the current time window against the previous window to detect anomalies. Shows which services are degrading, improving, or stable.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()
			fmt.Printf("%s Comparing time windows...\n", output.MutedStyle.Render("⏳"))

			result, err := diff.Compare(ctx, client, instKey, diff.Options{
				Duration: duration,
			})
			if err != nil {
				return err
			}

			result.RenderTerminal(os.Stdout)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration per window in minutes (compares last N min vs previous N min)")

	return cmd
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

func watchCmd() *cobra.Command {
	var instance string
	var interval int
	var errWarn, errCrit, p99Warn, p99Crit, spike float64

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuously monitor services and alert on anomalies",
		Long: `Watch mode polls your Signoz instance at regular intervals and alerts
on error rate spikes, high latency, and new errors. Like htop for your services,
but with anomaly detection.

Thresholds can be customized. Alerts include:
- Error rate exceeding warning/critical thresholds
- P99 latency exceeding warning/critical thresholds  
- Error count spikes compared to rolling baseline
- New errors on previously clean services`,
		Example: `  argus watch
  argus watch --interval 60
  argus watch --error-rate-warn 3 --error-rate-crit 10
  argus watch -i production --p99-warn 1000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			instName := instKey
			if inst.Name != "" {
				instName = inst.Name
			}
			client := signoz.New(*inst)
			thresholds := watch.DefaultThresholds()
			if cmd.Flags().Changed("error-rate-warn") {
				thresholds.ErrorRateWarning = errWarn
			}
			if cmd.Flags().Changed("error-rate-crit") {
				thresholds.ErrorRateCritical = errCrit
			}
			if cmd.Flags().Changed("p99-warn") {
				thresholds.P99Warning = p99Warn
			}
			if cmd.Flags().Changed("p99-crit") {
				thresholds.P99Critical = p99Crit
			}
			if cmd.Flags().Changed("spike") {
				thresholds.ErrorSpike = spike
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			w := watch.New(client, instName, time.Duration(interval)*time.Second, thresholds, os.Stdout)
			return w.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to watch")
	cmd.Flags().IntVar(&interval, "interval", 30, "Poll interval in seconds")
	cmd.Flags().Float64Var(&errWarn, "error-rate-warn", 5, "Error rate % warning threshold")
	cmd.Flags().Float64Var(&errCrit, "error-rate-crit", 15, "Error rate % critical threshold")
	cmd.Flags().Float64Var(&p99Warn, "p99-warn", 2000, "P99 latency ms warning threshold")
	cmd.Flags().Float64Var(&p99Crit, "p99-crit", 5000, "P99 latency ms critical threshold")
	cmd.Flags().Float64Var(&spike, "spike", 3, "Error spike multiplier over baseline")

	return cmd
}

func alertCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alert",
		Short: "Manage and evaluate alert rules",
		Long: `Define alert rules in ~/.argus/alerts.yaml and evaluate them against your
Signoz instances. Perfect for cron jobs and CI pipelines.

Exit codes: 0 = all OK, 1 = warnings, 2 = critical alerts found.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create sample alert rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := alert.InitAlerts(); err != nil {
				return err
			}
			fmt.Println("✅ Sample alert rules created at ~/.argus/alerts.yaml")
			fmt.Println("   Edit the file to customize rules, then run: argus alert check")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured alert rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := alert.LoadAlerts()
			if err != nil {
				return err
			}
			fmt.Printf("\n🔔 Alert Rules (%d configured)\n\n", len(cfg.Rules))
			for i, rule := range cfg.Rules {
				enabled := "✅"
				if !rule.IsEnabled() {
					enabled = "⏸️"
				}
				svc := rule.Service
				if svc == "" {
					svc = "all services"
				}
				fmt.Printf("  %s %d. %s\n", enabled, i+1, rule.Name)
				if rule.Description != "" {
					fmt.Printf("     %s\n", rule.Description)
				}
				fmt.Printf("     Type: %s | Target: %s | Warning: %.1f | Critical: %.1f\n\n",
					rule.Type, svc, rule.Warning, rule.Critical)
			}
			return nil
		},
	})

	var instance string
	var format string
	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Evaluate all alert rules against Signoz",
		Long: `Evaluate all enabled alert rules and report results.

Use --format json for machine-readable output (great for cron jobs).
Exit code reflects highest severity: 0=ok, 1=warning, 2=critical.`,
		Example: `  argus alert check
  argus alert check --format json
  argus alert check -i production
  argus alert check --format json | jq '.summary'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			alertCfg, err := alert.LoadAlerts()
			if err != nil {
				return err
			}
			appCfg, err := config.Load()
			if err != nil {
				return err
			}
			inst, instKey, err := config.GetInstance(appCfg, instance)
			if err != nil {
				return err
			}
			ctx := context.Background()
			client := signoz.New(*inst)
			if format != "json" {
				fmt.Printf("%s Checking alerts against %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))
			}
			checker := alert.NewChecker(client, instKey)
			rpt, err := checker.CheckAll(ctx, alertCfg)
			if err != nil {
				return err
			}
			if format == "json" {
				out, err := alert.FormatJSON(rpt)
				if err != nil {
					return err
				}
				fmt.Println(out)
			} else {
				fmt.Print(alert.FormatText(rpt))
			}
			os.Exit(rpt.ExitCode())
			return nil
		},
	}
	checkCmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to check against")
	checkCmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text or json")
	cmd.AddCommand(checkCmd)

	return cmd
}

func sloCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "slo",
		Short: "Track and evaluate Service Level Objectives",
		Long: `Define SLOs in ~/.argus/slos.yaml and evaluate them against your
Signoz instances. Track error budgets, burn rates, and compliance.

Perfect for SLO reviews, on-call handoffs, and dashboards.
Exit codes: 0 = all OK, 1 = warnings, 2 = critical/exhausted.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create sample SLO definitions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := slo.InitSLOs(); err != nil {
				return err
			}
			fmt.Println("✅ Sample SLO definitions created at ~/.argus/slos.yaml")
			fmt.Println("   Edit the file to define your SLOs, then run: argus slo check")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured SLOs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := slo.LoadSLOs()
			if err != nil {
				return err
			}
			fmt.Printf("\n📊 Service Level Objectives (%d configured)\n\n", len(cfg.SLOs))
			for i, s := range cfg.SLOs {
				enabled := "✅"
				if !s.IsEnabled() {
					enabled = "⏸️"
				}
				svc := s.Service
				if svc == "" {
					svc = "all services"
				}
				fmt.Printf("  %s %d. %s\n", enabled, i+1, s.Name)
				if s.Description != "" {
					fmt.Printf("     %s\n", s.Description)
				}
				extra := ""
				if s.Type == "latency" {
					extra = fmt.Sprintf(" (≤%.0fms)", s.Threshold)
				}
				fmt.Printf("     Type: %s | Target: %.2f%% | Window: %s | Service: %s%s\n\n",
					s.Type, s.Target, s.Window, svc, extra)
			}
			return nil
		},
	})

	var instance string
	var format string
	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Evaluate all SLOs against Signoz data",
		Long: `Evaluate all enabled SLOs and report error budgets, burn rates, and compliance.

Use --format json for machine-readable output (great for cron jobs and dashboards).
Exit code reflects worst SLO: 0=ok, 1=warning, 2=critical/exhausted.`,
		Example: `  argus slo check
  argus slo check --format json
  argus slo check -i production
  argus slo check --format json | jq '.results[] | select(.status != "ok")'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sloCfg, err := slo.LoadSLOs()
			if err != nil {
				return err
			}
			appCfg, err := config.Load()
			if err != nil {
				return err
			}
			inst, instKey, err := config.GetInstance(appCfg, instance)
			if err != nil {
				return err
			}
			ctx := context.Background()
			client := signoz.New(*inst)
			if format != "json" {
				fmt.Printf("%s Evaluating SLOs against %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(instKey))
			}
			checker := slo.NewChecker(client, instKey)
			rpt, err := checker.CheckAll(ctx, sloCfg)
			if err != nil {
				return err
			}
			if format == "json" {
				out, err := slo.FormatJSON(rpt)
				if err != nil {
					return err
				}
				fmt.Println(out)
			} else {
				fmt.Print(slo.FormatText(rpt))
			}
			os.Exit(rpt.ExitCode())
			return nil
		},
	}
	checkCmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to check against")
	checkCmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text or json")
	cmd.AddCommand(checkCmd)

	return cmd
}

func explainCmd() *cobra.Command {
	var instance string
	var duration int

	cmd := &cobra.Command{
		Use:   "explain [service]",
		Short: "AI-powered root cause analysis for a service",
		Long: `Correlate logs, traces, and metrics for a service and use AI to
perform root cause analysis. Collects all available observability data,
identifies patterns, and provides actionable recommendations.

Think of it as having a senior SRE look at all your dashboards at once.`,
		Example: `  argus explain api-service
  argus explain payment-service --duration 30
  argus explain auth-service -i production`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.AnthropicKey == "" {
				return fmt.Errorf("Anthropic API key required. Run: argus config init")
			}
			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}
			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Collecting observability data for %s from %s...\n",
				output.MutedStyle.Render("🔍"), output.AccentStyle.Render(args[0]), output.AccentStyle.Render(instKey))

			data, err := explain.Collect(ctx, client, instKey, explain.Options{
				Service:      args[0],
				Duration:     duration,
				AnthropicKey: cfg.AnthropicKey,
			})
			if err != nil {
				return err
			}

			fmt.Printf("%s Collected: %d error logs, %d recent logs, %d traces\n",
				output.MutedStyle.Render("📊"),
				len(data.ErrorLogs), len(data.RecentLogs), len(data.Traces))
			fmt.Printf("%s Analyzing with AI...\n\n", output.MutedStyle.Render("🤖"))

			prompt := explain.BuildPrompt(data)
			analyzer := ai.New(cfg.AnthropicKey)
			return analyzer.Analyze(prompt, os.Stdout)
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to analyze")

	return cmd
}

func anomalyCmd() *cobra.Command {
	var instance string
	var duration int
	var sensitivity float64
	var service string
	var withAI bool
	var quiet bool

	cmd := &cobra.Command{
		Use:   "anomaly",
		Short: "Detect anomalies across services",
		Long:  "Automatically detect anomalies in error rates, log patterns, and latency using statistical analysis (z-score, percentiles) with optional AI root cause analysis.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			opts := anomaly.Options{
				Duration:     duration,
				Sensitivity:  sensitivity,
				Service:      service,
				WithAI:       withAI,
				AnthropicKey: cfg.AnthropicKey,
				Quiet:        quiet,
			}

			fmt.Printf("🔍 Scanning for anomalies (last %d min, sensitivity=%.1f)...\n\n", duration, sensitivity)

			result, err := anomaly.Detect(ctx, client, instKey, opts)
			if err != nil {
				return err
			}

			// Convert to output types
			outResult := output.AnomalyResult{
				ScanTime:     result.ScanTime,
				Duration:     result.Duration,
				Instance:     result.Instance,
				AISummary:    result.AISummary,
				TotalScanned: result.TotalScanned,
			}

			for _, a := range result.Anomalies {
				outResult.Anomalies = append(outResult.Anomalies, output.AnomalyItem{
					Type:        a.Type,
					Severity:    a.Severity,
					Service:     a.Service,
					Metric:      a.Metric,
					Description: a.Description,
					Value:       a.Value,
					Expected:    a.Expected,
					Deviation:   a.Deviation,
					DetectedAt:  a.DetectedAt,
					Window:      a.Window,
				})
			}

			for _, s := range result.Services {
				outResult.Services = append(outResult.Services, output.AnomalyServiceScan{
					Name:            s.Name,
					Calls:           s.Calls,
					Errors:          s.Errors,
					ErrorRate:       s.ErrorRate,
					AnomalyCount:    s.AnomalyCount,
					HighestSeverity: s.HighestSeverity,
				})
			}

			output.PrintAnomalyResult(outResult, quiet)
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to use")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Time window in minutes to analyze")
	cmd.Flags().Float64VarP(&sensitivity, "sensitivity", "s", 2.0, "Z-score threshold for anomaly detection (lower = more sensitive)")
	cmd.Flags().StringVar(&service, "service", "", "Scan a specific service only")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI root cause analysis")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only show anomalies (hide healthy services)")

	return cmd
}

func timelineCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var withAI bool
	var format string

	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Reconstruct an incident timeline from observability data",
		Long: `Automatically correlate errors, latency spikes, and service health
into a chronological incident timeline. Perfect for incident review,
postmortems, and shift handoffs.

Analyzes logs, traces, and service health to detect:
  - Error spikes (sudden increases in error rate)
  - New error patterns (unique errors appearing)
  - Latency spikes (P99 outliers)
  - Service degradation (high error rates)

Use --ai to generate an AI-powered incident narrative.`,
		Example: `  argus timeline
  argus timeline --duration 120
  argus timeline --service api-service --ai
  argus timeline --format markdown > incident-report.md
  argus timeline -i production --duration 30 --ai`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}
			client := signoz.New(*inst)
			ctx := context.Background()

			opts := timeline.Options{
				Duration:     duration,
				Service:      service,
				WithAI:       withAI,
				Format:       format,
				AnthropicKey: cfg.AnthropicKey,
			}

			if withAI && cfg.AnthropicKey == "" {
				return fmt.Errorf("Anthropic API key required for --ai. Run: argus config init")
			}

			tl, err := timeline.Generate(ctx, client, instKey, opts)
			if err != nil {
				return err
			}

			switch format {
			case "markdown":
				tl.RenderMarkdown(os.Stdout)
			default:
				tl.RenderTerminal(os.Stdout)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to use")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Time window in minutes")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to a specific service")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Generate AI incident narrative")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output format (terminal, markdown)")

	return cmd
}

func tuiCmd() *cobra.Command {
	var instance string
	var maxHistory int

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive AI-powered troubleshooting session",
		Long: `Start an interactive session connected to a Signoz instance.
Ask questions in natural language and get AI-powered analysis with full
conversation context. The AI automatically gathers live data from Signoz
with each question.

Perfect for extended troubleshooting sessions where you need to drill
down into issues with follow-up questions.`,
		Example: `  argus tui
  argus tui -i production
  argus tui --max-history 40`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if cfg.AnthropicKey == "" {
				return fmt.Errorf("Anthropic API key required. Run: argus config init")
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			instName := instKey
			if inst.Name != "" {
				instName = inst.Name
			}

			client := signoz.New(*inst)

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			session := tui.New(client, tui.Options{
				InstanceKey:  instKey,
				InstanceName: instName,
				AnthropicKey: cfg.AnthropicKey,
				MaxHistory:   maxHistory,
			})

			return session.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to connect to")
	cmd.Flags().IntVar(&maxHistory, "max-history", 20, "Maximum conversation messages to retain")

	return cmd
}

func correlateCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var bucketSize int
	var minEvents int
	var useAI bool
	var markdown bool

	cmd := &cobra.Command{
		Use:   "correlate",
		Short: "Cross-signal correlation across services",
		Long: `Analyze logs, traces, and metrics across all services to find temporal
correlations, error propagation patterns, and causal chains.

Unlike 'explain' (which focuses on one service), 'correlate' looks at the
entire system to find how issues spread between services and identifies
the root cause in a cascade.`,
		Example: `  argus correlate
  argus correlate --service api-gateway
  argus correlate --duration 30 --ai
  argus correlate --bucket 30 --min-events 5
  argus correlate --markdown`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}
			client := signoz.New(*inst)
			ctx := context.Background()

			opts := correlate.Options{
				Duration:     duration,
				Service:      service,
				BucketSize:   bucketSize,
				MinEvents:    minEvents,
				AnthropicKey: cfg.AnthropicKey,
			}

			if useAI {
				if cfg.AnthropicKey == "" {
					return fmt.Errorf("Anthropic API key required for AI analysis. Run: argus config init")
				}
				fmt.Printf("%s Collecting signals from %s...\n",
					output.MutedStyle.Render("🔍"), output.AccentStyle.Render(instKey))
				return correlate.RunWithAI(ctx, client, instKey, opts, os.Stdout)
			}

			fmt.Printf("%s Collecting signals from %s...\n",
				output.MutedStyle.Render("🔍"), output.AccentStyle.Render(instKey))

			result, err := correlate.Run(ctx, client, instKey, opts)
			if err != nil {
				return err
			}

			if markdown {
				fmt.Print(correlate.RenderMarkdown(result))
			} else {
				correlate.Render(result)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to analyze")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Focus on a specific service (default: all)")
	cmd.Flags().IntVar(&bucketSize, "bucket", 60, "Time bucket size in seconds for clustering")
	cmd.Flags().IntVar(&minEvents, "min-events", 3, "Minimum events to form a cluster")
	cmd.Flags().BoolVar(&useAI, "ai", false, "Include AI-powered correlation analysis")
	cmd.Flags().BoolVar(&markdown, "markdown", false, "Output as markdown")

	return cmd
}

func incidentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "incident",
		Short: "Manage incidents with timelines and status tracking",
		Long: `Track incidents from creation to resolution. Incidents are stored locally
in ~/.argus/incidents.yaml and can be managed entirely from the CLI.

Perfect for on-call SREs who need to track incidents during shifts.`,
	}

	// create
	var severity, commander, description string
	var services []string
	createCmd := &cobra.Command{
		Use:   "create [title]",
		Short: "Create a new incident",
		Args:  cobra.MinimumNArgs(1),
		Example: `  argus incident create "API returning 500s" --severity critical --services api-service
  argus incident create "Latency spike" -s major --commander lester
  argus incident create "DB connection pool exhausted" -s critical --services db,api`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			title := strings.Join(args, " ")
			inc := store.Create(title, severity, services, commander, description)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n🚨 Incident created: %s\n", output.AccentStyle.Render(inc.ID))
			fmt.Printf("   Title: %s\n", inc.Title)
			fmt.Printf("   Severity: %s %s\n", incident.SeverityIcon(inc.Severity), inc.Severity)
			if len(services) > 0 {
				fmt.Printf("   Services: %s\n", strings.Join(services, ", "))
			}
			fmt.Printf("\n   Update: argus incident update %s --status investigating --message \"looking into it\"\n\n", inc.ID)
			return nil
		},
	}
	createCmd.Flags().StringVarP(&severity, "severity", "s", "major", "Severity: critical, major, minor")
	createCmd.Flags().StringSliceVar(&services, "services", nil, "Affected services (comma-separated)")
	createCmd.Flags().StringVarP(&commander, "commander", "c", "", "Incident commander")
	createCmd.Flags().StringVarP(&description, "description", "d", "", "Description")
	cmd.AddCommand(createCmd)

	// list
	var all bool
	var limit int
	var format string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List incidents (active by default)",
		Example: `  argus incident list
  argus incident list --all
  argus incident list --limit 5
  argus incident list --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			var incidents []incident.Incident
			var title string
			if all {
				incidents = store.RecentIncidents(limit)
				title = "📋 All Incidents"
			} else {
				incidents = store.ActiveIncidents()
				title = "🚨 Active Incidents"
			}
			if format == "json" {
				out, err := incident.FormatJSON(incidents)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}
			incident.RenderList(incidents, title)
			return nil
		},
	}
	listCmd.Flags().BoolVarP(&all, "all", "a", false, "Show all incidents (including resolved)")
	listCmd.Flags().IntVarP(&limit, "limit", "l", 20, "Max incidents to show (with --all)")
	listCmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text or json")
	cmd.AddCommand(listCmd)

	// update
	var updateStatus, message, author string
	updateCmd := &cobra.Command{
		Use:   "update [incident-id]",
		Short: "Update incident status with a timeline entry",
		Args:  cobra.ExactArgs(1),
		Example: `  argus incident update INC-20260222-001 --status investigating --message "checking logs"
  argus incident update INC-20260222-001 --status identified --message "root cause: bad deploy"
  argus incident update INC-20260222-001 --status monitoring --message "rollback applied"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc := store.FindByID(args[0])
			if inc == nil {
				return fmt.Errorf("incident %q not found", args[0])
			}
			if updateStatus == "" {
				return fmt.Errorf("--status is required (investigating, identified, monitoring, resolved)")
			}
			inc.Update(updateStatus, message, author)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n✅ Updated %s → %s %s\n", output.AccentStyle.Render(inc.ID),
				incident.StatusIcon(updateStatus), updateStatus)
			if message != "" {
				fmt.Printf("   %s\n", message)
			}
			fmt.Println()
			return nil
		},
	}
	updateCmd.Flags().StringVar(&updateStatus, "status", "", "New status: investigating, identified, monitoring, resolved")
	updateCmd.Flags().StringVarP(&message, "message", "m", "", "Timeline message")
	updateCmd.Flags().StringVarP(&author, "author", "a", "", "Author of the update")
	cmd.AddCommand(updateCmd)

	// resolve (shortcut for update --status resolved)
	var resolveMsg string
	resolveCmd := &cobra.Command{
		Use:   "resolve [incident-id]",
		Short: "Resolve an incident",
		Args:  cobra.ExactArgs(1),
		Example: `  argus incident resolve INC-20260222-001 --message "deployed fix in v2.3.1"
  argus incident resolve INC-20260222-001 -m "false alarm"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc := store.FindByID(args[0])
			if inc == nil {
				return fmt.Errorf("incident %q not found", args[0])
			}
			msg := resolveMsg
			if msg == "" {
				msg = "Incident resolved"
			}
			inc.Update(incident.StatusResolved, msg, "")
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n✅ Resolved %s — %s\n", output.AccentStyle.Render(inc.ID), inc.Title)
			fmt.Printf("   Duration: %s\n\n", inc.Duration)
			return nil
		},
	}
	resolveCmd.Flags().StringVarP(&resolveMsg, "message", "m", "", "Resolution message")
	cmd.AddCommand(resolveCmd)

	// timeline
	timelineCmd := &cobra.Command{
		Use:     "timeline [incident-id]",
		Short:   "Show detailed timeline for an incident",
		Args:    cobra.ExactArgs(1),
		Example: "  argus incident timeline INC-20260222-001",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc := store.FindByID(args[0])
			if inc == nil {
				return fmt.Errorf("incident %q not found", args[0])
			}
			incident.RenderTimeline(inc)
			return nil
		},
	}
	cmd.AddCommand(timelineCmd)

	return cmd
}
func runbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runbook",
		Short: "Manage and execute operational runbooks",
		Long: `Create, manage, and execute operational runbooks for incident response,
maintenance procedures, and troubleshooting workflows.

Runbooks are stored as YAML files in ~/.argus/runbooks/ and can be shared
across teams via version control.`,
	}

	// init
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create sample runbooks to get started",
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			existing, _ := store.List()
			if len(existing) > 0 {
				fmt.Printf("⚠️  Found %d existing runbooks in %s\n", len(existing), store.Dir())
				fmt.Print("Add sample runbooks anyway? (y/N): ")
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(answer) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			if err := runbook.InitSamples(store); err != nil {
				return err
			}
			fmt.Printf("✅ Created 5 sample runbooks in %s\n", store.Dir())
			fmt.Println("   Run: argus runbook list")
			return nil
		},
	})

	// list
	var format string
	var category string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all runbooks",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rbs, err := store.List()
			if err != nil {
				return err
			}

			if category != "" {
				var filtered []*runbook.Runbook
				for _, rb := range rbs {
					if strings.EqualFold(rb.Category, category) {
						filtered = append(filtered, rb)
					}
				}
				rbs = filtered
			}

			if format == "json" {
				out, err := runbook.FormatJSON(rbs)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}

			runbook.PrintList(os.Stdout, rbs)
			return nil
		},
	}
	listCmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text or json")
	listCmd.Flags().StringVarP(&category, "category", "c", "", "Filter by category")
	cmd.AddCommand(listCmd)

	// show
	cmd.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Show runbook details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}
			runbook.PrintShow(os.Stdout, rb)
			return nil
		},
	})

	// search
	cmd.AddCommand(&cobra.Command{
		Use:   "search <query>",
		Short: "Search runbooks by name, description, tags, or category",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			results, err := store.Search(args[0])
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Printf("\n🔍 No runbooks matching %q\n\n", args[0])
				return nil
			}
			fmt.Printf("\n🔍 Search results for %q:\n", args[0])
			runbook.PrintList(os.Stdout, results)
			return nil
		},
	})

	// delete
	cmd.AddCommand(&cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a runbook",
		Aliases: []string{"rm"},
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Delete runbook %q? (y/N): ", rb.Name)
			var answer string
			fmt.Scanln(&answer)
			if strings.ToLower(answer) != "y" {
				fmt.Println("Aborted.")
				return nil
			}
			if err := store.Delete(args[0]); err != nil {
				return err
			}
			fmt.Printf("✅ Deleted: %s\n", rb.Name)
			return nil
		},
	})

	// validate
	cmd.AddCommand(&cobra.Command{
		Use:   "validate <id>",
		Short: "Validate a runbook's structure",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}

			issues := validateRunbook(rb)
			if len(issues) == 0 {
				fmt.Printf("✅ Runbook %q is valid (%d steps)\n", rb.Name, len(rb.Steps))
				return nil
			}

			fmt.Printf("⚠️  Runbook %q has %d issues:\n\n", rb.Name, len(issues))
			for _, issue := range issues {
				fmt.Printf("  • %s\n", issue)
			}
			return nil
		},
	})

	// run (dry-run mode — shows steps without executing)
	var dryRun bool
	runCmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Execute a runbook step-by-step (dry-run by default)",
		Long: `Walk through a runbook step by step. By default runs in dry-run mode,
showing each step's command without executing. Commands contain placeholders
(like <POD>, <NS>) that should be filled in for your specific situation.

Use --execute to actually run commands (use with caution).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}

			log := &runbook.RunLog{
				RunbookID:   rb.ID,
				RunbookName: rb.Name,
				StartedAt:   time.Now(),
				Status:      "running",
			}

			fmt.Printf("\n🚀 Running: %s", rb.Name)
			if dryRun {
				fmt.Print(" [DRY RUN]")
			}
			fmt.Printf("\n   %d steps\n\n", len(rb.Steps))

			allPassed := true
			for i, step := range rb.Steps {
				result := runbook.StepResult{
					StepName:  step.Name,
					StartedAt: time.Now(),
				}

				prefix := fmt.Sprintf("[%d/%d]", i+1, len(rb.Steps))
				if step.Manual {
					fmt.Printf("  %s 🖐️  %s [MANUAL]\n", prefix, step.Name)
				} else {
					fmt.Printf("  %s ⚡ %s\n", prefix, step.Name)
				}

				if step.Command != "" {
					fmt.Printf("       $ %s\n", step.Command)
				}
				if step.Notes != "" {
					fmt.Printf("       💡 %s\n", step.Notes)
				}

				if dryRun {
					result.Status = "skipped"
					fmt.Printf("       %s\n\n", output.MutedStyle.Render("(dry-run: skipped)"))
				} else if step.Manual {
					fmt.Print("       Continue? (y/n/skip): ")
					var answer string
					fmt.Scanln(&answer)
					switch strings.ToLower(answer) {
					case "y", "yes":
						result.Status = "passed"
					case "skip", "s":
						result.Status = "skipped"
					default:
						result.Status = "failed"
						result.Error = "manual step rejected"
						allPassed = false
					}
					fmt.Println()
				} else {
					result.Status = "passed"
					fmt.Println()
				}

				log.StepResults = append(log.StepResults, result)

				if !allPassed && rb.OnFailure == "escalate" {
					fmt.Println("  ⚠️  Step failed — on_failure=escalate, stopping execution")
					break
				}
			}

			log.CompletedAt = time.Now()
			if allPassed {
				log.Status = "completed"
			} else {
				log.Status = "failed"
			}

			runbook.PrintRunLog(os.Stdout, log)
			return nil
		},
	}
	runCmd.Flags().BoolVar(&dryRun, "dry-run", true, "Show steps without executing (default: true)")
	cmd.AddCommand(runCmd)

	return cmd
}

func validateRunbook(rb *runbook.Runbook) []string {
	var issues []string

	if rb.Name == "" {
		issues = append(issues, "missing name")
	}
	if len(rb.Steps) == 0 {
		issues = append(issues, "no steps defined")
	}
	for i, step := range rb.Steps {
		if step.Name == "" {
			issues = append(issues, fmt.Sprintf("step %d: missing name", i+1))
		}
		if step.Command == "" && step.Check == "" && !step.Manual {
			issues = append(issues, fmt.Sprintf("step %d (%s): no command, check, or manual flag", i+1, step.Name))
		}
	}
	if rb.Severity != "" {
		valid := map[string]bool{"P1": true, "P2": true, "P3": true, "P4": true}
		if !valid[strings.ToUpper(rb.Severity)] {
			issues = append(issues, fmt.Sprintf("invalid severity %q (use P1-P4)", rb.Severity))
		}
	}
	return issues
}

func scorecardCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var withAI bool
	var format string

	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "Generate a service reliability scorecard",
		Long:  "Grade each service on reliability (error rate, latency, trends) and produce an overall score. Use for weekly reviews, shift handoffs, or SLA reporting.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()
			fmt.Printf("%s Generating reliability scorecard...\n", output.MutedStyle.Render("⏳"))

			sc, err := scorecard.Generate(ctx, client, instKey, scorecard.Options{
				Duration:     duration,
				Service:      service,
				WithAI:       withAI,
				Format:       format,
				AnthropicKey: cfg.AnthropicKey,
			})
			if err != nil {
				return err
			}

			if format == "markdown" {
				scorecard.RenderMarkdown(os.Stdout, sc)
			} else {
				scorecard.RenderTerminal(os.Stdout, sc)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to analyze")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to a single service")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-generated summary (uses Anthropic API)")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output format: terminal or markdown")

	return cmd
}

func forecastCmd() *cobra.Command {
	var instance string
	var duration int
	var horizon int
	var service string
	var format string
	var withAI bool

	cmd := &cobra.Command{
		Use:   "forecast",
		Short: "Predict service health trends using linear regression",
		Long: `Analyze historical error rates and traffic patterns to forecast service health.
Uses linear regression on time-bucketed metrics to predict future error rates,
detect degrading services, and warn about potential incidents before they happen.

Risk levels:
  stable    (score <30)  — No significant issues expected
  degrading (score 30-59) — Trending in wrong direction, monitor closely
  critical  (score 60+)  — Likely to cause issues, take action now`,
		Example: `  argus forecast
  argus forecast --duration 240 --horizon 120
  argus forecast -s api-service --ai
  argus forecast -f markdown > forecast.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Analyzing trends from %s (last %dm, forecasting %dm)...\n",
				output.MutedStyle.Render("🔮"), output.AccentStyle.Render(instKey), duration, horizon)

			r, err := forecast.Generate(ctx, client, instKey, forecast.Options{
				Duration:     duration,
				Horizon:      horizon,
				Service:      service,
				Format:       format,
				WithAI:       withAI,
				AnthropicKey: cfg.AnthropicKey,
			})
			if err != nil {
				return err
			}

			if format == "markdown" {
				r.RenderMarkdown(os.Stdout)
			} else {
				r.RenderTerminal(os.Stdout)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 120, "Historical duration in minutes to analyze")
	cmd.Flags().IntVar(&horizon, "horizon", 60, "Forecast horizon in minutes")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to specific service")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output format: terminal or markdown")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-powered analysis (uses Anthropic API)")

	return cmd
}

func depsCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var format string
	var withAI bool

	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Map service dependencies from trace data",
		Long:  "Discover upstream and downstream service dependencies by analyzing trace spans. Shows call volumes, error rates, and latency between services. Outputs an ASCII dependency graph and optional Mermaid diagram.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()
			fmt.Printf("%s Mapping service dependencies...\n", output.MutedStyle.Render("⏳"))

			dm, err := deps.Generate(ctx, deps.Options{
				Querier:  client,
				Instance: instKey,
				Duration: duration,
				Service:  service,
				Format:   format,
				AI:       withAI,
				AIKey:    cfg.AnthropicKey,
				Writer:   os.Stdout,
			})
			if err != nil {
				return err
			}

			if format == "markdown" {
				deps.RenderMarkdown(os.Stdout, dm)
			} else {
				deps.RenderTable(os.Stdout, dm)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to analyze")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to show only deps for this service")
	cmd.Flags().StringVarP(&format, "format", "f", "table", "Output format: table or markdown")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI architecture analysis")

	return cmd
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP (Model Context Protocol) server",
		Long: `Start an MCP server over stdio, exposing Argus observability tools
to AI agents and LLM applications.

Supported tools: status, services, logs, traces, metrics, ask, explain,
dashboard, report, top, diff, alert_check, slo_check.

Configure in Claude Desktop, Cursor, or any MCP client:
  {
    "mcpServers": {
      "argus": {
        "command": "argus",
        "args": ["mcp"]
      }
    }
  }`,
		Example: `  argus mcp
  echo '{"jsonrpc":"2.0","method":"initialize",...}' | argus mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()
			return mcpserver.Run(ctx, version)
		},
	}
}

func postmortemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "postmortem",
		Aliases: []string{"pm"},
		Short:   "Generate and manage blameless postmortems from incidents",
		Long:    "Auto-generate structured postmortem documents from incidents, enriched with Signoz metrics and AI-powered root cause analysis.",
	}

	cmd.AddCommand(
		postmortemGenerateCmd(),
		postmortemListCmd(),
		postmortemShowCmd(),
		postmortemExportCmd(),
		postmortemDeleteCmd(),
	)

	return cmd
}

func postmortemGenerateCmd() *cobra.Command {
	var useAI bool
	var format string

	cmd := &cobra.Command{
		Use:   "generate <incident-id>",
		Short: "Generate a postmortem from an incident",
		Long:  "Collects incident timeline, Signoz metrics, error logs, and optionally runs AI analysis to produce a structured postmortem document.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			incidentID := args[0]

			// Check if postmortem already exists for this incident
			store, err := pmlib.Load()
			if err != nil {
				return err
			}
			if existing := store.FindByIncidentID(incidentID); existing != nil {
				fmt.Println(output.WarningStyle.Render(fmt.Sprintf("⚠️  Postmortem %s already exists for incident %s", existing.ID, incidentID)))
				fmt.Println("  Use 'argus postmortem show " + existing.ID + "' to view it.")
				return nil
			}

			// Try to get Signoz querier
			var querier signoz.SignozQuerier
			cfg, err := config.Load()
			if err == nil {
				inst, _, err := config.GetInstance(cfg, "")
				if err == nil {
					querier = signoz.New(*inst)
				}
			}

			// Get AI key
			var aiKey string
			if useAI {
				if cfg != nil {
					aiKey = cfg.AnthropicKey
				}
				if aiKey == "" {
					aiKey = os.Getenv("ANTHROPIC_API_KEY")
				}
				if aiKey == "" {
					fmt.Println(output.WarningStyle.Render("⚠️  No Anthropic API key found. Skipping AI analysis."))
					useAI = false
				}
			}

			fmt.Println(output.AccentStyle.Render("📋 Generating postmortem for incident " + incidentID + "..."))
			if useAI {
				fmt.Println(output.AccentStyle.Render("🤖 AI analysis enabled"))
			}
			if querier != nil {
				fmt.Println(output.AccentStyle.Render("📊 Signoz metrics enrichment enabled"))
			}
			fmt.Println()

			ctx := context.Background()
			pm, err := pmlib.Generate(ctx, pmlib.Options{
				IncidentID: incidentID,
				UseAI:      useAI,
				AIKey:      aiKey,
				Format:     format,
				Querier:    querier,
			})
			if err != nil {
				return err
			}

			// Save to store
			store.Postmortems = append(store.Postmortems, *pm)
			if err := store.Save(); err != nil {
				return fmt.Errorf("saving postmortem: %w", err)
			}

			fmt.Println(output.SuccessStyle.Render(fmt.Sprintf("✅ Postmortem %s generated and saved", pm.ID)))
			fmt.Println()

			// Display based on format
			switch format {
			case "markdown", "md":
				fmt.Println(pmlib.RenderMarkdown(pm))
			case "json":
				out, err := pmlib.FormatJSON(pm)
				if err != nil {
					return err
				}
				fmt.Println(out)
			default:
				pmlib.RenderTerminal(pm)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&useAI, "ai", false, "Enable AI-powered root cause analysis and action items")
	cmd.Flags().StringVar(&format, "format", "terminal", "Output format: terminal, markdown, json")

	return cmd
}

func postmortemListCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all postmortems",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := pmlib.Load()
			if err != nil {
				return err
			}

			pms := store.RecentPostmortems(limit)
			pmlib.RenderList(pms)
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "Max postmortems to show (0=all)")

	return cmd
}

func postmortemShowCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "show <postmortem-id>",
		Short: "Display a postmortem",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := pmlib.Load()
			if err != nil {
				return err
			}

			pm := store.FindByID(args[0])
			if pm == nil {
				// Try finding by incident ID
				pm = store.FindByIncidentID(args[0])
			}
			if pm == nil {
				return fmt.Errorf("postmortem %q not found", args[0])
			}

			switch format {
			case "markdown", "md":
				fmt.Println(pmlib.RenderMarkdown(pm))
			case "json":
				out, err := pmlib.FormatJSON(pm)
				if err != nil {
					return err
				}
				fmt.Println(out)
			default:
				pmlib.RenderTerminal(pm)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "terminal", "Output format: terminal, markdown, json")

	return cmd
}

func postmortemExportCmd() *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "export <postmortem-id>",
		Short: "Export a postmortem to a markdown file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := pmlib.Load()
			if err != nil {
				return err
			}

			pm := store.FindByID(args[0])
			if pm == nil {
				pm = store.FindByIncidentID(args[0])
			}
			if pm == nil {
				return fmt.Errorf("postmortem %q not found", args[0])
			}

			md := pmlib.RenderMarkdown(pm)

			if outputPath == "" {
				outputPath = fmt.Sprintf("postmortem-%s.md", pm.IncidentID)
			}

			if err := os.WriteFile(outputPath, []byte(md), 0o644); err != nil {
				return fmt.Errorf("writing file: %w", err)
			}

			fmt.Println(output.SuccessStyle.Render(fmt.Sprintf("✅ Exported to %s", outputPath)))
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output file path (default: postmortem-<incident-id>.md)")

	return cmd
}

func postmortemDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <postmortem-id>",
		Short: "Delete a postmortem",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := pmlib.Load()
			if err != nil {
				return err
			}

			found := false
			var filtered []pmlib.Postmortem
			for _, pm := range store.Postmortems {
				if pm.ID == args[0] {
					found = true
					continue
				}
				filtered = append(filtered, pm)
			}

			if !found {
				return fmt.Errorf("postmortem %q not found", args[0])
			}

			store.Postmortems = filtered
			if err := store.Save(); err != nil {
				return err
			}

			fmt.Println(output.SuccessStyle.Render(fmt.Sprintf("✅ Deleted postmortem %s", args[0])))
			return nil
		},
	}
}

func deployCmd() *cobra.Command {
	var instance string
	var duration int
	var buckets int
	var service string
	var sensitivity string
	var format string
	var withAI bool

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Detect deployments from behavioral changes and analyze impact",
		Long: `Analyze service behavior to detect deployment-like changes and assess their impact.

Uses change point detection (binary segmentation) on time-bucketed metrics to find
moments where service behavior shifted significantly — typically caused by deployments,
config changes, or infrastructure events.

Detection methods:
  • Error rate change points (CUSUM-inspired binary segmentation)
  • P99 latency shifts
  • New error pattern emergence

Sensitivity levels:
  high   — Flag small changes (15%+ error rate shift)
  medium — Balanced detection (30%+ error rate shift)
  low    — Only major changes (50%+ error rate shift)

Impact scoring: -100 (severe regression) to +100 (significant improvement)`,
		Example: `  argus deploy
  argus deploy --duration 720 --sensitivity high
  argus deploy -s payment-api --ai
  argus deploy -f markdown > deploy-report.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			inst, instKey, err := config.GetInstance(cfg, instance)
			if err != nil {
				return err
			}

			client := signoz.New(*inst)
			ctx := context.Background()

			fmt.Printf("%s Scanning %s for deployment changes (last %dm, %s sensitivity)...\n",
				output.MutedStyle.Render("🚀"), output.AccentStyle.Render(instKey), duration, sensitivity)

			r, err := deploy.Detect(ctx, client, instKey, deploy.Options{
				Duration:     duration,
				Buckets:      buckets,
				Service:      service,
				Sensitivity:  sensitivity,
				Format:       format,
				WithAI:       withAI,
				AnthropicKey: cfg.AnthropicKey,
			})
			if err != nil {
				return err
			}

			if format == "markdown" {
				r.RenderMarkdown(os.Stdout)
			} else {
				r.RenderTerminal(os.Stdout)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to query")
	cmd.Flags().IntVarP(&duration, "duration", "d", 360, "Time window in minutes to analyze (default: 6h)")
	cmd.Flags().IntVarP(&buckets, "buckets", "b", 12, "Number of time buckets for analysis")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to specific service")
	cmd.Flags().StringVar(&sensitivity, "sensitivity", "medium", "Detection sensitivity: low, medium, high")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output format: terminal or markdown")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-powered deployment analysis (uses Anthropic API)")

	return cmd
}

// ──────────────────────────────────────────────

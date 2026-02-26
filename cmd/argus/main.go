package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/alert"
	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/diff"
	"github.com/lbarahona/argus/internal/explain"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/report"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/internal/slo"
	topkg "github.com/lbarahona/argus/internal/top"
	"github.com/lbarahona/argus/internal/scorecard"
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
		tuiCmd(),
		scorecardCmd(),
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

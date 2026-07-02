package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/spf13/cobra"
)

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

			keys := make([]string, 0, len(cfg.Instances))
			for k := range cfg.Instances {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				inst := cfg.Instances[key]
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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			// A query is an explicit request for AI — validate it's
			// available before doing the (potentially slow) Signoz fetch,
			// so a missing key errors instantly instead of after the query.
			var provider ai.Provider
			if query != "" {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			fmt.Printf("%s Querying logs from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))

			result, err := sctx.client.QueryLogs(ctx, service, duration, limit, severity)
			if err != nil {
				return fmt.Errorf("querying logs: %w", err)
			}

			// If we have a query, send to AI for analysis.
			if query != "" {
				output.PrintAnalyzing(query)

				dataContext := result.Raw
				if len(result.Logs) > 0 {
					dataContext = formatLogsForAI(result.Logs)
				}

				prompt := fmt.Sprintf("User query: %s\n\nObservability data from Signoz instance %q:\n%s",
					query, sctx.instKey, dataContext)

				analyzer := ai.NewFromProvider(provider)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			// Print formatted logs
			output.PrintLogs(result.Logs)
			return nil
		},
	}

	cmd.Flags().StringVarP(&query, "query", "q", "", "Natural language query for AI analysis")
	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to look back (e.g. 90, 90m, 2h)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 100, "Maximum number of log entries")
	cmd.Flags().StringVar(&severity, "severity", "", "Filter by severity (ERROR, WARN, INFO, DEBUG)")

	return cmd
}

func servicesCmd() *cobra.Command {
	var instance string

	cmd := &cobra.Command{
		Use:   "services",
		Short: "List services from Signoz",
		Long:  "List all services discovered by Signoz with call counts and error rates.",
		RunE: func(cmd *cobra.Command, args []string) error {
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			ctx := context.Background()

			fmt.Printf("%s Fetching services from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))

			services, err := sctx.client.ListServices(ctx)
			if err != nil {
				return fmt.Errorf("listing services: %w", err)
			}

			output.PrintServices(services)
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)

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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			// A query is an explicit request for AI — validate it's
			// available before doing the (potentially slow) Signoz fetch,
			// so a missing key errors instantly instead of after the query.
			var provider ai.Provider
			if query != "" {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			service := ""
			if len(args) > 0 {
				service = args[0]
			}

			ctx := context.Background()

			fmt.Printf("%s Querying traces from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))

			result, err := sctx.client.QueryTraces(ctx, service, duration, limit)
			if err != nil {
				return fmt.Errorf("querying traces: %w", err)
			}

			// If we have a query, send to AI.
			if query != "" {
				output.PrintAnalyzing(query)

				prompt := fmt.Sprintf("User query: %s\n\nTrace data from Signoz instance %q:\n%s",
					query, sctx.instKey, result.Raw)

				analyzer := ai.NewFromProvider(provider)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			output.PrintTraces(result.Traces)
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to look back (e.g. 90, 90m, 2h)")
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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			// A query is an explicit request for AI — validate it's
			// available before doing the (potentially slow) Signoz fetch,
			// so a missing key errors instantly instead of after the query.
			var provider ai.Provider
			if query != "" {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			metricName := ""
			if len(args) > 0 {
				metricName = args[0]
			}

			ctx := context.Background()

			fmt.Printf("%s Querying metrics from %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))

			result, err := sctx.client.QueryMetrics(ctx, metricName, duration)
			if err != nil {
				return fmt.Errorf("querying metrics: %w", err)
			}

			// A query is an explicit request for AI.
			if query != "" {
				output.PrintAnalyzing(query)

				prompt := fmt.Sprintf("User query: %s\n\nMetric data from Signoz instance %q:\n%s",
					query, sctx.instKey, result.Raw)

				analyzer := ai.NewFromProvider(provider)
				return analyzer.Analyze(prompt, os.Stdout)
			}

			output.PrintMetrics(result.Metrics)
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to look back (e.g. 90, 90m, 2h)")
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
			// Resolve the target instance first so a bad -i (or a missing
			// default with no instances configured) errors out immediately,
			// before spending time sweeping every configured instance for
			// health.
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			ctx := context.Background()

			// Collect health statuses from all instances
			var statuses []types.HealthStatus
			keys := make([]string, 0, len(sctx.cfg.Instances))
			for k := range sctx.cfg.Instances {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				inst := sctx.cfg.Instances[key]
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

			// Get services and recent errors from the target instance.
			var services []types.Service
			var recentLogs []types.LogEntry

			if svcs, err := sctx.client.ListServices(ctx); err == nil {
				services = svcs
			}

			if result, err := sctx.client.QueryLogs(ctx, "", duration, 20, "ERROR"); err == nil {
				recentLogs = result.Logs
			}

			output.PrintDashboard(statuses, services, recentLogs)
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to look back for errors (e.g. 90, 90m, 2h)")

	return cmd
}

func askCmd() *cobra.Command {
	var instance string

	cmd := &cobra.Command{
		Use:   "ask [question]",
		Short: "Ask a free-form question about your infrastructure",
		Long:  "Use AI (Anthropic, OpenAI, or Bedrock) to analyze your observability data and answer questions about your infrastructure.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve the instance first (a bad -i errors instead of
			// silently producing output with missing context), then
			// require AI — ask can't function without it.
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			provider, err := requireAI(sctx.cfg)
			if err != nil {
				return err
			}

			question := strings.Join(args, " ")
			output.PrintAnalyzing(question)

			contextInfo := ""
			ctx := context.Background()

			// Try to get services for context
			if services, err := sctx.client.ListServices(ctx); err == nil && len(services) > 0 {
				contextInfo += fmt.Sprintf("\n\nServices in %s:\n", sctx.instKey)
				for _, svc := range services {
					contextInfo += fmt.Sprintf("- %s (calls: %d, errors: %d, error rate: %.1f%%)\n",
						svc.Name, svc.NumCalls, svc.NumErrors, svc.ErrorRate)
				}
			}

			// Try to get recent error logs
			if result, err := sctx.client.QueryLogs(ctx, "", 30, 20, "ERROR"); err == nil && len(result.Logs) > 0 {
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
				contextInfo = fmt.Sprintf("\n\nConnected Signoz instance: %s (%s)", sctx.instKey, sctx.inst.URL)
			}

			prompt := question + contextInfo

			analyzer := ai.NewFromProvider(provider)
			return analyzer.Analyze(prompt, os.Stdout)
		},
	}

	addInstanceFlag(cmd, &instance)

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

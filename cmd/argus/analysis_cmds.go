package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/anomaly"
	"github.com/lbarahona/argus/internal/correlate"
	"github.com/lbarahona/argus/internal/deploy"
	"github.com/lbarahona/argus/internal/deps"
	"github.com/lbarahona/argus/internal/diff"
	"github.com/lbarahona/argus/internal/explain"
	"github.com/lbarahona/argus/internal/forecast"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/report"
	"github.com/lbarahona/argus/internal/scorecard"
	"github.com/lbarahona/argus/internal/timeline"
	"github.com/lbarahona/argus/internal/watch"
	"github.com/spf13/cobra"
)

func reportCmd() *cobra.Command {
	var instance string
	var duration int
	var withAI bool
	var format string
	var grade bool
	var service string
	fmts := formatSet{Markdown: true}

	cmd := &cobra.Command{
		Use:   "report",
		Short: "Generate a health report for shift handoffs",
		Long: `Compile a comprehensive health report including service status, error patterns, and optional AI summary. Perfect for shift handoffs and incident reviews.

With --grade, generates a service reliability scorecard instead: grades each
service on reliability (error rate, latency, trends) and produces an overall
score. Use for weekly reviews, shift handoffs, or SLA reporting.`,
		Example: `  argus report
  argus report --ai -d 2h
  argus report --grade
  argus report --grade --service api-gateway
  argus report --format markdown`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			if service != "" && !grade {
				return fmt.Errorf("--service is only valid with --grade")
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()

			if grade {
				fmt.Printf("%s Generating reliability scorecard...\n", output.MutedStyle.Render("⏳"))

				sc, err := scorecard.Generate(ctx, sctx.client, sctx.instKey, scorecard.Options{
					Duration:   duration,
					Service:    service,
					WithAI:     withAI,
					AIProvider: provider,
				})
				if err != nil {
					return err
				}

				return renderOutput(format, fmts, func() error {
					scorecard.RenderTerminal(os.Stdout, sc)
					return nil
				}, func() error {
					scorecard.RenderMarkdown(os.Stdout, sc)
					return nil
				}, nil)
			}

			fmt.Printf("%s Generating health report...\n", output.MutedStyle.Render("⏳"))

			r, err := report.Generate(ctx, sctx.client, sctx.instKey, report.Options{
				Duration:   duration,
				WithAI:     withAI,
				AIProvider: provider,
			})
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				r.RenderTerminal(os.Stdout)
				return nil
			}, func() error {
				r.RenderMarkdown(os.Stdout)
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to cover (e.g. 90, 90m, 2h)")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-generated summary (uses Anthropic API)")
	cmd.Flags().BoolVar(&grade, "grade", false, "Generate a service reliability scorecard instead of a health report")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to a single service (only valid with --grade)")
	addFormatFlag(cmd, &format, "terminal", fmts)

	return cmd
}

func diffCmd() *cobra.Command {
	var instance string
	var duration int

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare error rates between two time windows",
		Long:  "Compare the current time window against the previous window to detect anomalies. Shows which services are degrading, improving, or stable.",
		Example: `  argus analyze diff
  argus analyze diff --duration 30
  argus analyze diff -i production --duration 15`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			ctx := context.Background()
			fmt.Printf("%s Comparing time windows...\n", output.MutedStyle.Render("⏳"))

			result, err := diff.Compare(ctx, sctx.client, sctx.instKey, diff.Options{
				Duration: duration,
			})
			if err != nil {
				return err
			}

			result.RenderTerminal(os.Stdout)
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration per window in minutes (compares last N min vs previous N min) (e.g. 90, 90m, 2h)")

	return cmd
}

func watchCmd() *cobra.Command {
	var instance string
	var interval int
	defaults := watch.DefaultThresholds()
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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			instName := sctx.instKey
			if sctx.inst.Name != "" {
				instName = sctx.inst.Name
			}
			thresholds := defaults
			thresholds.ErrorRateWarning = errWarn
			thresholds.ErrorRateCritical = errCrit
			thresholds.P99Warning = p99Warn
			thresholds.P99Critical = p99Crit
			thresholds.ErrorSpike = spike

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			w := watch.New(sctx.client, instName, time.Duration(interval)*time.Second, thresholds, os.Stdout)
			return w.Run(ctx)
		},
	}

	addInstanceFlag(cmd, &instance)
	cmd.Flags().IntVar(&interval, "interval", 30, "Poll interval in seconds")
	cmd.Flags().Float64Var(&errWarn, "error-rate-warn", defaults.ErrorRateWarning, "Error rate % warning threshold")
	cmd.Flags().Float64Var(&errCrit, "error-rate-crit", defaults.ErrorRateCritical, "Error rate % critical threshold")
	cmd.Flags().Float64Var(&p99Warn, "p99-warn", defaults.P99Warning, "P99 latency ms warning threshold")
	cmd.Flags().Float64Var(&p99Crit, "p99-crit", defaults.P99Critical, "P99 latency ms critical threshold")
	cmd.Flags().Float64Var(&spike, "spike", defaults.ErrorSpike, "Error spike multiplier over baseline")

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
			// explain requires AI to function at all.
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			provider, err := requireAI(sctx.cfg)
			if err != nil {
				return err
			}
			ctx := context.Background()

			fmt.Printf("%s Collecting observability data for %s from %s...\n",
				output.MutedStyle.Render("🔍"), output.AccentStyle.Render(args[0]), output.AccentStyle.Render(sctx.instKey))

			data, err := explain.Collect(ctx, sctx.client, sctx.instKey, explain.Options{
				Service:    args[0],
				Duration:   duration,
				AIProvider: provider,
			})
			if err != nil {
				return err
			}

			fmt.Printf("%s Collected: %d error logs, %d recent logs, %d traces\n",
				output.MutedStyle.Render("📊"),
				len(data.ErrorLogs), len(data.RecentLogs), len(data.Traces))
			fmt.Printf("%s Analyzing with AI...\n\n", output.MutedStyle.Render("🤖"))

			prompt := explain.BuildPrompt(data)
			analyzer := ai.NewFromProvider(provider)
			return analyzer.Analyze(prompt, os.Stdout)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to analyze (e.g. 90, 90m, 2h)")

	return cmd
}

func analyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Investigate system behavior: anomalies, timelines, correlations, changes",
		Long: `Analysis commands that scan services, error logs, and traces:

  anomalies   z-score anomaly detection across services
  timeline    chronological incident-style event timeline
  correlate   cross-signal correlation and error propagation
  changes     deployment/change-point detection
  diff        error-count comparison between two time windows`,
	}
	cmd.AddCommand(anomalyCmd(), timelineCmd(), correlateCmd(), deployCmd(), diffCmd())
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
		Use:   "anomalies",
		Short: "Detect anomalies across services",
		Long:  "Automatically detect anomalies in error rates, log patterns, and latency using statistical analysis (z-score, percentiles) with optional AI root cause analysis.",
		Example: `  argus analyze anomalies
  argus analyze anomalies --duration 120 --sensitivity 1.5
  argus analyze anomalies --service api-service --ai
  argus analyze anomalies --quiet`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()

			opts := anomaly.Options{
				Duration:    duration,
				Sensitivity: sensitivity,
				Service:     service,
				WithAI:      withAI,
				AIProvider:  provider,
				Quiet:       quiet,
			}

			fmt.Printf("🔍 Scanning for anomalies (last %d min, sensitivity=%.1f)...\n\n", duration, sensitivity)

			result, err := anomaly.Detect(ctx, sctx.client, sctx.instKey, opts)
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

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Time window in minutes to analyze (e.g. 90, 90m, 2h)")
	cmd.Flags().Float64Var(&sensitivity, "sensitivity", 2.0, "Z-score threshold for anomaly detection (lower = more sensitive)")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Scan a specific service only")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI root cause analysis")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Only show anomalies (hide healthy services)")

	return cmd
}

func timelineCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var withAI bool
	var format string
	fmts := formatSet{Markdown: true}

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
		Example: `  argus analyze timeline
  argus analyze timeline --duration 120
  argus analyze timeline --service api-service --ai
  argus analyze timeline --format markdown > incident-report.md
  argus analyze timeline -i production --duration 30 --ai`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}
			ctx := context.Background()

			opts := timeline.Options{
				Duration:   duration,
				Service:    service,
				WithAI:     withAI,
				AIProvider: provider,
			}

			tl, err := timeline.Generate(ctx, sctx.client, sctx.instKey, opts)
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				tl.RenderTerminal(os.Stdout)
				return nil
			}, func() error {
				tl.RenderMarkdown(os.Stdout)
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Time window in minutes (e.g. 90, 90m, 2h)")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to a specific service")
	cmd.Flags().BoolVar(&withAI, "ai", false, "Generate AI incident narrative")
	addFormatFlag(cmd, &format, "terminal", fmts)

	return cmd
}

func correlateCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var bucketSize int
	var minEvents int
	var useAI bool
	var format string
	fmts := formatSet{Markdown: true}

	cmd := &cobra.Command{
		Use:   "correlate",
		Short: "Cross-signal correlation across services",
		Long: `Analyze logs, traces, and metrics across all services to find temporal
correlations, error propagation patterns, and causal chains.

Unlike 'explain' (which focuses on one service), 'correlate' looks at the
entire system to find how issues spread between services and identifies
the root cause in a cascade.`,
		Example: `  argus analyze correlate
  argus analyze correlate --service api-gateway
  argus analyze correlate --duration 30 --ai
  argus analyze correlate --bucket 30 --min-events 5
  argus analyze correlate --format markdown
  argus analyze correlate stack --duration 30`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if useAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()

			opts := correlate.Options{
				Duration:   duration,
				Service:    service,
				BucketSize: bucketSize,
				MinEvents:  minEvents,
				AIProvider: provider,
			}

			if useAI {
				fmt.Printf("%s Collecting signals from %s...\n",
					output.MutedStyle.Render("🔍"), output.AccentStyle.Render(sctx.instKey))
				return correlate.RunWithAI(ctx, sctx.client, sctx.instKey, opts, os.Stdout)
			}

			fmt.Printf("%s Collecting signals from %s...\n",
				output.MutedStyle.Render("🔍"), output.AccentStyle.Render(sctx.instKey))

			result, err := correlate.Run(ctx, sctx.client, sctx.instKey, opts)
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				correlate.Render(result)
				return nil
			}, func() error {
				fmt.Print(correlate.RenderMarkdown(result))
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to analyze (e.g. 90, 90m, 2h)")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Focus on a specific service (default: all)")
	cmd.Flags().IntVar(&bucketSize, "bucket", 60, "Time bucket size in seconds for clustering")
	cmd.Flags().IntVar(&minEvents, "min-events", 3, "Minimum events to form a cluster")
	cmd.Flags().BoolVar(&useAI, "ai", false, "Include AI-powered correlation analysis")
	addFormatFlag(cmd, &format, "terminal", fmts)
	cmd.AddCommand(correlateStackCmd())

	return cmd
}

func correlateStackCmd() *cobra.Command {
	var duration int
	var format string
	var logLimit int
	var samples int
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "stack",
		Short: "Correlate active alerts across Alertmanager, Prometheus, Grafana, and Loki",
		Long: `Correlate active alerts across Alertmanager, Prometheus, Grafana, and Loki.

Useful during incident response when you want one view of which services are
screaming across the stack, plus a few related Loki log samples to confirm the
blast radius.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			amClient, err := getAMClient()
			if err != nil {
				return err
			}
			promClient, err := getPromClient()
			if err != nil {
				return err
			}
			grafanaClient, err := getGrafanaClient()
			if err != nil {
				return err
			}
			lokiClient, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 45*time.Second)
			defer cancel()

			result, err := correlate.RunStack(ctx, amClient, promClient, grafanaClient, lokiClient, correlate.StackOptions{
				Duration:    duration,
				LogLimit:    logLimit,
				SampleLimit: samples,
			})
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(correlate.RenderStack(result))
				return nil
			}, nil, result)
		},
	}

	addDurationFlag(cmd, &duration, 60, "Duration in minutes to look back for Loki samples (e.g. 90, 90m, 2h)")
	cmd.Flags().IntVar(&logLimit, "log-limit", 50, "Maximum Loki log entries to scan per service")
	cmd.Flags().IntVar(&samples, "samples", 3, "Maximum Loki log samples to show per correlated group")
	addFormatFlag(cmd, &format, "terminal", fmts)
	return cmd
}

func forecastCmd() *cobra.Command {
	var instance string
	var duration int
	var horizon int
	var service string
	var format string
	var withAI bool
	fmts := formatSet{Markdown: true}

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
			if err := fmts.validate(format); err != nil {
				return err
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()

			fmt.Printf("%s Analyzing trends from %s (last %dm, forecasting %dm)...\n",
				output.MutedStyle.Render("🔮"), output.AccentStyle.Render(sctx.instKey), duration, horizon)

			r, err := forecast.Generate(ctx, sctx.client, sctx.instKey, forecast.Options{
				Duration:   duration,
				Horizon:    horizon,
				Service:    service,
				WithAI:     withAI,
				AIProvider: provider,
			})
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				r.RenderTerminal(os.Stdout)
				return nil
			}, func() error {
				r.RenderMarkdown(os.Stdout)
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 120, "Historical duration in minutes to analyze (e.g. 90, 90m, 2h)")
	cmd.Flags().IntVar(&horizon, "horizon", 60, "Forecast horizon in minutes")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to specific service")
	addFormatFlag(cmd, &format, "terminal", fmts)
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-powered analysis (uses Anthropic API)")

	return cmd
}

func depsCmd() *cobra.Command {
	var instance string
	var duration int
	var service string
	var format string
	var withAI bool
	fmts := formatSet{Markdown: true}

	cmd := &cobra.Command{
		Use:   "deps",
		Short: "Map service dependencies from trace data",
		Long:  "Discover upstream and downstream service dependencies by analyzing trace spans. Shows call volumes, error rates, and latency between services. Outputs an ASCII dependency graph and optional Mermaid diagram.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()
			fmt.Printf("%s Mapping service dependencies...\n", output.MutedStyle.Render("⏳"))

			dm, err := deps.Generate(ctx, deps.Options{
				Querier:    sctx.client,
				Instance:   sctx.instKey,
				Duration:   duration,
				Service:    service,
				AI:         withAI,
				AIProvider: provider,
			})
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				deps.RenderTable(os.Stdout, dm)
				return nil
			}, func() error {
				deps.RenderMarkdown(os.Stdout, dm)
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 60, "Duration in minutes to analyze (e.g. 90, 90m, 2h)")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to show only deps for this service")
	addFormatFlag(cmd, &format, "table", fmts)
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI architecture analysis")

	return cmd
}

func deployCmd() *cobra.Command {
	var instance string
	var duration int
	var buckets int
	var service string
	var sensitivity string
	var format string
	var withAI bool
	fmts := formatSet{Markdown: true}

	cmd := &cobra.Command{
		Use:   "changes",
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
		Example: `  argus analyze changes
  argus analyze changes --duration 720 --sensitivity high
  argus analyze changes -s payment-api --ai
  argus analyze changes -f markdown > deploy-report.md`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			var provider ai.Provider
			if withAI {
				provider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}

			ctx := context.Background()

			fmt.Printf("%s Scanning %s for deployment changes (last %dm, %s sensitivity)...\n",
				output.MutedStyle.Render("🚀"), output.AccentStyle.Render(sctx.instKey), duration, sensitivity)

			r, err := deploy.Detect(ctx, sctx.client, sctx.instKey, deploy.Options{
				Duration:    duration,
				Buckets:     buckets,
				Service:     service,
				Sensitivity: sensitivity,
				WithAI:      withAI,
				AIProvider:  provider,
			})
			if err != nil {
				return err
			}

			return renderOutput(format, fmts, func() error {
				r.RenderTerminal(os.Stdout)
				return nil
			}, func() error {
				r.RenderMarkdown(os.Stdout)
				return nil
			}, nil)
		},
	}

	addInstanceFlag(cmd, &instance)
	addDurationFlag(cmd, &duration, 360, "Time window in minutes to analyze (default: 6h) (e.g. 90, 90m, 2h)")
	cmd.Flags().IntVarP(&buckets, "buckets", "b", 12, "Number of time buckets for analysis")
	cmd.Flags().StringVarP(&service, "service", "s", "", "Filter to specific service")
	cmd.Flags().StringVar(&sensitivity, "sensitivity", "medium", "Detection sensitivity: low, medium, high")
	addFormatFlag(cmd, &format, "terminal", fmts)
	cmd.Flags().BoolVar(&withAI, "ai", false, "Include AI-powered deployment analysis (uses Anthropic API)")

	return cmd
}

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/lbarahona/argus/internal/config"
	lokilib "github.com/lbarahona/argus/internal/loki"
	"github.com/spf13/cobra"
)

// ── Loki Integration ─────────────────────────────────

func getLokiClient() (*lokilib.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if !cfg.Loki.IsConfigured() {
		return nil, fmt.Errorf("loki not configured — add loki.url to your config:\n  argus config init  (or edit ~/.argus/config.yaml)")
	}
	lokiCfg := lokilib.LokiConfig{
		URL:      cfg.Loki.URL,
		TenantID: cfg.Loki.TenantID,
		BasicAuth: lokilib.BasicAuth{
			Username: cfg.Loki.BasicAuth.Username,
			Password: cfg.Loki.BasicAuth.Password,
		},
	}
	return lokilib.NewClient(lokiCfg), nil
}

func lokiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "loki",
		Short:   "Loki integration — query logs, labels, series, and stats",
		Long:    "Query Grafana Loki for log streams, label discovery, series exploration, and ingestion statistics.",
		Aliases: []string{"log"},
	}

	cmd.AddCommand(
		lokiQueryCmd(),
		lokiLabelsCmd(),
		lokiLabelValuesCmd(),
		lokiSeriesCmd(),
		lokiStatsCmd(),
		lokiStatusCmd(),
		lokiSummaryCmd(),
	)

	return cmd
}

func lokiQueryCmd() *cobra.Command {
	var (
		duration int
		limit    int
		format   string
		labels   bool
	)

	cmd := &cobra.Command{
		Use:   "query [logql]",
		Short: "Query logs with LogQL",
		Long: `Run a LogQL query against Loki. Supports log stream selectors and filter expressions.

Examples:
  argus loki query '{app="nginx"}'
  argus loki query '{namespace="production"} |= "error"'
  argus loki query '{job="varlogs"} | json | level="error"' --duration 30
  argus loki query '{app="api"} |~ "5[0-9]{2}"' --limit 50`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			end := time.Now()
			start := end.Add(-time.Duration(duration) * time.Minute)

			fmt.Printf("⏳ Querying Loki (last %dm)...\n", duration)

			result, err := client.QueryRange(ctx, args[0], start, end, limit)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				if result.Data.ResultType != "" && result.Data.ResultType != "streams" {
					fmt.Print(lokilib.FormatMetricSeries(result.Data))
				} else {
					entries := lokilib.ParseEntries(result)
					fmt.Print(lokilib.FormatLogEntries(entries, labels))
				}
				return nil
			}, nil, result)
		},
	}

	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().IntVarP(&limit, "limit", "l", 100, "Maximum number of log entries")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	cmd.Flags().BoolVar(&labels, "labels", false, "Show labels for each log entry")

	return cmd
}

func lokiLabelsCmd() *cobra.Command {
	var (
		duration int
		format   string
	)

	cmd := &cobra.Command{
		Use:   "labels",
		Short: "List all label names",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			end := time.Now()
			start := end.Add(-time.Duration(duration) * time.Minute)

			labelNames, err := client.Labels(ctx, start, end)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatLabels(labelNames))
				return nil
			}, nil, labelNames)
		},
	}

	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")

	return cmd
}

func lokiLabelValuesCmd() *cobra.Command {
	var (
		duration int
		format   string
	)

	cmd := &cobra.Command{
		Use:   "label-values [label]",
		Short: "List values for a label",
		Args:  cobra.ExactArgs(1),
		Example: `  argus loki label-values app
  argus loki label-values namespace --duration 120`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			end := time.Now()
			start := end.Add(-time.Duration(duration) * time.Minute)

			values, err := client.LabelValues(ctx, args[0], start, end)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatLabelValues(args[0], values))
				return nil
			}, nil, values)
		},
	}

	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")

	return cmd
}

func lokiSeriesCmd() *cobra.Command {
	var (
		duration int
		format   string
	)

	cmd := &cobra.Command{
		Use:   "series [matcher...]",
		Short: "Find matching log series",
		Long:  "Query series from Loki matching the given label matchers.",
		Example: `  argus loki series '{app="nginx"}'
  argus loki series '{namespace="production"}' '{job="varlogs"}'`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			end := time.Now()
			start := end.Add(-time.Duration(duration) * time.Minute)

			series, err := client.Series(ctx, args, start, end)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatSeries(series))
				return nil
			}, nil, series)
		},
	}

	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes to look back")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")

	return cmd
}

func lokiStatsCmd() *cobra.Command {
	var (
		duration int
		query    string
		format   string
	)

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show ingestion statistics",
		Long:  "Display index statistics including stream count, chunk count, entry count, and bytes ingested.",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			end := time.Now()
			start := end.Add(-time.Duration(duration) * time.Minute)

			q := query
			if q == "" {
				labels, err := client.Labels(ctx, start, end)
				if err != nil {
					return fmt.Errorf("deriving default query: %w", err)
				}
				q = lokilib.MatchAllSelector(labels)
				if q == "" {
					return fmt.Errorf("no labels found to build a default query; pass -q '{label=~\".+\"}'")
				}
			}

			stats, err := client.IndexStats(ctx, q, start, end)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatStats(stats))
				return nil
			}, nil, stats)
		},
	}

	cmd.Flags().IntVarP(&duration, "duration", "d", 60, "Duration in minutes")
	cmd.Flags().StringVarP(&query, "query", "q", "", "Optional LogQL selector to scope stats (default: derived from your labels)")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")

	return cmd
}

func lokiStatusCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check Loki health and version",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			healthy, latency, _ := client.Healthy(ctx)
			info, _ := client.BuildInfo(ctx)

			data := map[string]interface{}{
				"healthy": healthy,
				"latency": latency.String(),
			}
			if info != nil {
				data["build_info"] = info
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatStatus(healthy, latency, info))
				return nil
			}, nil, data)
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	return cmd
}

func lokiSummaryCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Quick overview of Loki instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getLokiClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			summary, err := client.BuildSummary(ctx)
			if err != nil {
				return fmt.Errorf("building summary: %w", err)
			}

			return renderOutput(format, func() error {
				fmt.Print(lokilib.FormatSummary(summary))
				return nil
			}, nil, summary)
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	return cmd
}

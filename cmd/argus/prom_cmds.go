package main

import (
	"context"
	"fmt"
	"time"

	"github.com/lbarahona/argus/internal/config"
	promlib "github.com/lbarahona/argus/internal/prometheus"
	"github.com/spf13/cobra"
)

// --- Prometheus commands ---

func getPromClient() (*promlib.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if !cfg.Prometheus.IsConfigured() {
		return nil, fmt.Errorf("prometheus not configured — add 'prometheus.url' to ~/.argus.yaml")
	}
	promCfg := promlib.PrometheusConfig{
		URL: cfg.Prometheus.URL,
	}
	if cfg.Prometheus.BasicAuth.Username != "" {
		promCfg.BasicAuth = promlib.BasicAuth{
			Username: cfg.Prometheus.BasicAuth.Username,
			Password: cfg.Prometheus.BasicAuth.Password,
		}
	}
	return promlib.NewClient(promCfg), nil
}

func promCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prom",
		Short:   "Prometheus integration — query rules, targets, alerts, and metrics",
		Long:    "Query Prometheus for alerting/recording rules, scrape targets, firing alerts, and run PromQL queries.",
		Aliases: []string{"prometheus"},
	}

	cmd.AddCommand(
		promRulesCmd(),
		promTargetsCmd(),
		promAlertsCmd(),
		promQueryCmd(),
		promStatusCmd(),
		promSummaryCmd(),
	)

	return cmd
}

func promRulesCmd() *cobra.Command {
	var ruleType string
	var format string

	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List alerting and recording rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			data, err := client.Rules(ctx, ruleType)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(promlib.FormatRules(data, ruleType))
				return nil
			}, nil, data)
		},
	}

	cmd.Flags().StringVar(&ruleType, "type", "", "Filter by rule type: alert, record")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json")
	return cmd
}

func promTargetsCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "targets",
		Short: "Show scrape targets and their health",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			data, err := client.Targets(ctx)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(promlib.FormatTargets(data))
				return nil
			}, nil, data)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json")
	return cmd
}

func promAlertsCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Show firing and pending alerts from Prometheus",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			data, err := client.Alerts(ctx)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(promlib.FormatAlerts(data))
				return nil
			}, nil, data)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json")
	return cmd
}

func promQueryCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "query [promql]",
		Short: "Execute an instant PromQL query",
		Long:  "Run a PromQL query against Prometheus and display the results.\n\nExamples:\n  argus prom query 'up'\n  argus prom query 'rate(http_requests_total[5m])'",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result, err := client.Query(ctx, args[0], nil)
			if err != nil {
				return err
			}

			return renderOutput(format, func() error {
				fmt.Print(promlib.FormatQuery(result))
				return nil
			}, nil, result)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json")
	return cmd
}

func promStatusCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Prometheus version, health, and runtime info",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			healthy, _ := client.Healthy(ctx)
			runtime, runtimeErr := client.RuntimeInfo(ctx)
			build, buildErr := client.BuildInfo(ctx)

			if runtimeErr != nil && buildErr != nil {
				return fmt.Errorf("failed to fetch status: %v / %v", runtimeErr, buildErr)
			}

			data := map[string]interface{}{
				"healthy": healthy,
				"runtime": runtime,
				"build":   build,
			}

			return renderOutput(format, func() error {
				fmt.Print(promlib.FormatStatus(runtime, build, healthy))
				return nil
			}, nil, data)
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json")
	return cmd
}

func promSummaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Quick overview of rules, alerts, and targets",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getPromClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			rules, _ := client.Rules(ctx, "")
			targets, _ := client.Targets(ctx)
			alerts, _ := client.Alerts(ctx)

			summary := promlib.BuildSummary(rules, targets, alerts)
			fmt.Print(promlib.FormatSummary(summary))
			return nil
		},
	}

	return cmd
}

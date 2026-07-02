package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/config"
	grafanalib "github.com/lbarahona/argus/internal/grafana"
	"github.com/spf13/cobra"
)

// ── Grafana Integration ─────────────────────────────────

func getGrafanaClient() (*grafanalib.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if !cfg.Grafana.IsConfigured() {
		return nil, fmt.Errorf("grafana is not configured — add 'grafana.url' to ~/.argus/config.yaml")
	}
	gCfg := grafanalib.GrafanaConfig{
		URL:    cfg.Grafana.URL,
		APIKey: cfg.Grafana.APIKey,
	}
	return grafanalib.NewClient(gCfg), nil
}

func grafanaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "grafana",
		Short:   "Grafana integration — dashboards, data sources, alerts, and status",
		Long:    "Query Grafana for dashboards, data sources, alert rules, and instance health.",
		Aliases: []string{"graf"},
	}

	cmd.AddCommand(
		grafanaDashboardsCmd(),
		grafanaDashboardGetCmd(),
		grafanaSearchCmd(),
		grafanaDatasourcesCmd(),
		grafanaFoldersCmd(),
		grafanaAlertsCmd(),
		grafanaAlertInstancesCmd(),
		grafanaStatusCmd(),
		grafanaSummaryCmd(),
	)

	return cmd
}

func grafanaDashboardsCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "dashboards",
		Short: "List all dashboards",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			dashboards, err := client.Dashboards(ctx)
			if err != nil {
				return fmt.Errorf("fetching dashboards: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatDashboards(dashboards))
				return nil
			}, nil, dashboards)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaDashboardGetCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "dashboard [uid]",
		Short: "Get dashboard details by UID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			dm, err := client.GetDashboard(ctx, args[0])
			if err != nil {
				return fmt.Errorf("fetching dashboard: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatDashboardDetail(dm))
				return nil
			}, nil, dm)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaSearchCmd() *cobra.Command {
	var (
		format string
		kind   string
		limit  int
	)
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search dashboards and folders",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			query := ""
			if len(args) > 0 {
				query = strings.Join(args, " ")
			}

			results, err := client.Search(ctx, query, kind, limit)
			if err != nil {
				return fmt.Errorf("searching: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatDashboards(results))
				return nil
			}, nil, results)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	cmd.Flags().StringVar(&kind, "type", "", "Filter by type: dash-db, dash-folder")
	cmd.Flags().IntVar(&limit, "limit", 100, "Max results")
	return cmd
}

func grafanaDatasourcesCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:     "datasources",
		Short:   "List configured data sources",
		Aliases: []string{"ds"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			ds, err := client.Datasources(ctx)
			if err != nil {
				return fmt.Errorf("fetching data sources: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatDatasources(ds))
				return nil
			}, nil, ds)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaFoldersCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "folders",
		Short: "List folders",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			folders, err := client.Folders(ctx)
			if err != nil {
				return fmt.Errorf("fetching folders: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatFolders(folders))
				return nil
			}, nil, folders)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaAlertsCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List alert rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			rules, err := client.AlertRules(ctx)
			if err != nil {
				return fmt.Errorf("fetching alert rules: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatAlertRules(rules))
				return nil
			}, nil, rules)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaAlertInstancesCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "firing",
		Short: "List firing alert instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			instances, err := client.AlertInstances(ctx)
			if err != nil {
				return fmt.Errorf("fetching alert instances: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatAlertInstances(instances))
				return nil
			}, nil, instances)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaStatusCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Grafana health and version",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			health, healthErr := client.Health(ctx)
			if healthErr != nil {
				return fmt.Errorf("grafana unreachable: %w", healthErr)
			}

			org, _ := client.Org(ctx) // org might fail without auth

			data := map[string]interface{}{"health": health}
			if org != nil {
				data["org"] = org
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatStatus(health, org))
				return nil
			}, nil, data)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

func grafanaSummaryCmd() *cobra.Command {
	var format string
	fmts := formatSet{JSON: true}

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Quick overview of Grafana instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := fmts.validate(format); err != nil {
				return err
			}
			client, err := getGrafanaClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			summary, err := client.BuildSummary(ctx)
			if err != nil {
				return fmt.Errorf("building summary: %w", err)
			}

			return renderOutput(format, fmts, func() error {
				fmt.Print(grafanalib.FormatSummary(summary))
				return nil
			}, nil, summary)
		},
	}
	addFormatFlag(cmd, &format, "text", fmts)
	return cmd
}

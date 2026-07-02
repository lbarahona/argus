package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	amlib "github.com/lbarahona/argus/internal/alertmanager"
	"github.com/lbarahona/argus/internal/config"
	"github.com/spf13/cobra"
)

// ──────────────────────────────────────────────
// Alertmanager commands
// ──────────────────────────────────────────────

func getAMClient() (*amlib.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	if !cfg.Alertmanager.IsConfigured() {
		return nil, fmt.Errorf("alertmanager not configured — add alertmanager.url to your config:\n  argus config init  (or edit ~/.argus/config.yaml)")
	}
	amCfg := amlib.AlertmanagerConfig{
		URL: cfg.Alertmanager.URL,
		BasicAuth: amlib.BasicAuth{
			Username: cfg.Alertmanager.BasicAuth.Username,
			Password: cfg.Alertmanager.BasicAuth.Password,
		},
	}
	return amlib.NewClient(amCfg), nil
}

func amCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "am",
		Short:   "Alertmanager integration — view alerts, silences, and status",
		Long:    "Query Prometheus Alertmanager for firing alerts, manage silences, and check cluster status.",
		Aliases: []string{"alertmanager"},
	}

	cmd.AddCommand(
		amAlertsCmd(),
		amSilencesCmd(),
		amSilenceCreateCmd(),
		amSilenceDeleteCmd(),
		amStatusCmd(),
		amSummaryCmd(),
	)

	return cmd
}

func amAlertsCmd() *cobra.Command {
	var (
		format  string
		showAll bool
		filter  []string
	)

	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List firing alerts",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getAMClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			opts := &amlib.AlertListOptions{}
			if !showAll {
				active := true
				opts.Active = &active
			}
			if len(filter) > 0 {
				opts.Filter = filter
			}

			alerts, err := client.ListAlerts(ctx, opts)
			if err != nil {
				return err
			}

			switch format {
			case "json":
				out, err := amlib.FormatJSON(alerts)
				if err != nil {
					return err
				}
				fmt.Println(out)
			default:
				fmt.Print(amlib.FormatAlerts(alerts, showAll))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	cmd.Flags().BoolVarP(&showAll, "all", "a", false, "Show all alerts (including suppressed)")
	cmd.Flags().StringArrayVar(&filter, "filter", nil, "Label filter (e.g. alertname=HighLatency)")

	return cmd
}

func amSilencesCmd() *cobra.Command {
	var (
		format      string
		showExpired bool
	)

	cmd := &cobra.Command{
		Use:   "silences",
		Short: "List silences",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getAMClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			silences, err := client.ListSilences(ctx)
			if err != nil {
				return err
			}

			switch format {
			case "json":
				out, err := amlib.FormatSilencesJSON(silences)
				if err != nil {
					return err
				}
				fmt.Println(out)
			default:
				fmt.Print(amlib.FormatSilences(silences, showExpired))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	cmd.Flags().BoolVar(&showExpired, "expired", false, "Include expired silences")

	return cmd
}

func amSilenceCreateCmd() *cobra.Command {
	var (
		duration  string
		comment   string
		createdBy string
		matchers  []string
	)

	cmd := &cobra.Command{
		Use:   "silence-create",
		Short: "Create a silence",
		Long:  "Create a silence with label matchers. Example:\n  argus am silence-create -m alertname=HighLatency -m severity=warning -d 2h -c \"Deploying fix\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(matchers) == 0 {
				return fmt.Errorf("at least one matcher is required (--matcher alertname=Value)")
			}

			client, err := getAMClient()
			if err != nil {
				return err
			}

			// Parse duration
			dur, err := time.ParseDuration(duration)
			if err != nil {
				return fmt.Errorf("invalid duration %q: %w", duration, err)
			}

			// Parse matchers
			var parsed []amlib.Matcher
			for _, m := range matchers {
				matcher, err := parseMatcher(m)
				if err != nil {
					return err
				}
				parsed = append(parsed, matcher)
			}

			if createdBy == "" {
				createdBy = "argus"
			}

			now := time.Now()
			req := amlib.SilenceRequest{
				Matchers:  parsed,
				StartsAt:  now,
				EndsAt:    now.Add(dur),
				CreatedBy: createdBy,
				Comment:   comment,
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			id, err := client.CreateSilence(ctx, req)
			if err != nil {
				return err
			}

			fmt.Printf("✅ Silence created: %s (expires in %s)\n", id, duration)
			return nil
		},
	}

	cmd.Flags().StringVarP(&duration, "duration", "d", "1h", "Silence duration (e.g. 30m, 2h, 1d)")
	cmd.Flags().StringVarP(&comment, "comment", "c", "Silenced via argus", "Silence comment")
	cmd.Flags().StringVar(&createdBy, "created-by", "argus", "Creator name")
	cmd.Flags().StringArrayVarP(&matchers, "matcher", "m", nil, "Label matcher (name=value, name!=value, name=~regex)")

	return cmd
}

func amSilenceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "silence-delete [silence-id]",
		Short: "Expire a silence",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getAMClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			if err := client.DeleteSilence(ctx, args[0]); err != nil {
				return err
			}

			fmt.Printf("✅ Silence %s expired\n", args[0])
			return nil
		},
	}

	return cmd
}

func amStatusCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check Alertmanager health and version",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getAMClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			healthy, latency, _ := client.Healthy(ctx)
			status, err := client.Status(ctx)
			if err != nil {
				return fmt.Errorf("alertmanager unreachable: %w", err)
			}

			switch format {
			case "json":
				data, err := jsonMarshal(status)
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				fmt.Print(amlib.FormatStatus(status, healthy, latency))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	return cmd
}

func amSummaryCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "summary",
		Short: "Quick alert summary — counts by severity and name",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := getAMClient()
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			alerts, err := client.ListAlerts(ctx, nil)
			if err != nil {
				return err
			}

			summary := amlib.BuildSummary(alerts)

			switch format {
			case "json":
				data, err := jsonMarshal(summary)
				if err != nil {
					return err
				}
				fmt.Println(string(data))
			default:
				fmt.Printf("\n🔔 Alert Summary: %d total (%d active, %d suppressed)\n\n",
					summary.TotalAlerts, summary.ActiveAlerts, summary.SuppressedAlerts)

				if len(summary.BySeverity) > 0 {
					fmt.Println("  By Severity:")
					for sev, count := range summary.BySeverity {
						fmt.Printf("    %s %s: %d\n", amlib.SeverityIcon(sev), sev, count)
					}
				}
				fmt.Println()

				if len(summary.FiringByName) > 0 {
					fmt.Println("  By Alert Name:")
					for name, count := range summary.FiringByName {
						fmt.Printf("    • %s: %d\n", name, count)
					}
				}
				fmt.Println()
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, json")
	return cmd
}

// parseMatcher parses "name=value", "name!=value", "name=~regex" into a Matcher.
func parseMatcher(s string) (amlib.Matcher, error) {
	// Check for !=~
	if idx := strings.Index(s, "!=~"); idx > 0 {
		return amlib.Matcher{
			Name:    s[:idx],
			Value:   s[idx+3:],
			IsEqual: false,
			IsRegex: true,
		}, nil
	}
	// Check for =~
	if idx := strings.Index(s, "=~"); idx > 0 {
		return amlib.Matcher{
			Name:    s[:idx],
			Value:   s[idx+2:],
			IsEqual: true,
			IsRegex: true,
		}, nil
	}
	// Check for !=
	if idx := strings.Index(s, "!="); idx > 0 {
		return amlib.Matcher{
			Name:    s[:idx],
			Value:   s[idx+2:],
			IsEqual: false,
			IsRegex: false,
		}, nil
	}
	// Check for =
	if idx := strings.Index(s, "="); idx > 0 {
		return amlib.Matcher{
			Name:    s[:idx],
			Value:   s[idx+1:],
			IsEqual: true,
			IsRegex: false,
		}, nil
	}
	return amlib.Matcher{}, fmt.Errorf("invalid matcher %q — use format name=value or name!=value", s)
}

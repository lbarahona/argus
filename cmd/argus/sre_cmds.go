package main

import (
	"context"
	"fmt"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/alert"
	"github.com/lbarahona/argus/internal/budget"
	"github.com/lbarahona/argus/internal/guard"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/internal/slo"
	"github.com/spf13/cobra"
)

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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			ctx := context.Background()
			if format != "json" {
				fmt.Printf("%s Checking alerts against %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))
			}
			checker := alert.NewChecker(sctx.client, sctx.instKey)
			rpt, err := checker.CheckAll(ctx, alertCfg)
			if err != nil {
				return err
			}
			if err := renderOutput(format, func() error {
				fmt.Print(alert.FormatText(rpt))
				return nil
			}, nil, rpt); err != nil {
				return err
			}
			if code := rpt.ExitCode(); code != 0 {
				return exitError{code: code}
			}
			return nil
		},
	}
	addInstanceFlag(checkCmd, &instance)
	addFormatFlag(checkCmd, &format, "text")
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
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}
			ctx := context.Background()
			if format != "json" {
				fmt.Printf("%s Evaluating SLOs against %s...\n", output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))
			}
			checker := slo.NewChecker(sctx.client, sctx.instKey)
			rpt, err := checker.CheckAll(ctx, sloCfg)
			if err != nil {
				return err
			}
			if err := renderOutput(format, func() error {
				fmt.Print(slo.FormatText(rpt))
				return nil
			}, nil, rpt); err != nil {
				return err
			}
			if code := rpt.ExitCode(); code != 0 {
				return exitError{code: code}
			}
			return nil
		},
	}
	addInstanceFlag(checkCmd, &instance)
	addFormatFlag(checkCmd, &format, "text")
	cmd.AddCommand(checkCmd)

	return cmd
}

func budgetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "budget",
		Short: "Error budget burndown analysis",
		Long: `Analyze error budget consumption against your SLOs.

Track how fast you're burning through your error budget with multi-window
burn rate analysis, exhaustion prediction, and deployment policy recommendations.

Implements Google SRE multi-window burn rate alerting:
  - Fast burn: 1h >14x AND 6h >6x → PAGE immediately
  - Slow burn: 6h >6x → create TICKET
  - Elevated: 1h >6x → WATCH closely

Requires SLOs to be configured (run 'argus slo init' first).
Exit codes: 0 = healthy, 1 = critical, 2 = exhausted/page.`,
	}

	var instance, service, format, window string
	var useAI bool

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Analyze error budget burndown for all SLOs",
		Example: `  argus budget check
  argus budget check --service api-gateway
  argus budget check --format json
  argus budget check --format markdown
  argus budget check --ai
  argus budget check --window 24h`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sloCfg, err := slo.LoadSLOs()
			if err != nil {
				return fmt.Errorf("loading SLOs: %w (run 'argus slo init' first)", err)
			}
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			ctx := context.Background()

			if format != "json" {
				fmt.Printf("%s Analyzing error budgets against %s...\n\n",
					output.MutedStyle.Render("⏳"), output.AccentStyle.Render(sctx.instKey))
			}

			var budgetProvider ai.Provider
			if useAI {
				budgetProvider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}
			opts := budget.Options{
				Window:     window,
				Service:    service,
				Format:     format,
				WithAI:     useAI,
				AIProvider: budgetProvider,
			}

			analyzer := budget.NewAnalyzer(sctx.client, sctx.instKey)
			rpt, err := analyzer.Analyze(ctx, sloCfg, opts)
			if err != nil {
				return err
			}

			budget.SortByUrgency(rpt.Reports)

			if err := renderOutput(format, func() error {
				fmt.Print(budget.FormatTerminal(rpt))
				return nil
			}, func() error {
				fmt.Print(budget.FormatMarkdown(rpt))
				return nil
			}, rpt); err != nil {
				return err
			}

			if code := rpt.ExitCode(); code != 0 {
				return exitError{code: code}
			}
			return nil
		},
	}
	addInstanceFlag(checkCmd, &instance)
	checkCmd.Flags().StringVarP(&service, "service", "s", "", "Filter to specific service")
	addFormatFlag(checkCmd, &format, "terminal")
	checkCmd.Flags().StringVarP(&window, "window", "w", "6h", "Analysis window: 1h, 6h, 24h, 7d, 30d")
	checkCmd.Flags().BoolVar(&useAI, "ai", false, "Include AI-powered recommendations")
	cmd.AddCommand(checkCmd)

	return cmd
}

func guardCmd() *cobra.Command {
	var instance, service, format string
	var strict, useAI bool
	var maxErrorRate, maxP99Latency float64
	var minCallVolume int

	cmd := &cobra.Command{
		Use:   "guard",
		Short: "CI/CD deployment gate — should we ship?",
		Long: `Pre-deployment safety check that answers the #1 question: "Is it safe to deploy?"

Runs 5 automated checks against your Signoz data:
  1. System Health — are services responding?
  2. Error Rates — any services above threshold?
  3. Latency (P99) — response times acceptable?
  4. Error Spikes — active error storms?
  5. Saturation — traffic distribution normal?

Returns a verdict: SHIP (exit 0), CAUTION (exit 1), or HOLD (exit 2).

Perfect for CI/CD pipelines:
  argus guard --format json || echo "Deploy blocked!"

Use --strict for critical services (lower thresholds, blocks on warnings).`,
		Example: `  argus guard
  argus guard --strict
  argus guard --service api-gateway
  argus guard --format json
  argus guard --max-error-rate 2.0 --max-p99 3000
  argus guard --ai

  # In CI/CD pipeline:
  argus guard --format json --strict || exit 1

  # GitHub Actions:
  - name: Pre-deploy check
    run: argus guard --strict --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			ctx := context.Background()

			if format != "json" {
				mode := "normal"
				if strict {
					mode = "STRICT"
				}
				fmt.Printf("%s Running deployment guard checks against %s [%s mode]...\n\n",
					output.MutedStyle.Render("🛡️"), output.AccentStyle.Render(sctx.instKey), mode)
			}

			var guardProvider ai.Provider
			if useAI {
				guardProvider, err = requireAI(sctx.cfg)
				if err != nil {
					return err
				}
			}
			opts := guard.Options{
				Service:       service,
				Strict:        strict,
				Format:        format,
				WithAI:        useAI,
				AIProvider:    guardProvider,
				MaxErrorRate:  maxErrorRate,
				MaxP99Latency: maxP99Latency,
				MinCallVolume: minCallVolume,
			}

			analyzer := guard.NewAnalyzer(sctx.client, sctx.instKey)
			rpt, err := analyzer.Check(ctx, opts)
			if err != nil {
				return err
			}

			if err := renderOutput(format, func() error {
				fmt.Print(guard.FormatTerminal(rpt))
				return nil
			}, func() error {
				fmt.Print(guard.FormatMarkdown(rpt))
				return nil
			}, rpt); err != nil {
				return err
			}

			if code := rpt.ExitCode(); code != 0 {
				return exitError{code: code}
			}
			return nil
		},
	}

	addInstanceFlag(cmd, &instance)
	cmd.Flags().StringVarP(&service, "service", "s", "", "Check specific service only")
	addFormatFlag(cmd, &format, "terminal")
	cmd.Flags().BoolVar(&strict, "strict", false, "Strict mode: lower thresholds, block on warnings")
	cmd.Flags().BoolVar(&useAI, "ai", false, "Include AI deployment advisory")
	cmd.Flags().Float64Var(&maxErrorRate, "max-error-rate", 0, "Max acceptable error rate %% (0 = default)")
	cmd.Flags().Float64Var(&maxP99Latency, "max-p99", 0, "Max acceptable P99 latency ms (0 = default)")
	cmd.Flags().IntVar(&minCallVolume, "min-calls", 0, "Min call volume to consider service (0 = default)")

	return cmd
}

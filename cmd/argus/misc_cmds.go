package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/doctor"
	"github.com/lbarahona/argus/internal/mcpserver"
	"github.com/lbarahona/argus/internal/tui"
	"github.com/spf13/cobra"
)

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
			provider, _ := getAIProvider(cfg)

			if !hasAIConfig(cfg) {
				return fmt.Errorf("AI provider not configured. Run: argus config init")
			}

			sctx, err := newSignozContext(instance)
			if err != nil {
				return err
			}

			instName := sctx.instKey
			if sctx.inst.Name != "" {
				instName = sctx.inst.Name
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			session := tui.New(sctx.client, tui.Options{
				InstanceKey:  sctx.instKey,
				InstanceName: instName,
				AIProvider:   provider,
				MaxHistory:   maxHistory,
			})

			return session.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&instance, "instance", "i", "", "Signoz instance to connect to")
	cmd.Flags().IntVar(&maxHistory, "max-history", 20, "Maximum conversation messages to retain")

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

func doctorCmd() *cobra.Command {
	var (
		verbose bool
		format  string
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration and connectivity issues",
		Long:  "Run diagnostic checks on config, Signoz instances, AI keys, and network connectivity.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			report := doctor.Run(ctx, version, verbose)

			// doctor.FormatJSON produces a hand-mapped schema (lowercase
			// fields, string statuses, pass/warn/fail counts) that the MCP
			// server also emits — pass it pre-serialized so renderOutput
			// doesn't re-marshal the raw Report struct.
			jsonData, err := doctor.FormatJSON(report)
			if err != nil {
				return err
			}

			if err := renderOutput(format, func() error {
				fmt.Print(doctor.FormatTerminal(report, verbose))
				return nil
			}, func() error {
				fmt.Print(doctor.FormatMarkdown(report))
				return nil
			}, json.RawMessage(jsonData)); err != nil {
				return err
			}

			if report.FailCount() > 0 {
				return exitError{code: 1}
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed information for each check")
	cmd.Flags().StringVarP(&format, "format", "f", "terminal", "Output: terminal, markdown, json")

	return cmd
}

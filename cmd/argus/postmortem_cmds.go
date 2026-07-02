package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/output"
	pmlib "github.com/lbarahona/argus/internal/postmortem"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/spf13/cobra"
)

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

			// Get AI provider
			var provider ai.Provider
			if useAI {
				if cfg != nil {
					provider, _ = getAIProvider(cfg)
				}
				if provider == nil {
					fmt.Println(output.WarningStyle.Render("⚠️  No AI provider configured. Skipping AI analysis."))
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
				AIProvider: provider,
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

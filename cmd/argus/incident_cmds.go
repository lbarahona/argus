package main

import (
	"fmt"
	"strings"

	"github.com/lbarahona/argus/internal/incident"
	"github.com/lbarahona/argus/internal/output"
	"github.com/spf13/cobra"
)

// incidentIDs lists all known incident IDs, for shell completion.
func incidentIDs() ([]string, error) {
	store, err := incident.Load()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(store.Incidents))
	for _, inc := range store.Incidents {
		ids = append(ids, inc.ID)
	}
	return ids, nil
}

func incidentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "incident",
		Short: "Manage incidents with timelines and status tracking",
		Long: `Track incidents from creation to resolution. Incidents are stored locally
in ~/.argus/incidents.yaml and can be managed entirely from the CLI.

Perfect for on-call SREs who need to track incidents during shifts.`,
	}

	// create
	var severity, commander, description string
	var services []string
	createCmd := &cobra.Command{
		Use:   "create [title]",
		Short: "Create a new incident",
		Args:  cobra.MinimumNArgs(1),
		Example: `  argus incident create "API returning 500s" --severity critical --services api-service
  argus incident create "Latency spike" --severity major --commander lester
  argus incident create "DB connection pool exhausted" --severity critical --services db,api`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			title := strings.Join(args, " ")
			inc := store.Create(title, severity, services, commander, description)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n🚨 Incident created: %s\n", output.AccentStyle.Render(inc.ID))
			fmt.Printf("   Title: %s\n", inc.Title)
			fmt.Printf("   Severity: %s %s\n", incident.SeverityIcon(inc.Severity), inc.Severity)
			if len(services) > 0 {
				fmt.Printf("   Services: %s\n", strings.Join(services, ", "))
			}
			fmt.Printf("\n   Update: argus incident update %s --status investigating --message \"looking into it\"\n\n", inc.ID)
			return nil
		},
	}
	createCmd.Flags().StringVar(&severity, "severity", "major", "Severity: critical, major, minor")
	createCmd.Flags().StringSliceVar(&services, "services", nil, "Affected services (comma-separated)")
	createCmd.Flags().StringVarP(&commander, "commander", "c", "", "Incident commander")
	createCmd.Flags().StringVar(&description, "description", "", "Description")
	cmd.AddCommand(createCmd)

	// list
	var all bool
	var limit int
	var format string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List incidents (active by default)",
		Example: `  argus incident list
  argus incident list --all
  argus incident list --limit 5
  argus incident list --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			var incidents []incident.Incident
			var title string
			if all {
				incidents = store.RecentIncidents(limit)
				title = "📋 All Incidents"
			} else {
				incidents = store.ActiveIncidents()
				title = "🚨 Active Incidents"
			}
			return renderOutput(format, func() error {
				incident.RenderList(incidents, title)
				return nil
			}, nil, incidents)
		},
	}
	listCmd.Flags().BoolVarP(&all, "all", "a", false, "Show all incidents (including resolved)")
	listCmd.Flags().IntVarP(&limit, "limit", "l", 20, "Max incidents to show (with --all)")
	addFormatFlag(listCmd, &format, "text")
	cmd.AddCommand(listCmd)

	// update
	var updateStatus, message, author string
	updateCmd := &cobra.Command{
		Use:   "update [incident-id]",
		Short: "Update incident status with a timeline entry",
		Args:  cobra.ExactArgs(1),
		Example: `  argus incident update INC-20260222-001 --status investigating --message "checking logs"
  argus incident update INC-20260222-001 --status identified --message "root cause: bad deploy"
  argus incident update INC-20260222-001 --status monitoring --message "rollback applied"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc, err := store.FindByPartialID(args[0])
			if err != nil {
				return err
			}
			if updateStatus == "" {
				return fmt.Errorf("--status is required (investigating, identified, monitoring, resolved)")
			}
			inc.Update(updateStatus, message, author)
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n✅ Updated %s → %s %s\n", output.AccentStyle.Render(inc.ID),
				incident.StatusIcon(updateStatus), updateStatus)
			if message != "" {
				fmt.Printf("   %s\n", message)
			}
			fmt.Println()
			return nil
		},
	}
	updateCmd.Flags().StringVar(&updateStatus, "status", "", "New status: investigating, identified, monitoring, resolved")
	updateCmd.Flags().StringVarP(&message, "message", "m", "", "Timeline message")
	updateCmd.Flags().StringVar(&author, "author", "", "Author of the update")
	updateCmd.ValidArgsFunction = completeIDs(incidentIDs)
	cmd.AddCommand(updateCmd)

	// resolve (shortcut for update --status resolved)
	var resolveMsg string
	resolveCmd := &cobra.Command{
		Use:   "resolve [incident-id]",
		Short: "Resolve an incident",
		Args:  cobra.ExactArgs(1),
		Example: `  argus incident resolve INC-20260222-001 --message "deployed fix in v2.3.1"
  argus incident resolve INC-20260222-001 -m "false alarm"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc, err := store.FindByPartialID(args[0])
			if err != nil {
				return err
			}
			msg := resolveMsg
			if msg == "" {
				msg = "Incident resolved"
			}
			inc.Update(incident.StatusResolved, msg, "")
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("\n✅ Resolved %s — %s\n", output.AccentStyle.Render(inc.ID), inc.Title)
			fmt.Printf("   Duration: %s\n\n", inc.Duration)
			return nil
		},
	}
	resolveCmd.Flags().StringVarP(&resolveMsg, "message", "m", "", "Resolution message")
	resolveCmd.ValidArgsFunction = completeIDs(incidentIDs)
	cmd.AddCommand(resolveCmd)

	// timeline
	timelineCmd := &cobra.Command{
		Use:     "timeline [incident-id]",
		Short:   "Show detailed timeline for an incident",
		Args:    cobra.ExactArgs(1),
		Example: "  argus incident timeline INC-20260222-001",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := incident.Load()
			if err != nil {
				return err
			}
			inc, err := store.FindByPartialID(args[0])
			if err != nil {
				return err
			}
			incident.RenderTimeline(inc)
			return nil
		},
	}
	timelineCmd.ValidArgsFunction = completeIDs(incidentIDs)
	cmd.AddCommand(timelineCmd)

	return cmd
}

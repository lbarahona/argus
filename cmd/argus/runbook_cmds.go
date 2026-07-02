package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lbarahona/argus/internal/runbook"
	"github.com/spf13/cobra"
)

func runbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runbook",
		Short: "Manage and execute operational runbooks",
		Long: `Create, manage, and execute operational runbooks for incident response,
maintenance procedures, and troubleshooting workflows.

Runbooks are stored as YAML files in ~/.argus/runbooks/ and can be shared
across teams via version control.`,
	}

	// init
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create sample runbooks to get started",
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			existing, _ := store.List()
			if len(existing) > 0 {
				fmt.Printf("⚠️  Found %d existing runbooks in %s\n", len(existing), store.Dir())
				fmt.Print("Add sample runbooks anyway? (y/N): ")
				var answer string
				fmt.Scanln(&answer)
				if strings.ToLower(answer) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			if err := runbook.InitSamples(store); err != nil {
				return err
			}
			fmt.Printf("✅ Created 5 sample runbooks in %s\n", store.Dir())
			fmt.Println("   Run: argus runbook list")
			return nil
		},
	})

	// list
	var format string
	var category string
	listCmd := &cobra.Command{
		Use:     "list",
		Short:   "List all runbooks",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rbs, err := store.List()
			if err != nil {
				return err
			}

			if category != "" {
				var filtered []*runbook.Runbook
				for _, rb := range rbs {
					if strings.EqualFold(rb.Category, category) {
						filtered = append(filtered, rb)
					}
				}
				rbs = filtered
			}

			if format == "json" {
				out, err := runbook.FormatJSON(rbs)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}

			runbook.PrintList(os.Stdout, rbs)
			return nil
		},
	}
	listCmd.Flags().StringVarP(&format, "format", "f", "text", "Output format: text or json")
	listCmd.Flags().StringVarP(&category, "category", "c", "", "Filter by category")
	cmd.AddCommand(listCmd)

	// show
	cmd.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Show runbook details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}
			runbook.PrintShow(os.Stdout, rb)
			return nil
		},
	})

	// search
	cmd.AddCommand(&cobra.Command{
		Use:   "search <query>",
		Short: "Search runbooks by name, description, tags, or category",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			results, err := store.Search(args[0])
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Printf("\n🔍 No runbooks matching %q\n\n", args[0])
				return nil
			}
			fmt.Printf("\n🔍 Search results for %q:\n", args[0])
			runbook.PrintList(os.Stdout, results)
			return nil
		},
	})

	// delete
	cmd.AddCommand(&cobra.Command{
		Use:     "delete <id>",
		Short:   "Delete a runbook",
		Aliases: []string{"rm"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Delete runbook %q? (y/N): ", rb.Name)
			var answer string
			fmt.Scanln(&answer)
			if strings.ToLower(answer) != "y" {
				fmt.Println("Aborted.")
				return nil
			}
			if err := store.Delete(args[0]); err != nil {
				return err
			}
			fmt.Printf("✅ Deleted: %s\n", rb.Name)
			return nil
		},
	})

	// validate
	cmd.AddCommand(&cobra.Command{
		Use:   "validate <id>",
		Short: "Validate a runbook's structure",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}

			issues := validateRunbook(rb)
			if len(issues) == 0 {
				fmt.Printf("✅ Runbook %q is valid (%d steps)\n", rb.Name, len(rb.Steps))
				return nil
			}

			fmt.Printf("⚠️  Runbook %q has %d issues:\n\n", rb.Name, len(issues))
			for _, issue := range issues {
				fmt.Printf("  • %s\n", issue)
			}
			return nil
		},
	})

	// run (dry-run by default; --execute runs command/check steps with confirmation)
	var execute bool
	runCmd := &cobra.Command{
		Use:   "run <id>",
		Short: "Walk through a runbook step-by-step (dry-run by default)",
		Long: `Walk through a runbook step by step. By default this is a dry run:
each step is shown but nothing executes. With --execute, command and check
steps run after a per-step confirmation, with timeouts, captured output,
and a run log saved under ~/.argus/runbooks/runs/.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := runbook.NewStore()
			rb, err := store.Load(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("\n🚀 Running: %s", rb.Name)
			if !execute {
				fmt.Print(" [DRY RUN]")
			}
			fmt.Printf("\n   %d steps\n\n", len(rb.Steps))

			executor := &runbook.Executor{
				Out:     os.Stdout,
				In:      os.Stdin,
				Execute: execute,
			}
			log := executor.Run(cmd.Context(), rb)

			if execute {
				if path, err := store.SaveRunLog(log); err == nil {
					fmt.Printf("   📝 run log: %s\n", path)
				} else {
					fmt.Printf("   ⚠️  could not save run log: %v\n", err)
				}
			}

			runbook.PrintRunLog(os.Stdout, log)
			if log.Status == "failed" {
				os.Exit(1)
			}
			return nil
		},
	}
	runCmd.Flags().BoolVar(&execute, "execute", false, "Actually execute command/check steps (with per-step confirmation)")
	cmd.AddCommand(runCmd)

	return cmd
}

func validateRunbook(rb *runbook.Runbook) []string {
	var issues []string

	if rb.Name == "" {
		issues = append(issues, "missing name")
	}
	if len(rb.Steps) == 0 {
		issues = append(issues, "no steps defined")
	}
	for i, step := range rb.Steps {
		if step.Name == "" {
			issues = append(issues, fmt.Sprintf("step %d: missing name", i+1))
		}
		if step.Command == "" && step.Check == "" && !step.Manual {
			issues = append(issues, fmt.Sprintf("step %d (%s): no command, check, or manual flag", i+1, step.Name))
		}
	}
	if rb.Severity != "" {
		valid := map[string]bool{"P1": true, "P2": true, "P3": true, "P4": true}
		if !valid[strings.ToUpper(rb.Severity)] {
			issues = append(issues, fmt.Sprintf("invalid severity %q (use P1-P4)", rb.Severity))
		}
	}
	return issues
}

package main

import (
	"fmt"
	"strings"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/output"
	"github.com/spf13/cobra"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Argus configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize configuration interactively",
		RunE: func(cmd *cobra.Command, args []string) error {
			if config.Exists() {
				fmt.Printf("⚠️  Config already exists at %s\n", config.Path())
				fmt.Print("Overwrite? (y/N): ")
				var answer string
				_, _ = fmt.Scanln(&answer) // EOF/error leaves answer empty → "N"
				if strings.ToLower(answer) != "y" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			_, err := config.RunInit()
			return err
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "add-instance",
		Short: "Add a new Signoz instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return config.AddInstance(cfg)
		},
	})

	return cmd
}

func useCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use [instance]",
		Short: "Set the default Signoz instance",
		Long: `Set the default Signoz instance used by all commands.

When called without arguments, lists all configured instances and marks
the current default (like kubectl config get-contexts).

When called with an instance name, sets it as the default.`,
		Example: `  argus use              # list instances, show current default
  argus use production   # set default to "production"
  argus use staging      # switch to "staging"`,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			cfg, err := config.Load()
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var keys []string
			for k := range cfg.Instances {
				keys = append(keys, k)
			}
			return keys, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			if len(cfg.Instances) == 0 {
				return fmt.Errorf("no instances configured. Run: argus config init")
			}

			// No args: list instances with current default marked
			if len(args) == 0 {
				output.PrintInstances(cfg.Instances, cfg.DefaultInstance)
				return nil
			}

			name := args[0]

			// Validate instance exists
			if _, ok := cfg.Instances[name]; !ok {
				var available []string
				for k := range cfg.Instances {
					available = append(available, k)
				}
				return fmt.Errorf("instance %q not found. Available instances: %s", name, strings.Join(available, ", "))
			}

			// Set default
			cfg.DefaultInstance = name
			if err := config.Save(cfg); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Printf("Default instance set to: %s ✓\n", name)
			return nil
		},
	}
}

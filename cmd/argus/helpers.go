package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/internal/signoz"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/spf13/cobra"
)

// signozContext bundles what every Signoz-backed command needs: the loaded
// config, a ready-to-use client for the resolved instance, and the resolved
// instance's key and struct.
type signozContext struct {
	cfg     *types.Config
	client  *signoz.Client
	instKey string
	inst    *types.Instance
}

// newSignozContext loads config, resolves the instance (explicit flag value
// or configured default), and builds the client. It is the single path from
// flags to a Signoz client; resolution failures are returned, never
// swallowed.
func newSignozContext(instanceFlag string) (*signozContext, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	inst, instKey, err := config.GetInstance(cfg, instanceFlag)
	if err != nil {
		return nil, err
	}
	return &signozContext{
		cfg:     cfg,
		client:  signoz.New(*inst),
		instKey: instKey,
		inst:    inst,
	}, nil
}

// renderOutput validates format and dispatches to the matching renderer.
// Pass nil for a renderer the command does not support; requesting an
// unsupported/unknown format is an error (not a silent default).
func renderOutput(format string, terminal func() error, markdown func() error, jsonValue any) error {
	switch format {
	case "", "terminal", "text", "table":
		if terminal == nil {
			return fmt.Errorf("terminal output not supported here")
		}
		return terminal()
	case "markdown", "md":
		if markdown == nil {
			return fmt.Errorf("markdown output is not supported by this command")
		}
		return markdown()
	case "json":
		// Pre-serialized JSON (a package's hand-mapped FormatJSON schema,
		// e.g. internal/doctor's) passes through verbatim rather than being
		// re-marshaled, which would rewrite the schema.
		if raw, ok := jsonValue.(json.RawMessage); ok {
			fmt.Println(strings.TrimSpace(string(raw)))
			return nil
		}
		if jsonValue == nil {
			return fmt.Errorf("json output is not supported by this command")
		}
		data, err := jsonMarshal(jsonValue)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	default:
		return fmt.Errorf("unknown format %q (valid: terminal, markdown, json)", format)
	}
}

// addInstanceFlag registers -i/--instance with completion from the config's
// instance names.
func addInstanceFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "instance", "i", "", "Signoz instance name (default: configured default)")
	_ = cmd.RegisterFlagCompletionFunc("instance", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(cfg.Instances))
		for name := range cfg.Instances {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	})
}

// completeIDs builds a ValidArgsFunction that offers the IDs returned by load
// as completions for a command's first positional argument. It degrades
// gracefully: a load failure yields no completions (never an error to the
// shell), and once args[0] is already present there is nothing left to
// complete.
func completeIDs(load func() ([]string, error)) func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ids, err := load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return ids, cobra.ShellCompDirectiveNoFileComp
	}
}

// formatSet names the render targets a command actually supports. Terminal
// is always supported; Markdown/JSON opt in to the rest. It is the single
// source of truth for a command's --format flag: help text, shell
// completion, and (via validate) the eager pre-work gate all derive from it,
// so a flag can never advertise a format renderOutput will go on to reject.
type formatSet struct {
	Markdown bool
	JSON     bool // terminal is always supported
}

// list returns the supported format names in display order.
func (fs formatSet) list() []string {
	out := []string{"terminal"}
	if fs.Markdown {
		out = append(out, "markdown")
	}
	if fs.JSON {
		out = append(out, "json")
	}
	return out
}

// validate rejects formats outside the set (synonyms text/table/md
// accepted). Known-but-unsupported formats get a "not supported by this
// command" message; genuinely unrecognized formats get "unknown format".
func (fs formatSet) validate(format string) error {
	switch format {
	case "", "terminal", "text", "table":
		return nil
	case "markdown", "md":
		if fs.Markdown {
			return nil
		}
	case "json":
		if fs.JSON {
			return nil
		}
	default:
		return fmt.Errorf("unknown format %q (valid: %s)", format, strings.Join(fs.list(), ", "))
	}
	return fmt.Errorf("format %q is not supported by this command (valid: %s)", format, strings.Join(fs.list(), ", "))
}

// addFormatFlag registers -f/--format with completion. def preserves each
// command's existing default string. fs drives both the help text and the
// shell completion values, so they always match what the command actually
// renders.
func addFormatFlag(cmd *cobra.Command, target *string, def string, fs formatSet) {
	cmd.Flags().StringVarP(target, "format", "f", def, "Output format: "+strings.Join(fs.list(), ", "))
	_ = cmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return fs.list(), cobra.ShellCompDirectiveNoFileComp
	})
}

// minutesValue is a pflag.Value accepting bare minutes ("90") or Go-style
// durations ("90m", "2h", "1h30m"), stored as whole minutes.
type minutesValue int

func newMinutesValue(def int, p *int) *minutesValue {
	*p = def
	return (*minutesValue)(p)
}

func (m *minutesValue) String() string { return strconv.Itoa(int(*m)) }
func (m *minutesValue) Type() string   { return "duration" }
func (m *minutesValue) Set(s string) error {
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			return fmt.Errorf("duration must be positive")
		}
		*m = minutesValue(n)
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q (use minutes like 90, or 90m / 2h)", s)
	}
	if d < 0 {
		return fmt.Errorf("duration must be positive")
	}
	*m = minutesValue(int(d.Minutes()))
	return nil
}

// addDurationFlag registers -d/--duration accepting minutes or human strings.
func addDurationFlag(cmd *cobra.Command, target *int, def int, help string) {
	cmd.Flags().VarP(newMinutesValue(def, target), "duration", "d", help)
}

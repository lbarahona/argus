package main

import (
	jsonPkg "encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/lbarahona/argus/internal/ai"
	"github.com/lbarahona/argus/internal/output"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// exitError carries a process exit code through RunE without os.Exit,
// so deferred cleanup runs and commands stay testable.
type exitError struct{ code int }

func (e exitError) Error() string { return "" }

// exitCodeFor maps a RunE error to the process exit code.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return 1
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "argus",
		Short: "AI-powered observability CLI for SREs",
		Long:  "Argus connects to Signoz instances and uses AI (Anthropic, OpenAI, or Amazon Bedrock) to analyze logs, metrics, and traces with natural language queries.",
		// Errors and usage are handled in main() below so exitError (which
		// carries a process exit code but no message) doesn't produce a
		// blank "Error: " line or a usage dump after a command has already
		// rendered its output.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.AddCommand(
		versionCmd(),
		configCmd(),
		useCmd(),
		instancesCmd(),
		statusCmd(),
		logsCmd(),
		askCmd(),
		servicesCmd(),
		tracesCmd(),
		metricsCmd(),
		dashboardCmd(),
		reportCmd(),
		topCmd(),
		diffCmd(),
		watchCmd(),
		alertCmd(),
		explainCmd(),
		sloCmd(),
		anomalyCmd(),
		timelineCmd(),
		tuiCmd(),
		correlateCmd(),
		incidentCmd(),
		runbookCmd(),
		scorecardCmd(),
		forecastCmd(),
		depsCmd(),
		mcpCmd(),
		postmortemCmd(),
		deployCmd(),
		budgetCmd(),
		guardCmd(),
		doctorCmd(),
		amCmd(),
		grafanaCmd(),
		promCmd(),
		lokiCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, output.ErrorStyle.Render("Error: "+msg))
		}
		os.Exit(exitCodeFor(err))
	}
}

// getAIProvider creates an AI provider from the loaded config.
// Returns a non-nil error whenever the configured (or default) provider is
// missing required credentials — there is no "unconfigured but not an
// error" case.
func getAIProvider(cfg *types.Config) (ai.Provider, error) {
	aiCfg := cfg.GetAIConfig()
	return ai.NewProvider(ai.AIConfig{
		Provider:     aiCfg.Provider,
		Model:        aiCfg.Model,
		AnthropicKey: aiCfg.AnthropicKey,
		OpenAIKey:    aiCfg.OpenAIKey,
		Bedrock: ai.BedrockConfig{
			Endpoint: aiCfg.Bedrock.Endpoint,
			Token:    aiCfg.Bedrock.Token,
			Model:    aiCfg.Bedrock.Model,
		},
	})
}

// requireAI returns a configured provider or a uniform, actionable error.
// Every command that was asked for AI (an --ai flag, or a --query/question
// argument) must call this — a user request for AI must never silently
// no-op or degrade to non-AI output.
func requireAI(cfg *types.Config) (ai.Provider, error) {
	provider, err := getAIProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("AI is not configured: %w — set ANTHROPIC_API_KEY or configure an ai provider in ~/.argus/config.yaml", err)
	}
	return provider, nil
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			output.PrintVersion(version, commit, date)
		},
	}
}

// jsonMarshal is a helper for JSON output.
func jsonMarshal(v interface{}) ([]byte, error) {
	return jsonPkg.MarshalIndent(v, "", "  ")
}

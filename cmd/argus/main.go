package main

import (
	jsonPkg "encoding/json"
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

func main() {
	rootCmd := &cobra.Command{
		Use:   "argus",
		Short: "AI-powered observability CLI for SREs",
		Long:  "Argus connects to Signoz instances and uses AI (Anthropic, OpenAI, or Amazon Bedrock) to analyze logs, metrics, and traces with natural language queries.",
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
		os.Exit(1)
	}
}

// getAIProvider creates an AI provider from the loaded config.
// Returns nil, nil if no AI is configured (not an error, just no AI available).
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

// hasAIConfig returns true if the config has any AI provider configured.
func hasAIConfig(cfg *types.Config) bool {
	aiCfg := cfg.GetAIConfig()
	switch aiCfg.Provider {
	case "anthropic", "":
		return aiCfg.AnthropicKey != "" || os.Getenv("ANTHROPIC_API_KEY") != ""
	case "openai":
		return aiCfg.OpenAIKey != "" || os.Getenv("OPENAI_API_KEY") != ""
	case "bedrock":
		return aiCfg.Bedrock.Endpoint != "" && (aiCfg.Bedrock.Token != "" || os.Getenv("AWS_BEARER_TOKEN_BEDROCK") != "")
	}
	return false
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

package ai

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// newHTTPClient returns a client suitable for streaming: no overall timeout
// (streams are long-lived) but bounded dial/TLS/first-byte phases so a hung
// endpoint cannot block a CLI or MCP call forever. Proxy and HTTP/2 are set
// explicitly because a bare &http.Transport{} (unlike http.DefaultTransport)
// does not pick up HTTPS_PROXY/HTTP_PROXY, and setting DialContext disables
// Go's automatic HTTP/2 upgrade unless ForceAttemptHTTP2 is set.
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
}

// newBedrockHTTPClient returns a client for Bedrock's non-streaming
// /model/{id}/invoke endpoint. Unlike newHTTPClient, it sets no
// ResponseHeaderTimeout: response headers only arrive after the full
// generation completes, which routinely exceeds 60s at MaxTokens 4096, so a
// fixed header timeout would kill otherwise-healthy calls. An overall
// Timeout still bounds the request since this is a single non-streaming
// response rather than a long-lived stream.
func newBedrockHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// Provider is the interface all AI providers implement.
type Provider interface {
	// Analyze sends a prompt with the default system context and streams the response.
	Analyze(ctx context.Context, prompt string, w io.Writer) error

	// AnalyzeWithSystem sends a prompt with a custom system prompt and streams the response.
	AnalyzeWithSystem(ctx context.Context, systemPrompt string, messages []Message, w io.Writer) error

	// AnalyzeSync sends a prompt and returns the full response (non-streaming).
	AnalyzeSync(ctx context.Context, prompt string) (string, error)

	// Name returns the provider identifier (e.g. "anthropic", "openai", "bedrock").
	Name() string

	// Model returns the active model name.
	Model() string
}

// AIConfig holds configuration for AI providers.
type AIConfig struct {
	Provider     string        `yaml:"provider"`
	Model        string        `yaml:"model"`
	AnthropicKey string        `yaml:"anthropic_key"`
	OpenAIKey    string        `yaml:"openai_key"`
	Bedrock      BedrockConfig `yaml:"bedrock"`
}

// BedrockConfig holds Bedrock-specific configuration.
type BedrockConfig struct {
	Endpoint string `yaml:"endpoint"`
	Token    string `yaml:"token"`
	Model    string `yaml:"model"`
}

// DefaultModels maps provider names to their default (best) models.
// These defaults can be overridden via the ai.model config key.
var DefaultModels = map[string]string{
	"anthropic": "claude-sonnet-5",
	"openai":    "gpt-4o",                                    // can be overridden by ai.model config key
	"bedrock":   "anthropic.claude-3-5-sonnet-20241022-v2:0", // can be overridden by ai.model config key
}

// ResolveModel returns the model to use: explicit if set, or provider default if "auto" or empty.
func ResolveModel(provider, model string) string {
	if model != "" && model != "auto" {
		return model
	}
	if m, ok := DefaultModels[provider]; ok {
		return m
	}
	return model
}

// NewProvider creates the appropriate Provider from AIConfig.
func NewProvider(cfg AIConfig) (Provider, error) {
	// Apply env var fallbacks
	if cfg.AnthropicKey == "" {
		cfg.AnthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if cfg.OpenAIKey == "" {
		cfg.OpenAIKey = os.Getenv("OPENAI_API_KEY")
	}
	if cfg.Bedrock.Token == "" {
		cfg.Bedrock.Token = os.Getenv("AWS_BEARER_TOKEN_BEDROCK")
	}

	switch cfg.Provider {
	case "anthropic", "":
		if cfg.AnthropicKey == "" {
			return nil, fmt.Errorf("Anthropic API key not configured. Run: argus config init")
		}
		return NewAnthropicProvider(cfg.AnthropicKey, ResolveModel("anthropic", cfg.Model)), nil

	case "openai":
		if cfg.OpenAIKey == "" {
			return nil, fmt.Errorf("OpenAI API key not configured. Set ai.openai_key in config or OPENAI_API_KEY env var")
		}
		return NewOpenAIProvider(cfg.OpenAIKey, ResolveModel("openai", cfg.Model)), nil

	case "bedrock":
		if cfg.Bedrock.Endpoint == "" {
			return nil, fmt.Errorf("Bedrock endpoint not configured. Set ai.bedrock.endpoint in config")
		}
		if cfg.Bedrock.Token == "" {
			return nil, fmt.Errorf("Bedrock token not configured. Set ai.bedrock.token in config or AWS_BEARER_TOKEN_BEDROCK env var")
		}
		model := cfg.Model
		if cfg.Bedrock.Model != "" && (model == "" || model == "auto") {
			model = cfg.Bedrock.Model
		}
		return NewBedrockProvider(cfg.Bedrock.Endpoint, cfg.Bedrock.Token, ResolveModel("bedrock", model)), nil

	default:
		return nil, fmt.Errorf("unknown AI provider: %q (supported: anthropic, openai, bedrock)", cfg.Provider)
	}
}

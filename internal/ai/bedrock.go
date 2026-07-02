package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	defaultBedrockModel = "anthropic.claude-3-5-sonnet-20241022-v2:0"

	// bedrockAnthropicVersion is the body-level version Bedrock requires for
	// Anthropic models — distinct from the direct API's anthropic-version header.
	bedrockAnthropicVersion = "bedrock-2023-05-31"
)

// BedrockProvider implements Provider for Amazon Bedrock via bearer token auth.
// This is a pure HTTP implementation — no AWS SDK dependency.
type BedrockProvider struct {
	endpoint string
	token    string
	model    string
	client   *http.Client
}

// NewBedrockProvider creates a new Bedrock provider.
// endpoint: the full Bedrock invoke endpoint URL (e.g. https://bedrock-runtime.us-east-1.amazonaws.com)
// token: bearer token for authentication
// model: the model ID to use
func NewBedrockProvider(endpoint, token, model string) *BedrockProvider {
	if model == "" {
		model = defaultBedrockModel
	}
	// Trim trailing slash from endpoint
	endpoint = strings.TrimRight(endpoint, "/")
	return &BedrockProvider{
		endpoint: endpoint,
		token:    token,
		model:    model,
		client:   newHTTPClient(),
	}
}

func (p *BedrockProvider) Name() string  { return "bedrock" }
func (p *BedrockProvider) Model() string { return p.model }

func (p *BedrockProvider) Analyze(ctx context.Context, prompt string, w io.Writer) error {
	return p.AnalyzeWithSystem(ctx, systemPrompt, []Message{{Role: "user", Content: prompt}}, w)
}

func (p *BedrockProvider) AnalyzeWithSystem(ctx context.Context, system string, messages []Message, w io.Writer) error {
	// Bedrock with Anthropic models uses the Anthropic messages format
	reqBody := bedrockRequest{
		AnthropicVersion: bedrockAnthropicVersion,
		MaxTokens:        4096,
		System:           system,
		Messages:         messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	// The invoke-with-response-stream endpoint returns binary AWS
	// eventstream framing; the non-streaming invoke endpoint returns plain
	// Anthropic-messages JSON we can parse without an AWS SDK.
	url := fmt.Sprintf("%s/model/%s/invoke", p.endpoint, p.model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.token)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling Bedrock API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Bedrock API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decoding Bedrock response: %w", err)
	}

	wrote := false
	for _, c := range out.Content {
		if c.Type == "text" && c.Text != "" {
			fmt.Fprint(w, c.Text)
			wrote = true
		}
	}
	if !wrote {
		return fmt.Errorf("Bedrock response contained no text content")
	}
	fmt.Fprintln(w)
	if out.StopReason == "max_tokens" {
		fmt.Fprintln(w, "[response truncated at max_tokens]")
	}
	return nil
}

func (p *BedrockProvider) AnalyzeSync(ctx context.Context, prompt string) (string, error) {
	var buf bytes.Buffer
	if err := p.Analyze(ctx, prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type bedrockRequest struct {
	AnthropicVersion string    `json:"anthropic_version"`
	MaxTokens        int       `json:"max_tokens"`
	System           string    `json:"system"`
	Messages         []Message `json:"messages"`
}

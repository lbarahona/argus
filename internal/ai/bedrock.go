package ai

import (
	"bufio"
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
		client:   &http.Client{},
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
		AnthropicVersion: anthropicVersion,
		MaxTokens:        4096,
		System:           system,
		Messages:         messages,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	// Bedrock invoke-with-response-stream endpoint
	url := fmt.Sprintf("%s/model/%s/invoke-with-response-stream", p.endpoint, p.model)

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

	// Bedrock with Anthropic models streams using the same SSE format as Anthropic
	return streamBedrockResponse(resp.Body, w)
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

// streamBedrockResponse parses the Bedrock SSE response stream.
// When using Anthropic models via Bedrock, the SSE format matches Anthropic's format.
func streamBedrockResponse(body io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			// Also check for Bedrock's bytes-based event format
			if strings.HasPrefix(line, "{") {
				// Direct JSON line (some Bedrock endpoints use newline-delimited JSON)
				var event struct {
					Type  string `json:"type"`
					Delta struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"delta"`
					// OpenAI-compatible format from some Bedrock models
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
					// Direct output text
					OutputText string `json:"outputText"`
				}
				if err := json.Unmarshal([]byte(line), &event); err == nil {
					if event.Type == "content_block_delta" && event.Delta.Text != "" {
						fmt.Fprint(w, event.Delta.Text)
					} else if len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" {
						fmt.Fprint(w, event.Choices[0].Delta.Content)
					} else if event.OutputText != "" {
						fmt.Fprint(w, event.OutputText)
					}
				}
				continue
			}
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fmt.Fprint(w, event.Delta.Text)
		}
	}

	fmt.Fprintln(w)
	return scanner.Err()
}

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
	anthropicAPI          = "https://api.anthropic.com/v1/messages"
	anthropicVersion      = "2023-06-01"
	defaultAnthropicModel = "claude-sonnet-4-20250514"
)

// AnthropicProvider implements Provider for Anthropic Claude.
type AnthropicProvider struct {
	apiKey string
	model  string
	client *http.Client
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey, model string) *AnthropicProvider {
	if model == "" {
		model = defaultAnthropicModel
	}
	return &AnthropicProvider{
		apiKey: apiKey,
		model:  model,
		client: newHTTPClient(),
	}
}

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }

func (p *AnthropicProvider) Analyze(ctx context.Context, prompt string, w io.Writer) error {
	return p.AnalyzeWithSystem(ctx, systemPrompt, []Message{{Role: "user", Content: prompt}}, w)
}

func (p *AnthropicProvider) AnalyzeWithSystem(ctx context.Context, system string, messages []Message, w io.Writer) error {
	reqBody := anthropicRequest{
		Model:     p.model,
		MaxTokens: 4096,
		System:    system,
		Messages:  messages,
		Stream:    true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPI, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Anthropic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return streamAnthropicResponse(resp.Body, w)
}

func (p *AnthropicProvider) AnalyzeSync(ctx context.Context, prompt string) (string, error) {
	var buf bytes.Buffer
	if err := p.Analyze(ctx, prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
}

func streamAnthropicResponse(body io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	truncated := false
	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch {
		case event.Type == "error":
			fmt.Fprintln(w)
			return fmt.Errorf("stream error from API: %s: %s", event.Error.Type, event.Error.Message)
		case event.Type == "content_block_delta" && event.Delta.Type == "text_delta":
			fmt.Fprint(w, event.Delta.Text)
		case event.Type == "message_delta" && event.Delta.StopReason == "max_tokens":
			truncated = true
		}
	}

	fmt.Fprintln(w)
	if truncated {
		fmt.Fprintln(w, "[response truncated at max_tokens]")
	}
	return scanner.Err()
}

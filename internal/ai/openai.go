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
	openaiAPI          = "https://api.openai.com/v1/chat/completions"
	defaultOpenAIModel = "gpt-4o"
)

// OpenAIProvider implements Provider for OpenAI.
type OpenAIProvider struct {
	apiKey string
	model  string
	client *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAIProvider{
		apiKey: apiKey,
		model:  model,
		client: newHTTPClient(),
	}
}

func (p *OpenAIProvider) Name() string  { return "openai" }
func (p *OpenAIProvider) Model() string { return p.model }

func (p *OpenAIProvider) Analyze(ctx context.Context, prompt string, w io.Writer) error {
	return p.AnalyzeWithSystem(ctx, systemPrompt, []Message{{Role: "user", Content: prompt}}, w)
}

func (p *OpenAIProvider) AnalyzeWithSystem(ctx context.Context, system string, messages []Message, w io.Writer) error {
	// Build OpenAI messages array with system message first
	oaiMessages := make([]openaiMessage, 0, len(messages)+1)
	oaiMessages = append(oaiMessages, openaiMessage{Role: "system", Content: system})
	for _, m := range messages {
		oaiMessages = append(oaiMessages, openaiMessage{Role: m.Role, Content: m.Content})
	}

	reqBody := openaiRequest{
		Model:     p.model,
		Messages:  oaiMessages,
		MaxTokens: 4096,
		Stream:    true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiAPI, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling OpenAI API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return streamOpenAIResponse(resp.Body, w)
}

func (p *OpenAIProvider) AnalyzeSync(ctx context.Context, prompt string) (string, error) {
	var buf bytes.Buffer
	if err := p.Analyze(ctx, prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiRequest struct {
	Model     string          `json:"model"`
	Messages  []openaiMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
	Stream    bool            `json:"stream"`
}

func streamOpenAIResponse(body io.Reader, w io.Writer) error {
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
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Error.Message != "" {
			fmt.Fprintln(w)
			return fmt.Errorf("stream error from API: %s: %s", event.Error.Type, event.Error.Message)
		}

		if len(event.Choices) > 0 {
			if event.Choices[0].Delta.Content != "" {
				fmt.Fprint(w, event.Choices[0].Delta.Content)
			}
			if event.Choices[0].FinishReason == "length" {
				truncated = true
			}
		}
	}

	fmt.Fprintln(w)
	if truncated {
		fmt.Fprintln(w, "[response truncated at max_tokens]")
	}
	return scanner.Err()
}

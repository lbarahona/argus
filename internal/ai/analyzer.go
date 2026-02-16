package ai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	anthropicAPI     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	model            = "claude-sonnet-4-20250514"
)

const systemPrompt = `You are an expert Site Reliability Engineer (SRE) analyzing observability data from Signoz.

Your role:
- Analyze logs, metrics, and traces to identify issues
- Provide clear, actionable insights
- Prioritize by severity and impact
- Suggest root causes and remediation steps
- Use concise, technical language appropriate for SREs

When analyzing data:
1. Start with a brief summary of what you see
2. Highlight anomalies or errors
3. Identify patterns or correlations
4. Suggest next steps or investigation paths

Format your response with clear sections using markdown.`

// Analyzer handles AI-powered analysis via Anthropic Claude.
type Analyzer struct {
	apiKey string
	client *http.Client
}

// New creates a new Analyzer.
func New(apiKey string) *Analyzer {
	return &Analyzer{
		apiKey: apiKey,
		client: &http.Client{},
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
	Stream    bool      `json:"stream"`
}

// Analyze sends data to Claude and streams the response to the writer.
func (a *Analyzer) Analyze(prompt string, w io.Writer) error {
	reqBody := request{
		Model:     model,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages: []message{
			{Role: "user", Content: prompt},
		},
		Stream: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, anthropicAPI, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Anthropic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	return a.streamResponse(resp.Body, w)
}

func (a *Analyzer) streamResponse(body io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(body)
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

// AnalyzeSync sends data to Claude and returns the full response (non-streaming).
func (a *Analyzer) AnalyzeSync(prompt string) (string, error) {
	var buf bytes.Buffer
	if err := a.Analyze(prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

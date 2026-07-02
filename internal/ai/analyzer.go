package ai

import (
	"bytes"
	"context"
	"io"
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

// Message represents a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Analyzer handles AI-powered analysis. It wraps a Provider for backward compatibility.
type Analyzer struct {
	provider Provider
	// Keep apiKey and client for tests that inspect internals.
	apiKey string
	client interface{}
}

// New creates a new Analyzer using Anthropic as the default provider.
// This is the backward-compatible constructor — existing code that calls ai.New(key) still works.
func New(apiKey string) *Analyzer {
	p := NewAnthropicProvider(apiKey, defaultAnthropicModel)
	return &Analyzer{
		provider: p,
		apiKey:   apiKey,
		client:   p.client,
	}
}

// NewFromProvider creates an Analyzer wrapping any Provider.
func NewFromProvider(p Provider) *Analyzer {
	return &Analyzer{
		provider: p,
	}
}

// Analyze sends data to the AI provider and streams the response to the writer.
func (a *Analyzer) Analyze(prompt string, w io.Writer) error {
	return a.AnalyzeContext(context.Background(), prompt, w)
}

// AnalyzeContext is Analyze with caller-controlled cancellation.
func (a *Analyzer) AnalyzeContext(ctx context.Context, prompt string, w io.Writer) error {
	return a.provider.Analyze(ctx, prompt, w)
}

// AnalyzeWithHistory sends a multi-turn conversation and streams the response.
func (a *Analyzer) AnalyzeWithHistory(systemPrompt string, messages []Message, w io.Writer) error {
	return a.AnalyzeWithHistoryContext(context.Background(), systemPrompt, messages, w)
}

// AnalyzeWithHistoryContext is AnalyzeWithHistory with cancellation.
func (a *Analyzer) AnalyzeWithHistoryContext(ctx context.Context, systemPrompt string, messages []Message, w io.Writer) error {
	return a.provider.AnalyzeWithSystem(ctx, systemPrompt, messages, w)
}

// AnalyzeSync sends data and returns the full response (non-streaming).
func (a *Analyzer) AnalyzeSync(prompt string) (string, error) {
	return a.AnalyzeSyncContext(context.Background(), prompt)
}

// AnalyzeSyncContext is AnalyzeSync with cancellation.
func (a *Analyzer) AnalyzeSyncContext(ctx context.Context, prompt string) (string, error) {
	var buf bytes.Buffer
	if err := a.AnalyzeContext(ctx, prompt, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// streamResponse is kept for backward compatibility with tests.
// It delegates to the Anthropic stream parser.
func (a *Analyzer) streamResponse(body io.Reader, w io.Writer) error {
	return streamAnthropicResponse(body, w)
}

package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestBedrockProvider(t *testing.T, handler http.HandlerFunc) *BedrockProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &BedrockProvider{
		endpoint: server.URL,
		token:    "test-token",
		model:    defaultBedrockModel,
		client:   &http.Client{},
	}
}

func TestBedrock_StreamResponse_AnthropicFormat(t *testing.T) {
	// Bedrock with Anthropic models uses Anthropic SSE format
	input := sseResponse("Hello ", "Bedrock")
	var buf bytes.Buffer
	if err := streamBedrockResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "Hello Bedrock\n" {
		t.Errorf("got %q, want %q", got, "Hello Bedrock\n")
	}
}

func TestBedrock_StreamResponse_DirectJSON(t *testing.T) {
	// Some Bedrock endpoints use newline-delimited JSON
	input := `{"type":"content_block_delta","delta":{"type":"text_delta","text":"direct"}}
{"type":"message_stop"}
`
	var buf bytes.Buffer
	if err := streamBedrockResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "direct\n" {
		t.Errorf("got %q, want %q", got, "direct\n")
	}
}

func TestBedrock_Analyze_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL contains the model
		if !strings.Contains(r.URL.Path, defaultBedrockModel) {
			t.Errorf("URL should contain model ID, got: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("bedrock ", "response"))
	}
	p := newTestBedrockProvider(t, handler)
	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "test", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "bedrock response\n" {
		t.Errorf("got %q, want %q", got, "bedrock response\n")
	}
}

func TestBedrock_Analyze_HTTPError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Forbidden")
	}
	p := newTestBedrockProvider(t, handler)
	var buf bytes.Buffer
	err := p.Analyze(t.Context(), "test", &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

func TestBedrock_VerifyRequestFormat(t *testing.T) {
	var capturedBody bedrockRequest
	var gotAuth string

	handler := func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("ok"))
	}
	p := newTestBedrockProvider(t, handler)
	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "my prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization: got %q, want %q", gotAuth, "Bearer test-token")
	}
	if capturedBody.AnthropicVersion != anthropicVersion {
		t.Errorf("anthropic_version: got %q, want %q", capturedBody.AnthropicVersion, anthropicVersion)
	}
	if capturedBody.MaxTokens != 4096 {
		t.Errorf("max_tokens: got %d, want 4096", capturedBody.MaxTokens)
	}
	if len(capturedBody.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedBody.Messages))
	}
	if capturedBody.Messages[0].Content != "my prompt" {
		t.Errorf("content: got %q, want %q", capturedBody.Messages[0].Content, "my prompt")
	}
}

func TestBedrock_AnalyzeSync(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("sync ", "bedrock"))
	}
	p := newTestBedrockProvider(t, handler)
	result, err := p.AnalyzeSync(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "sync bedrock\n" {
		t.Errorf("got %q, want %q", result, "sync bedrock\n")
	}
}

func TestBedrock_Name(t *testing.T) {
	p := NewBedrockProvider("https://endpoint", "token", "")
	if p.Name() != "bedrock" {
		t.Errorf("expected bedrock, got %q", p.Name())
	}
}

func TestBedrock_DefaultModel(t *testing.T) {
	p := NewBedrockProvider("https://endpoint", "token", "")
	if p.Model() != defaultBedrockModel {
		t.Errorf("expected %q, got %q", defaultBedrockModel, p.Model())
	}
}

func TestBedrock_EndpointTrailingSlash(t *testing.T) {
	p := NewBedrockProvider("https://endpoint/", "token", "model")
	// Should strip trailing slash
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			t.Error("double slash in URL path")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("ok"))
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	p.endpoint = server.URL
	p.client = &http.Client{}

	var buf bytes.Buffer
	_ = p.Analyze(t.Context(), "test", &buf)
}

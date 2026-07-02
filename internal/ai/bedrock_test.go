package ai

import (
	"bytes"
	"context"
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

// bedrockJSONResponse builds a non-streaming Bedrock invoke response body
// (plain Anthropic-messages JSON) from text fragments.
func bedrockJSONResponse(texts ...string) string {
	var sb strings.Builder
	sb.WriteString(`{"content":[`)
	for i, text := range texts {
		if i > 0 {
			sb.WriteString(",")
		}
		encoded, _ := json.Marshal(text)
		sb.WriteString(fmt.Sprintf(`{"type":"text","text":%s}`, encoded))
	}
	sb.WriteString(`],"stop_reason":"end_turn"}`)
	return sb.String()
}

func TestBedrock_Analyze_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL contains the model
		if !strings.Contains(r.URL.Path, defaultBedrockModel) {
			t.Errorf("URL should contain model ID, got: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, bedrockJSONResponse("bedrock ", "response"))
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, bedrockJSONResponse("ok"))
	}
	p := newTestBedrockProvider(t, handler)
	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "my prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization: got %q, want %q", gotAuth, "Bearer test-token")
	}
	if capturedBody.AnthropicVersion != bedrockAnthropicVersion {
		t.Errorf("anthropic_version: got %q, want %q (Bedrock rejects the direct-API value)", capturedBody.AnthropicVersion, bedrockAnthropicVersion)
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, bedrockJSONResponse("sync ", "bedrock"))
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

func TestBedrockUsesBedrockAnthropicVersionAndInvokeEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"analysis result"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	p := NewBedrockProvider(server.URL, "test-token", "anthropic.claude-3-5-sonnet-20241022-v2:0")
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "why is api failing?", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody["anthropic_version"] != "bedrock-2023-05-31" {
		t.Errorf("anthropic_version = %v, want bedrock-2023-05-31 (Bedrock rejects the direct-API value)", gotBody["anthropic_version"])
	}
	if gotPath != "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/invoke" {
		t.Errorf("path = %q, want the non-streaming invoke endpoint", gotPath)
	}
	if !strings.Contains(buf.String(), "analysis result") {
		t.Errorf("response text not written to writer, got %q", buf.String())
	}
}

func TestBedrockEmptyContentIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"content":[],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	p := NewBedrockProvider(server.URL, "tok", "m")
	var buf bytes.Buffer
	if err := p.Analyze(context.Background(), "q", &buf); err == nil {
		t.Error("empty content must return an error, not silent empty output")
	}
}

func TestBedrock_EndpointTrailingSlash(t *testing.T) {
	p := NewBedrockProvider("https://endpoint/", "token", "model")
	// Should strip trailing slash
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "//") {
			t.Error("double slash in URL path")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, bedrockJSONResponse("ok"))
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	p.endpoint = server.URL
	p.client = &http.Client{}

	var buf bytes.Buffer
	_ = p.Analyze(t.Context(), "test", &buf)
}

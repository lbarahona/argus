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

// openaiSSEResponse builds an OpenAI-format SSE payload.
func openaiSSEResponse(texts ...string) string {
	var sb strings.Builder
	for _, text := range texts {
		sb.WriteString(fmt.Sprintf(`data: {"choices":[{"delta":{"content":%q}}]}`, text))
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

func newTestOpenAIProvider(t *testing.T, handler http.HandlerFunc) *OpenAIProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &OpenAIProvider{
		apiKey: "test-key",
		model:  defaultOpenAIModel,
		client: &http.Client{Transport: &testTransport{server: server}},
	}
}

func TestOpenAI_StreamResponse(t *testing.T) {
	input := openaiSSEResponse("Hello ", "world")
	var buf bytes.Buffer
	if err := streamOpenAIResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "Hello world\n" {
		t.Errorf("got %q, want %q", got, "Hello world\n")
	}
}

func TestOpenAI_Analyze_Success(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openaiSSEResponse("test ", "output"))
	}
	p := newTestOpenAIProvider(t, handler)
	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "test", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "test output\n" {
		t.Errorf("got %q, want %q", got, "test output\n")
	}
}

func TestOpenAI_Analyze_HTTPError(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "Unauthorized")
	}
	p := newTestOpenAIProvider(t, handler)
	var buf bytes.Buffer
	err := p.Analyze(t.Context(), "test", &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}

func TestOpenAI_VerifyRequestFormat(t *testing.T) {
	var capturedBody openaiRequest
	var gotAuth string

	handler := func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openaiSSEResponse("ok"))
	}
	p := newTestOpenAIProvider(t, handler)
	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "my prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization: got %q, want %q", gotAuth, "Bearer test-key")
	}
	if capturedBody.Model != defaultOpenAIModel {
		t.Errorf("model: got %q, want %q", capturedBody.Model, defaultOpenAIModel)
	}
	if !capturedBody.Stream {
		t.Error("expected stream=true")
	}
	// First message should be system
	if len(capturedBody.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (system+user), got %d", len(capturedBody.Messages))
	}
	if capturedBody.Messages[0].Role != "system" {
		t.Errorf("first message role: got %q, want system", capturedBody.Messages[0].Role)
	}
	if capturedBody.Messages[1].Role != "user" {
		t.Errorf("second message role: got %q, want user", capturedBody.Messages[1].Role)
	}
	if capturedBody.Messages[1].Content != "my prompt" {
		t.Errorf("user content: got %q, want %q", capturedBody.Messages[1].Content, "my prompt")
	}
}

func TestOpenAI_AnalyzeSync(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, openaiSSEResponse("sync ", "result"))
	}
	p := newTestOpenAIProvider(t, handler)
	result, err := p.AnalyzeSync(t.Context(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "sync result\n" {
		t.Errorf("got %q, want %q", result, "sync result\n")
	}
}

func TestOpenAI_StreamResponse_MalformedJSON(t *testing.T) {
	input := "data: {bad json}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"valid\"}}]}\n\ndata: [DONE]\n\n"
	var buf bytes.Buffer
	if err := streamOpenAIResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "valid\n" {
		t.Errorf("got %q, want %q", got, "valid\n")
	}
}

func TestOpenAI_StreamResponse_EmptyChoices(t *testing.T) {
	input := "data: {\"choices\":[]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
	var buf bytes.Buffer
	if err := streamOpenAIResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := buf.String(); got != "ok\n" {
		t.Errorf("got %q, want %q", got, "ok\n")
	}
}

func TestOpenAI_Name(t *testing.T) {
	p := NewOpenAIProvider("key", "")
	if p.Name() != "openai" {
		t.Errorf("expected openai, got %q", p.Name())
	}
}

func TestOpenAI_DefaultModel(t *testing.T) {
	p := NewOpenAIProvider("key", "")
	if p.Model() != defaultOpenAIModel {
		t.Errorf("expected %q, got %q", defaultOpenAIModel, p.Model())
	}
}

func TestOpenAI_CustomModel(t *testing.T) {
	p := NewOpenAIProvider("key", "gpt-4o-mini")
	if p.Model() != "gpt-4o-mini" {
		t.Errorf("expected gpt-4o-mini, got %q", p.Model())
	}
}

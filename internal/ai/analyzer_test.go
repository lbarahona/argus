package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// testTransport redirects all requests to a test server regardless of the original URL.
type testTransport struct {
	server *httptest.Server
}

func (tt *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	newReq := req.Clone(req.Context())
	u, err := url.Parse(tt.server.URL)
	if err != nil {
		return nil, err
	}
	newReq.URL = u
	newReq.Host = u.Host
	return http.DefaultTransport.RoundTrip(newReq)
}

// newTestAnalyzer creates an Analyzer whose HTTP client is redirected to the given handler.
func newTestAnalyzer(t *testing.T, handler http.HandlerFunc) *Analyzer {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := &AnthropicProvider{
		apiKey: "test-key",
		model:  defaultAnthropicModel,
		client: &http.Client{Transport: &testTransport{server: server}},
	}
	return &Analyzer{
		provider: p,
		apiKey:   "test-key",
	}
}

// sseResponse builds an SSE payload that emits each text fragment as a content_block_delta event.
func sseResponse(texts ...string) string {
	var sb strings.Builder
	for _, text := range texts {
		sb.WriteString(fmt.Sprintf(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":%q}}`, text))
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

// okHandler returns a handler that responds with a valid SSE stream containing the supplied texts.
func okHandler(texts ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse(texts...))
	}
}

// errorHandler returns a handler that responds with the given HTTP status and body.
func errorHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

// ---- Existing tests (preserved) ----

func TestAnalyzerBuildsCorrectRequest(t *testing.T) {
	var capturedReq anthropicRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("missing API key header")
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("wrong anthropic-version header")
		}

		json.NewDecoder(r.Body).Decode(&capturedReq)

		// Return a simple SSE response
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	// We can't easily override the const API URL, so verify request structure
	_ = server // server used to validate handler logic above

	reqBody := anthropicRequest{
		Model:     defaultAnthropicModel,
		MaxTokens: 4096,
		System:    systemPrompt,
		Messages:  []Message{{Role: "user", Content: "test prompt"}},
		Stream:    true,
	}

	if reqBody.Model != defaultAnthropicModel {
		t.Errorf("expected model=%s", defaultAnthropicModel)
	}
	if reqBody.System != systemPrompt {
		t.Error("expected system prompt to be set")
	}
	if len(reqBody.Messages) != 1 || reqBody.Messages[0].Content != "test prompt" {
		t.Error("unexpected messages")
	}
	if !reqBody.Stream {
		t.Error("expected stream=true")
	}
}

func TestStreamResponse(t *testing.T) {
	input := `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello "}}

data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}

data: [DONE]

`

	analyzer := &Analyzer{}
	var buf bytes.Buffer
	err := analyzer.streamResponse(bytes.NewBufferString(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Hello world\n"
	if buf.String() != expected {
		t.Errorf("got %q, want %q", buf.String(), expected)
	}
}

func TestStreamResponseIgnoresNonDelta(t *testing.T) {
	input := `data: {"type":"message_start","message":{"id":"msg_1"}}

data: {"type":"content_block_start","index":0}

data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}

data: {"type":"message_stop"}

`

	analyzer := &Analyzer{}
	var buf bytes.Buffer
	err := analyzer.streamResponse(bytes.NewBufferString(input), &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.String() != "ok\n" {
		t.Errorf("got %q, want %q", buf.String(), "ok\n")
	}
}

// ---- New tests ----

func TestNew(t *testing.T) {
	a := New("my-api-key")
	if a == nil {
		t.Fatal("expected non-nil Analyzer")
	}
	if a.apiKey != "my-api-key" {
		t.Errorf("got apiKey=%q, want %q", a.apiKey, "my-api-key")
	}
	if a.client == nil {
		t.Error("expected non-nil HTTP client")
	}
}

func TestAnalyze_Success(t *testing.T) {
	a := newTestAnalyzer(t, okHandler("Hello ", "world"))

	var buf bytes.Buffer
	if err := a.Analyze("test prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "Hello world\n" {
		t.Errorf("got %q, want %q", got, "Hello world\n")
	}
}

func TestAnalyze_HTTPError_401(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusUnauthorized, "Unauthorized"))

	var buf bytes.Buffer
	err := a.Analyze("test prompt", &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected error to contain '401', got: %v", err)
	}
}

func TestAnalyze_HTTPError_500(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusInternalServerError, "Internal Server Error"))

	var buf bytes.Buffer
	err := a.Analyze("test prompt", &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain '500', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Anthropic API error") {
		t.Errorf("expected error to contain 'Anthropic API error', got: %v", err)
	}
}

func TestAnalyze_VerifyRequestHeaders(t *testing.T) {
	var gotAPIKey, gotVersion, gotContentType string

	handler := func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("ok"))
	}

	a := newTestAnalyzer(t, handler)
	var buf bytes.Buffer
	if err := a.Analyze("prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAPIKey != "test-key" {
		t.Errorf("x-api-key: got %q, want %q", gotAPIKey, "test-key")
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version: got %q, want %q", gotVersion, anthropicVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", gotContentType, "application/json")
	}
}

func TestAnalyze_VerifyRequestBody(t *testing.T) {
	var capturedReq anthropicRequest

	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("done"))
	}

	a := newTestAnalyzer(t, handler)
	var buf bytes.Buffer
	if err := a.Analyze("my test prompt", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.Model != defaultAnthropicModel {
		t.Errorf("model: got %q, want %q", capturedReq.Model, defaultAnthropicModel)
	}
	if capturedReq.MaxTokens != 4096 {
		t.Errorf("max_tokens: got %d, want 4096", capturedReq.MaxTokens)
	}
	if !capturedReq.Stream {
		t.Error("expected stream=true")
	}
	if capturedReq.System != systemPrompt {
		t.Errorf("system prompt mismatch: got %q", capturedReq.System)
	}
	if len(capturedReq.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "user" {
		t.Errorf("message role: got %q, want %q", capturedReq.Messages[0].Role, "user")
	}
	if capturedReq.Messages[0].Content != "my test prompt" {
		t.Errorf("message content: got %q, want %q", capturedReq.Messages[0].Content, "my test prompt")
	}
}

func TestAnalyzeSync_Success(t *testing.T) {
	a := newTestAnalyzer(t, okHandler("buffered ", "response"))

	result, err := a.AnalyzeSync("sync prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "buffered response\n" {
		t.Errorf("got %q, want %q", result, "buffered response\n")
	}
}

func TestAnalyzeSync_HTTPError(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusForbidden, "Forbidden"))

	_, err := a.AnalyzeSync("prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to contain '403', got: %v", err)
	}
}

func TestAnalyzeWithHistory_Success(t *testing.T) {
	a := newTestAnalyzer(t, okHandler("history ", "response"))

	messages := []Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "follow-up question"},
	}

	var buf bytes.Buffer
	if err := a.AnalyzeWithHistory("custom system prompt", messages, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "history response\n" {
		t.Errorf("got %q, want %q", got, "history response\n")
	}
}

func TestAnalyzeWithHistory_VerifyRequest(t *testing.T) {
	var capturedReq anthropicRequest

	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("ok"))
	}

	a := newTestAnalyzer(t, handler)

	customSystem := "You are a custom assistant."
	messages := []Message{
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer one"},
		{Role: "user", Content: "question two"},
	}

	var buf bytes.Buffer
	if err := a.AnalyzeWithHistory(customSystem, messages, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.System != customSystem {
		t.Errorf("system: got %q, want %q", capturedReq.System, customSystem)
	}
	if capturedReq.Model != defaultAnthropicModel {
		t.Errorf("model: got %q, want %q", capturedReq.Model, defaultAnthropicModel)
	}
	if capturedReq.MaxTokens != 4096 {
		t.Errorf("max_tokens: got %d, want 4096", capturedReq.MaxTokens)
	}
	if !capturedReq.Stream {
		t.Error("expected stream=true")
	}
	if len(capturedReq.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(capturedReq.Messages))
	}
	if capturedReq.Messages[0].Role != "user" || capturedReq.Messages[0].Content != "question one" {
		t.Errorf("message[0] mismatch: %+v", capturedReq.Messages[0])
	}
	if capturedReq.Messages[1].Role != "assistant" || capturedReq.Messages[1].Content != "answer one" {
		t.Errorf("message[1] mismatch: %+v", capturedReq.Messages[1])
	}
	if capturedReq.Messages[2].Role != "user" || capturedReq.Messages[2].Content != "question two" {
		t.Errorf("message[2] mismatch: %+v", capturedReq.Messages[2])
	}
}

func TestAnalyzeWithHistory_HTTPError(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusBadRequest, "bad request body"))

	var buf bytes.Buffer
	err := a.AnalyzeWithHistory("system", []Message{{Role: "user", Content: "hi"}}, &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to contain '400', got: %v", err)
	}
	if !strings.Contains(err.Error(), "Anthropic API error") {
		t.Errorf("expected error to contain 'Anthropic API error', got: %v", err)
	}
}

func TestAnalyzeWithHistory_ErrorBodyIncludedInError(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusUnauthorized, "invalid api key"))

	var buf bytes.Buffer
	err := a.AnalyzeWithHistory("system", []Message{{Role: "user", Content: "hi"}}, &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("expected error body in message, got: %v", err)
	}
}

func TestAnalyze_ErrorBodyIncludedInError(t *testing.T) {
	a := newTestAnalyzer(t, errorHandler(http.StatusUnauthorized, "bad api key"))

	var buf bytes.Buffer
	err := a.Analyze("prompt", &buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bad api key") {
		t.Errorf("expected error body in message, got: %v", err)
	}
}

func TestStreamResponse_EmptyInput(t *testing.T) {
	// An SSE stream with only a blank line produces just the trailing newline.
	a := &Analyzer{}
	var buf bytes.Buffer
	if err := a.streamResponse(strings.NewReader("\n"), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// streamResponse always writes a trailing newline at the end.
	if buf.String() != "\n" {
		t.Errorf("got %q, want %q", buf.String(), "\n")
	}
}

func TestStreamResponse_MultipleDeltas(t *testing.T) {
	input := sseResponse("one", " two", " three")

	a := &Analyzer{}
	var buf bytes.Buffer
	if err := a.streamResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "one two three\n" {
		t.Errorf("got %q, want %q", got, "one two three\n")
	}
}

func TestStreamResponse_MalformedJSON(t *testing.T) {
	// Malformed JSON lines after "data: " should be silently skipped.
	input := "data: {not valid json}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"valid\"}}\n\ndata: [DONE]\n\n"

	a := &Analyzer{}
	var buf bytes.Buffer
	if err := a.streamResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "valid\n" {
		t.Errorf("got %q, want %q", got, "valid\n")
	}
}

func TestStreamResponse_NonDataLines(t *testing.T) {
	// Lines that do not start with "data: " are ignored.
	input := "event: message_start\nid: 1\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\ndata: [DONE]\n\n"

	a := &Analyzer{}
	var buf bytes.Buffer
	if err := a.streamResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "hi\n" {
		t.Errorf("got %q, want %q", got, "hi\n")
	}
}

func TestStreamResponse_DoneStopsProcessing(t *testing.T) {
	// Text after [DONE] should not appear in output.
	input := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"before\"}}\n\ndata: [DONE]\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"after\"}}\n\n"

	a := &Analyzer{}
	var buf bytes.Buffer
	if err := a.streamResponse(strings.NewReader(input), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := buf.String(); got != "before\n" {
		t.Errorf("got %q, want %q", got, "before\n")
	}
}

func TestAnalyzeWithHistory_VerifyHeaders(t *testing.T) {
	var gotAPIKey, gotVersion string

	handler := func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("ok"))
	}

	a := newTestAnalyzer(t, handler)
	var buf bytes.Buffer
	if err := a.AnalyzeWithHistory("sys", []Message{{Role: "user", Content: "hi"}}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotAPIKey != "test-key" {
		t.Errorf("x-api-key: got %q, want %q", gotAPIKey, "test-key")
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version: got %q, want %q", gotVersion, anthropicVersion)
	}
}

// ---- Stream robustness tests (error events, max_tokens truncation, long lines) ----

// newTestAnthropicProvider creates an AnthropicProvider whose HTTP client is
// redirected to the given handler, mirroring newTestOpenAIProvider.
func newTestAnthropicProvider(t *testing.T, handler http.HandlerFunc) *AnthropicProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &AnthropicProvider{
		apiKey: "test-key",
		model:  defaultAnthropicModel,
		client: &http.Client{Transport: &testTransport{server: server}},
	}
}

func TestAnthropicStreamErrorEventFailsLoudly(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}
	p := newTestAnthropicProvider(t, handler)

	var buf bytes.Buffer
	err := p.Analyze(t.Context(), "q", &buf)
	if err == nil {
		t.Fatal("mid-stream error event must surface as an error, not silent truncation")
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error should carry the API message, got %v", err)
	}
}

func TestAnthropicStreamMaxTokensNoted(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\n")
	}
	p := newTestAnthropicProvider(t, handler)

	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "q", &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "truncated at max_tokens") {
		t.Errorf("max_tokens truncation must be noted in output, got %q", buf.String())
	}
}

func TestAnthropicStreamLongLine(t *testing.T) {
	long := strings.Repeat("x", 200*1024) // > default 64KB scanner cap
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"%s\"}}\n\n", long)
	}
	p := newTestAnthropicProvider(t, handler)

	var buf bytes.Buffer
	if err := p.Analyze(t.Context(), "q", &buf); err != nil {
		t.Fatalf("200KB SSE line must not kill the stream: %v", err)
	}
	if len(buf.String()) < 200*1024 {
		t.Errorf("long delta truncated: got %d bytes", len(buf.String()))
	}
}

func TestAnalyzeWithHistory_EmptyMessages(t *testing.T) {
	var capturedReq anthropicRequest

	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("empty"))
	}

	a := newTestAnalyzer(t, handler)
	var buf bytes.Buffer
	if err := a.AnalyzeWithHistory("sys", []Message{}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(capturedReq.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(capturedReq.Messages))
	}
}

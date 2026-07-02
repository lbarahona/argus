package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSignozContextTestConfig() *types.Config {
	return &types.Config{
		AnthropicKey:    "sk-test",
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod":    {URL: "https://prod.example.com", Name: "Prod"},
			"staging": {URL: "https://staging.example.com", Name: "Staging"},
		},
	}
}

func TestNewSignozContext_ResolvesDefaultInstance(t *testing.T) {
	setupTestConfig(t, newSignozContextTestConfig())

	sctx, err := newSignozContext("")
	require.NoError(t, err)
	require.NotNil(t, sctx)
	assert.Equal(t, "prod", sctx.instKey)
	assert.Equal(t, "https://prod.example.com", sctx.inst.URL)
	assert.NotNil(t, sctx.client)
	assert.NotNil(t, sctx.cfg)
}

func TestNewSignozContext_ResolvesExplicitInstance(t *testing.T) {
	setupTestConfig(t, newSignozContextTestConfig())

	sctx, err := newSignozContext("staging")
	require.NoError(t, err)
	require.NotNil(t, sctx)
	assert.Equal(t, "staging", sctx.instKey)
	assert.Equal(t, "https://staging.example.com", sctx.inst.URL)
	assert.NotNil(t, sctx.client)
}

func TestNewSignozContext_UnknownInstanceErrors(t *testing.T) {
	setupTestConfig(t, newSignozContextTestConfig())

	sctx, err := newSignozContext("nope")
	require.Error(t, err)
	assert.Nil(t, sctx)
	assert.Contains(t, err.Error(), "nope")
}

func TestNewSignozContext_NoConfigFileErrors(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	sctx, err := newSignozContext("")
	require.Error(t, err)
	assert.Nil(t, sctx)
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything written to it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	fn()

	require.NoError(t, w.Close())
	os.Stdout = orig

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestRenderOutput_TerminalSynonymsDispatchToTerminal(t *testing.T) {
	for _, format := range []string{"", "terminal", "text", "table"} {
		t.Run(format, func(t *testing.T) {
			called := false
			err := renderOutput(format, func() error {
				called = true
				return nil
			}, func() error {
				t.Fatal("markdown renderer should not be called")
				return nil
			}, nil)
			require.NoError(t, err)
			assert.True(t, called)
		})
	}
}

func TestRenderOutput_MarkdownSynonymsDispatchToMarkdown(t *testing.T) {
	for _, format := range []string{"markdown", "md"} {
		t.Run(format, func(t *testing.T) {
			called := false
			err := renderOutput(format, func() error {
				t.Fatal("terminal renderer should not be called")
				return nil
			}, func() error {
				called = true
				return nil
			}, nil)
			require.NoError(t, err)
			assert.True(t, called)
		})
	}
}

func TestRenderOutput_JSONMarshalsAndPrintsValue(t *testing.T) {
	value := map[string]string{"hello": "world"}

	out := captureStdout(t, func() {
		err := renderOutput("json", func() error {
			t.Fatal("terminal renderer should not be called")
			return nil
		}, nil, value)
		require.NoError(t, err)
	})

	expected, err := jsonMarshal(value)
	require.NoError(t, err)
	assert.Equal(t, string(expected)+"\n", out)
}

func TestRenderOutput_JSONRawMessagePrintsVerbatim(t *testing.T) {
	// Pre-serialized JSON (e.g. a package's hand-mapped FormatJSON schema,
	// like internal/doctor's) must pass through byte-for-byte — NOT be
	// re-marshaled, which would rewrite the formatting/schema. The 4-space
	// indentation here is deliberately different from jsonMarshal's 2-space
	// indent so a re-marshal path fails this test.
	raw := json.RawMessage("{\n    \"pass\": 3,\n    \"custom_field\": \"kept\"\n}")

	out := captureStdout(t, func() {
		err := renderOutput("json", nil, nil, raw)
		require.NoError(t, err)
	})

	assert.Equal(t, "{\n    \"pass\": 3,\n    \"custom_field\": \"kept\"\n}\n", out)
}

func TestRenderOutput_NilTerminalRendererErrors(t *testing.T) {
	err := renderOutput("terminal", nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "terminal output not supported here")
}

func TestRenderOutput_NilMarkdownRendererErrors(t *testing.T) {
	err := renderOutput("markdown", func() error { return nil }, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "markdown output is not supported by this command")
}

func TestRenderOutput_NilJSONValueErrors(t *testing.T) {
	err := renderOutput("json", func() error { return nil }, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "json output is not supported by this command")
}

func TestRenderOutput_UnknownFormatErrors(t *testing.T) {
	err := renderOutput("bogus", func() error { return nil }, func() error { return nil }, "value")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown format "bogus"`)
	assert.Contains(t, err.Error(), "valid: terminal, markdown, json")
}

func TestRenderOutput_TerminalRendererErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	err := renderOutput("terminal", func() error {
		return sentinel
	}, nil, nil)
	require.ErrorIs(t, err, sentinel)
}

func TestRenderOutput_MarkdownRendererErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	err := renderOutput("markdown", nil, func() error {
		return sentinel
	}, nil)
	require.ErrorIs(t, err, sentinel)
}

func TestRenderOutput_JSONMarshalErrorPropagates(t *testing.T) {
	// Functions cannot be marshaled to JSON, so this exercises the
	// jsonMarshal error path.
	err := renderOutput("json", nil, nil, func() {})
	require.Error(t, err)
}

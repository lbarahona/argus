package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/spf13/cobra"
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

func TestMinutesValue_Set(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "bare minutes", input: "90", want: 90},
		{name: "minutes suffix", input: "90m", want: 90},
		{name: "hours suffix", input: "2h", want: 120},
		{name: "hours and minutes", input: "1h30m", want: 90},
		{name: "junk", input: "junk", wantErr: true},
		{name: "negative bare", input: "-5", wantErr: true},
		{name: "negative duration", input: "-5m", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target int
			mv := newMinutesValue(0, &target)
			err := mv.Set(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, target)
			assert.Equal(t, tt.want, int(*mv))
			assert.Equal(t, strconv.Itoa(tt.want), mv.String())
		})
	}
}

func TestNewMinutesValue_DefaultPreservedWhenUnset(t *testing.T) {
	var target int
	mv := newMinutesValue(42, &target)
	assert.Equal(t, 42, target)
	assert.Equal(t, "42", mv.String())
	assert.Equal(t, "duration", mv.Type())
}

func TestAddDurationFlag_ParsesFromCommandLine(t *testing.T) {
	cmd := &cobra.Command{Use: "test", RunE: func(cmd *cobra.Command, args []string) error { return nil }}
	var duration int
	addDurationFlag(cmd, &duration, 60, "test duration flag")

	assert.Equal(t, 60, duration)

	cmd.SetArgs([]string{"--duration", "2h"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, 120, duration)
}

func TestFormatSet_List(t *testing.T) {
	tests := []struct {
		name string
		fs   formatSet
		want []string
	}{
		{name: "terminal only", fs: formatSet{}, want: []string{"terminal"}},
		{name: "markdown only", fs: formatSet{Markdown: true}, want: []string{"terminal", "markdown"}},
		{name: "json only", fs: formatSet{JSON: true}, want: []string{"terminal", "json"}},
		{name: "markdown and json", fs: formatSet{Markdown: true, JSON: true}, want: []string{"terminal", "markdown", "json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.fs.list())
		})
	}
}

func TestFormatSet_Validate(t *testing.T) {
	tests := []struct {
		name       string
		fs         formatSet
		format     string
		wantErr    bool
		errContain string
	}{
		// terminal + its synonyms are always accepted, regardless of set.
		{name: "empty format always ok", fs: formatSet{}, format: ""},
		{name: "terminal always ok", fs: formatSet{}, format: "terminal"},
		{name: "text synonym always ok", fs: formatSet{}, format: "text"},
		{name: "table synonym always ok", fs: formatSet{}, format: "table"},

		// in-set passes, including synonyms.
		{name: "markdown in set", fs: formatSet{Markdown: true}, format: "markdown"},
		{name: "md synonym in set", fs: formatSet{Markdown: true}, format: "md"},
		{name: "json in set", fs: formatSet{JSON: true}, format: "json"},

		// out-of-set: known format, but not supported by this command.
		{
			name: "markdown out of set", fs: formatSet{JSON: true}, format: "markdown",
			wantErr: true, errContain: `format "markdown" is not supported by this command (valid: terminal, json)`,
		},
		{
			name: "md synonym out of set", fs: formatSet{JSON: true}, format: "md",
			wantErr: true, errContain: `format "md" is not supported by this command (valid: terminal, json)`,
		},
		{
			name: "json out of set", fs: formatSet{Markdown: true}, format: "json",
			wantErr: true, errContain: `format "json" is not supported by this command (valid: terminal, markdown)`,
		},
		{
			name: "json out of set with empty set", fs: formatSet{}, format: "json",
			wantErr: true, errContain: `format "json" is not supported by this command (valid: terminal)`,
		},

		// unknown formats are always rejected, with the set's own list.
		{
			name: "unknown format", fs: formatSet{Markdown: true}, format: "bogus",
			wantErr: true, errContain: `unknown format "bogus" (valid: terminal, markdown)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fs.validate(tt.format)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContain)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestAddInstanceFlag_RegistersWithoutPanic(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var instance string

	assert.NotPanics(t, func() {
		addInstanceFlag(cmd, &instance)
	})

	flag := cmd.Flags().Lookup("instance")
	require.NotNil(t, flag)
	assert.Equal(t, "i", flag.Shorthand)
}

func TestAddInstanceFlag_CompletionReturnsConfiguredInstances(t *testing.T) {
	setupTestConfig(t, newSignozContextTestConfig())

	cmd := &cobra.Command{Use: "test"}
	var instance string
	addInstanceFlag(cmd, &instance)

	completionFunc, exists := cmd.GetFlagCompletionFunc("instance")
	require.True(t, exists)
	completions, directive := completionFunc(cmd, nil, "")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
	assert.ElementsMatch(t, []string{"prod", "staging"}, completions)
}

func TestAddInstanceFlag_CompletionHandlesMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cmd := &cobra.Command{Use: "test"}
	var instance string
	addInstanceFlag(cmd, &instance)

	// config.Load() returns an empty (non-error) config when the file is
	// simply absent, so completions should be an empty, non-nil slice.
	completionFunc, exists := cmd.GetFlagCompletionFunc("instance")
	require.True(t, exists)
	completions, directive := completionFunc(cmd, nil, "")
	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestAddInstanceFlag_CompletionHandlesUnreadableConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	argusDir := tmpDir + "/.argus"
	require.NoError(t, os.MkdirAll(argusDir, 0700))
	// Malformed YAML causes config.Load() to return an error, which the
	// completion func must handle gracefully instead of panicking.
	require.NoError(t, os.WriteFile(argusDir+"/config.yaml", []byte("not: valid: yaml: [["), 0600))

	cmd := &cobra.Command{Use: "test"}
	var instance string
	addInstanceFlag(cmd, &instance)

	completionFunc, exists := cmd.GetFlagCompletionFunc("instance")
	require.True(t, exists)
	completions, directive := completionFunc(cmd, nil, "")
	assert.Nil(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestAddFormatFlag_RegistersWithoutPanicAndDefault(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	var format string

	assert.NotPanics(t, func() {
		addFormatFlag(cmd, &format, "terminal", formatSet{Markdown: true, JSON: true})
	})

	flag := cmd.Flags().Lookup("format")
	require.NotNil(t, flag)
	assert.Equal(t, "f", flag.Shorthand)
	assert.Equal(t, "terminal", format)
}

func TestAddFormatFlag_CompletionReflectsSet(t *testing.T) {
	tests := []struct {
		name string
		fs   formatSet
	}{
		{name: "terminal only", fs: formatSet{}},
		{name: "markdown only", fs: formatSet{Markdown: true}},
		{name: "json only", fs: formatSet{JSON: true}},
		{name: "markdown and json", fs: formatSet{Markdown: true, JSON: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var format string
			addFormatFlag(cmd, &format, "", tt.fs)

			completionFunc, exists := cmd.GetFlagCompletionFunc("format")
			require.True(t, exists)
			completions, directive := completionFunc(cmd, nil, "")
			assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
			assert.Equal(t, tt.fs.list(), completions)
		})
	}
}

func TestAddFormatFlag_HelpTextReflectsSet(t *testing.T) {
	tests := []struct {
		name string
		fs   formatSet
		want string
	}{
		{name: "terminal only", fs: formatSet{}, want: "Output format: terminal"},
		{name: "markdown only", fs: formatSet{Markdown: true}, want: "Output format: terminal, markdown"},
		{name: "json only", fs: formatSet{JSON: true}, want: "Output format: terminal, json"},
		{name: "markdown and json", fs: formatSet{Markdown: true, JSON: true}, want: "Output format: terminal, markdown, json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			var format string
			addFormatFlag(cmd, &format, "", tt.fs)

			flag := cmd.Flags().Lookup("format")
			require.NotNil(t, flag)
			assert.Equal(t, tt.want, flag.Usage)
		})
	}
}

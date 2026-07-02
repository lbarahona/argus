package doctor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		status   Status
		expected string
	}{
		{StatusPass, "PASS"},
		{StatusWarn, "WARN"},
		{StatusFail, "FAIL"},
		{StatusSkip, "SKIP"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, tt.status.String())
	}
}

func TestStatus_Symbol(t *testing.T) {
	assert.Equal(t, "✅", StatusPass.Symbol())
	assert.Equal(t, "⚠️", StatusWarn.Symbol())
	assert.Equal(t, "❌", StatusFail.Symbol())
	assert.Equal(t, "⏭️", StatusSkip.Symbol())
}

func TestReport_Counts(t *testing.T) {
	r := &Report{
		Checks: []CheckResult{
			{Status: StatusPass},
			{Status: StatusPass},
			{Status: StatusWarn},
			{Status: StatusFail},
			{Status: StatusSkip},
		},
	}
	assert.Equal(t, 2, r.PassCount())
	assert.Equal(t, 1, r.WarnCount())
	assert.Equal(t, 1, r.FailCount())
}

func TestCheckConfigFile_NoFile(t *testing.T) {
	// This test depends on actual filesystem state
	// We test the function runs without panic
	result := checkConfigFile()
	assert.NotEmpty(t, result.Name)
	assert.Contains(t, []Status{StatusPass, StatusWarn, StatusFail}, result.Status)
}

func TestCheckDNSResolution(t *testing.T) {
	ctx := context.Background()
	result := checkDNSResolution(ctx)
	// Should at least not panic; actual result depends on network
	assert.Equal(t, "DNS resolution", result.Name)
}

func TestCheckInternetConnectivity(t *testing.T) {
	ctx := context.Background()
	result := checkInternetConnectivity(ctx)
	assert.Equal(t, "Internet connectivity", result.Name)
}

func TestRun(t *testing.T) {
	ctx := context.Background()
	report := Run(ctx, "test-version", false)

	assert.NotNil(t, report)
	assert.Equal(t, "test-version", report.Version)
	assert.NotEmpty(t, report.OS)
	assert.NotEmpty(t, report.Arch)
	assert.True(t, len(report.Checks) > 0, "should have at least some checks")
	assert.True(t, report.Duration > 0, "duration should be positive")
}

func TestFormatTerminal(t *testing.T) {
	report := &Report{
		Version: "1.0.0",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []CheckResult{
			{Name: "Test check", Status: StatusPass, Message: "All good"},
			{Name: "Warning check", Status: StatusWarn, Message: "Maybe fix this", Detail: "Some detail"},
		},
	}

	output := FormatTerminal(report, false)
	assert.Contains(t, output, "Argus Doctor")
	assert.Contains(t, output, "1.0.0")
	assert.Contains(t, output, "Test check")
	assert.Contains(t, output, "All good")
	assert.NotContains(t, output, "Some detail") // verbose=false

	// With verbose
	outputVerbose := FormatTerminal(report, true)
	assert.Contains(t, outputVerbose, "Some detail")
}

func TestFormatTerminal_WithFailures(t *testing.T) {
	report := &Report{
		Version: "1.0.0",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []CheckResult{
			{Name: "Failing check", Status: StatusFail, Message: "Broken", Detail: "Fix it"},
		},
	}

	output := FormatTerminal(report, false)
	assert.Contains(t, output, "Suggested fixes")
	assert.Contains(t, output, "Failing check")
}

func TestFormatJSON(t *testing.T) {
	report := &Report{
		Version: "1.0.0",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []CheckResult{
			{Name: "Test", Status: StatusPass, Message: "OK"},
		},
	}

	data, err := FormatJSON(report)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "1.0.0", parsed["version"])
	assert.Equal(t, float64(1), parsed["pass"])
	assert.Equal(t, float64(0), parsed["fail"])
}

func TestFormatMarkdown(t *testing.T) {
	report := &Report{
		Version: "1.0.0",
		OS:      "linux",
		Arch:    "amd64",
		Checks: []CheckResult{
			{Name: "Test", Status: StatusPass, Message: "OK"},
			{Name: "Warn", Status: StatusWarn, Message: "Hmm"},
		},
	}

	md := FormatMarkdown(report)
	assert.Contains(t, md, "# 🩺 Argus Doctor Report")
	assert.Contains(t, md, "| ✅ |")
	assert.Contains(t, md, "| ⚠️ |")
	assert.Contains(t, md, "1 passed, 1 warnings, 0 failed")
}

func TestCheckAnthropicKey_NotSet(t *testing.T) {
	// Unset env for this test
	t.Setenv("ANTHROPIC_API_KEY", "")
	result := checkAnthropicKey(nil)
	assert.Equal(t, "AI provider configured", result.Name)
	// Result depends on env state
	assert.Contains(t, []Status{StatusPass, StatusWarn}, result.Status)
}

func TestCheckInstanceURL_Valid(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		status Status
	}{
		{"https", "https://signoz.example.com", StatusPass},
		{"http localhost", "http://localhost:3301", StatusPass},
		{"http remote", "http://signoz.example.com", StatusWarn},
		{"empty", "", StatusFail},
		{"no scheme", "signoz.example.com", StatusFail},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkInstanceURL("test", types.Instance{URL: tt.url})
			assert.Equal(t, tt.status, result.Status, "URL: %s", tt.url)
		})
	}
}

func TestKeySuffix(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"abc", "abc"},
		{"", ""},
		{"abcd", "abcd"},
		{"sk-ant-abcdef1234", "1234"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, keySuffix(tt.key), "keySuffix(%q)", tt.key)
	}
}

func TestCheckAnthropicKey_ShortKey(t *testing.T) {
	cfg := &types.Config{
		AI: types.AIConfig{
			Provider:     "anthropic",
			AnthropicKey: "abc",
		},
	}
	// Must not panic on a key shorter than 4 characters.
	result := checkAnthropicKey(cfg)
	assert.Equal(t, "AI provider configured", result.Name)
}

func TestConfigPath(t *testing.T) {
	path := ConfigPath()
	assert.True(t, strings.Contains(path, ".argus"))
	assert.True(t, strings.HasSuffix(path, "config.yaml"))
}

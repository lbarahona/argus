package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/lbarahona/argus/internal/config"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestConfig(t *testing.T, cfg *types.Config) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	require.NoError(t, config.Save(cfg))
}

func testConfig() *types.Config {
	return &types.Config{
		AnthropicKey:    "sk-test",
		DefaultInstance: "mcpress",
		Instances: map[string]types.Instance{
			"mcpress":    {URL: "https://mcpress.example.com", Name: "MCPress"},
			"production": {URL: "https://prod.example.com", Name: "Production"},
		},
	}
}

func executeUseCmd(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd := useCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestUseCmd_SwitchInstance(t *testing.T) {
	setupTestConfig(t, testConfig())

	// Switch to production
	_, err := executeUseCmd("production")
	require.NoError(t, err)

	// Verify config was updated
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "production", cfg.DefaultInstance)
}

func TestUseCmd_SwitchPrintsConfirmation(t *testing.T) {
	setupTestConfig(t, testConfig())

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	_, err := executeUseCmd("production")
	require.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "Default instance set to: production ✓")
}

func TestUseCmd_InstanceNotFound(t *testing.T) {
	setupTestConfig(t, testConfig())

	_, err := executeUseCmd("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance \"nonexistent\" not found")
	assert.Contains(t, err.Error(), "Available instances:")
}

func TestUseCmd_NoArgs_ListsInstances(t *testing.T) {
	setupTestConfig(t, testConfig())

	// Capture stdout — PrintInstances writes to os.Stdout directly
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	_, err := executeUseCmd()
	require.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should list instances
	assert.Contains(t, output, "Configured Instances")
}

func TestUseCmd_NoInstances(t *testing.T) {
	setupTestConfig(t, &types.Config{
		Instances: map[string]types.Instance{},
	})

	_, err := executeUseCmd("something")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no instances configured")
}

func TestUseCmd_PreservesOtherConfig(t *testing.T) {
	cfg := testConfig()
	cfg.AnthropicKey = "sk-preserve-me"
	setupTestConfig(t, cfg)

	_, err := executeUseCmd("production")
	require.NoError(t, err)

	loaded, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "production", loaded.DefaultInstance)
	assert.Equal(t, "sk-preserve-me", loaded.AnthropicKey)
	assert.Len(t, loaded.Instances, 2)
}

func TestUseCmd_AlreadyDefault(t *testing.T) {
	setupTestConfig(t, testConfig())

	// Setting to the same default should still work
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	_, err := executeUseCmd("mcpress")
	require.NoError(t, err)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "Default instance set to: mcpress ✓")
}

func TestUseCmd_ErrorMessageListsAvailable(t *testing.T) {
	setupTestConfig(t, testConfig())

	_, err := executeUseCmd("nonexistent")
	require.Error(t, err)
	errMsg := err.Error()

	// Should list available instances (order may vary)
	assert.True(t, strings.Contains(errMsg, "mcpress") && strings.Contains(errMsg, "production"),
		"error should list available instances, got: %s", errMsg)
}

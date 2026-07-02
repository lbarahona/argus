package main

import (
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

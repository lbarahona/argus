package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/lbarahona/argus/internal/incident"
	"github.com/lbarahona/argus/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedIncident writes a single incident to ~/.argus/incidents.yaml (under
// the HOME set up by setupTestConfig) so postmortem generate has something
// to load.
func seedIncident(t *testing.T) string {
	t.Helper()
	store, err := incident.Load()
	require.NoError(t, err)
	inc := store.Create("Test incident for postmortem", "minor", nil, "", "")
	require.NoError(t, store.Save())
	return inc.ID
}

// executePostmortemGenerateCmd runs postmortem generate with args, capturing
// both cobra's own output buffer and anything written directly to
// os.Stdout (RenderTerminal/warnings write straight to os.Stdout).
func executePostmortemGenerateCmd(args ...string) (string, error) {
	buf := new(bytes.Buffer)
	cmd := postmortemGenerateCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Execute()

	w.Close()
	os.Stdout = old
	var out bytes.Buffer
	out.ReadFrom(r)

	return buf.String() + out.String(), err
}

func TestPostmortemGenerate_ZeroConfigNoInstanceFlag_WarnsAndContinues(t *testing.T) {
	// Zero instances configured, and no explicit -i: enrichment should be
	// skipped with a warning, not a hard error.
	setupTestConfig(t, &types.Config{Instances: map[string]types.Instance{}})
	incidentID := seedIncident(t)

	out, err := executePostmortemGenerateCmd(incidentID)
	require.NoError(t, err)
	assert.Contains(t, out, "No Signoz instances configured")
	assert.Contains(t, out, "continuing without Signoz enrichment")
	assert.Contains(t, out, "Postmortem")
}

func TestPostmortemGenerate_ExplicitInstanceFlag_HardErrorsWhenUnresolvable(t *testing.T) {
	// Zero instances configured, but the user explicitly passed -i: a
	// resolution failure must be a hard error, not silently skipped.
	setupTestConfig(t, &types.Config{Instances: map[string]types.Instance{}})
	incidentID := seedIncident(t)

	out, err := executePostmortemGenerateCmd(incidentID, "-i", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `instance "nonexistent" not found`)
	assert.NotContains(t, out, "continuing without Signoz enrichment")
}

func TestPostmortemGenerate_InvalidFormat_ErrorsBeforeIncidentLookup(t *testing.T) {
	setupTestConfig(t, &types.Config{Instances: map[string]types.Instance{}})

	// No incident is seeded — if validateFormat runs first (as required),
	// the format error surfaces before any "postmortem not found"-style
	// incident lookup error.
	_, err := executePostmortemGenerateCmd("nonexistent-incident", "-f", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown format "bogus"`)
}

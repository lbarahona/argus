package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
)

// replaceStdin replaces os.Stdin with a reader containing the provided lines
// (newline-terminated) and restores the original stdin after the test.
func replaceStdin(t *testing.T, lines []string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig })

	go func() {
		defer w.Close()
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}()
	_ = io.Discard // suppress unused-import lint noise
}

// withTempHome sets HOME to a fresh temp dir for the duration of the test.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	return tmpDir
}

func TestSaveAndLoad(t *testing.T) {
	// Use a temp dir
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg := &types.Config{
		AnthropicKey:    "sk-test-123",
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod": {
				URL:        "https://signoz.example.com",
				APIKey:     "signoz-key-123",
				Name:       "Production",
				APIVersion: "v5",
			},
		},
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists
	cfgPath := filepath.Join(tmpDir, configDir, configFile)
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Fatal("config file not created")
	}

	// Load it back
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.AnthropicKey != cfg.AnthropicKey {
		t.Errorf("AnthropicKey: got %q, want %q", loaded.AnthropicKey, cfg.AnthropicKey)
	}
	if loaded.DefaultInstance != cfg.DefaultInstance {
		t.Errorf("DefaultInstance: got %q, want %q", loaded.DefaultInstance, cfg.DefaultInstance)
	}
	inst, ok := loaded.Instances["prod"]
	if !ok {
		t.Fatal("instance 'prod' not found")
	}
	if inst.URL != "https://signoz.example.com" {
		t.Errorf("URL: got %q", inst.URL)
	}
	if inst.APIVersion != "v5" {
		t.Errorf("APIVersion: got %q, want v5", inst.APIVersion)
	}
}

func TestLoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load should not error on missing file: %v", err)
	}
	if cfg.Instances == nil {
		t.Error("Instances should be initialized")
	}
}

func TestGetInstance(t *testing.T) {
	cfg := &types.Config{
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod":    {URL: "https://prod.example.com", Name: "Prod"},
			"staging": {URL: "https://staging.example.com", Name: "Staging"},
		},
	}

	// Get default
	inst, key, err := GetInstance(cfg, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "prod" {
		t.Errorf("expected key=prod, got %s", key)
	}
	if inst.URL != "https://prod.example.com" {
		t.Errorf("unexpected URL: %s", inst.URL)
	}

	// Get by name
	_, key, err = GetInstance(cfg, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "staging" {
		t.Errorf("expected key=staging, got %s", key)
	}

	// Get missing
	_, _, err = GetInstance(cfg, "missing")
	if err == nil {
		t.Error("expected error for missing instance")
	}
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if Exists() {
		t.Error("should not exist yet")
	}

	Save(&types.Config{Instances: map[string]types.Instance{}})

	if !Exists() {
		t.Error("should exist after save")
	}
}

// TestPathAndDir verifies that Path and Dir return paths rooted under HOME/.argus.
func TestPathAndDir(t *testing.T) {
	tmpDir := withTempHome(t)

	dir := Dir()
	if !strings.Contains(dir, ".argus") {
		t.Errorf("Dir() should contain '.argus', got %q", dir)
	}
	if !strings.HasPrefix(dir, tmpDir) {
		t.Errorf("Dir() should be under tmpDir %q, got %q", tmpDir, dir)
	}

	p := Path()
	if !strings.Contains(p, ".argus") {
		t.Errorf("Path() should contain '.argus', got %q", p)
	}
	if !strings.HasSuffix(p, "config.yaml") {
		t.Errorf("Path() should end with 'config.yaml', got %q", p)
	}
	if !strings.HasPrefix(p, tmpDir) {
		t.Errorf("Path() should be under tmpDir %q, got %q", tmpDir, p)
	}
}

// TestLoadInvalidYAML verifies that Load returns an error for corrupt YAML.
func TestLoadInvalidYAML(t *testing.T) {
	withTempHome(t)

	// Create config directory and write invalid YAML.
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(Path(), []byte(":\tinvalid: [yaml\x00content"), 0600); err != nil {
		t.Fatalf("failed to write bad config: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("Load should return an error for invalid YAML")
	}
}

// TestGetInstanceNoDefault verifies that GetInstance errors when there is no
// name argument, no DefaultInstance set, and zero configured instances — the
// error should point the user at 'argus config init' since there's nothing
// to select from.
func TestGetInstanceNoDefault(t *testing.T) {
	cfg := &types.Config{
		DefaultInstance: "",
		Instances:       map[string]types.Instance{},
	}

	_, _, err := GetInstance(cfg, "")
	if err == nil {
		t.Fatal("expected error when no instance specified and no default set")
	}
	if !strings.Contains(err.Error(), "no instance specified") {
		t.Errorf("error message should contain 'no instance specified', got: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "argus config init") {
		t.Errorf("error message should point to 'argus config init' when config has zero instances, got: %q", err.Error())
	}
}

// TestGetInstanceNoDefaultPopulated verifies that GetInstance keeps the
// plain error (no 'config init' pointer) when the config already has
// instances configured but none is marked as the default — the fix here is
// picking a default with '-i' or 'argus use', not running init again.
func TestGetInstanceNoDefaultPopulated(t *testing.T) {
	cfg := &types.Config{
		DefaultInstance: "",
		Instances: map[string]types.Instance{
			"alpha": {URL: "https://alpha.example.com", Name: "Alpha"},
		},
	}

	_, _, err := GetInstance(cfg, "")
	if err == nil {
		t.Fatal("expected error when no instance specified and no default set")
	}
	if !strings.Contains(err.Error(), "no instance specified") {
		t.Errorf("error message should contain 'no instance specified', got: %q", err.Error())
	}
	if strings.Contains(err.Error(), "config init") {
		t.Errorf("error message should NOT suggest 'config init' when instances already exist, got: %q", err.Error())
	}
}

// TestGetInstanceWithExplicitName verifies retrieval by explicit key when no
// DefaultInstance is configured.
func TestGetInstanceWithExplicitName(t *testing.T) {
	cfg := &types.Config{
		DefaultInstance: "",
		Instances: map[string]types.Instance{
			"alpha": {URL: "https://alpha.example.com", Name: "Alpha"},
			"beta":  {URL: "https://beta.example.com", Name: "Beta"},
		},
	}

	inst, key, err := GetInstance(cfg, "beta")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "beta" {
		t.Errorf("expected key=beta, got %q", key)
	}
	if inst.URL != "https://beta.example.com" {
		t.Errorf("expected beta URL, got %q", inst.URL)
	}
}

// TestLoadWithNilInstances verifies that Load initialises Instances to a
// non-nil map even when the YAML file omits the instances field.
func TestLoadWithNilInstances(t *testing.T) {
	withTempHome(t)

	// Write a minimal YAML file without an instances key.
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.WriteFile(Path(), []byte("anthropic_key: sk-abc\n"), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Instances == nil {
		t.Error("Instances should not be nil after Load")
	}
}

// TestSaveFilePermissions verifies that the config file is written with mode 0600.
func TestSaveFilePermissions(t *testing.T) {
	withTempHome(t)

	cfg := &types.Config{Instances: map[string]types.Instance{}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	info, err := os.Stat(Path())
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0600 {
		t.Errorf("config file mode: got %04o, want 0600", mode)
	}
}

// TestSaveDirPermissions verifies that the config directory is created with mode 0700.
func TestSaveDirPermissions(t *testing.T) {
	withTempHome(t)

	cfg := &types.Config{Instances: map[string]types.Instance{}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	info, err := os.Stat(Dir())
	if err != nil {
		t.Fatalf("Stat on dir failed: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0700 {
		t.Errorf("config dir mode: got %04o, want 0700", mode)
	}
}

// TestLoadPreservesAllFields verifies that every field in Config and Instance
// survives a Save/Load round-trip.
func TestLoadPreservesAllFields(t *testing.T) {
	withTempHome(t)

	cfg := &types.Config{
		AnthropicKey:    "sk-full-test",
		DefaultInstance: "primary",
		Instances: map[string]types.Instance{
			"primary": {
				URL:        "https://primary.example.com",
				APIKey:     "key-primary",
				Name:       "Primary",
				APIVersion: "v3",
			},
			"secondary": {
				URL:        "https://secondary.example.com",
				APIKey:     "key-secondary",
				Name:       "Secondary",
				APIVersion: "v5",
			},
		},
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.AnthropicKey != cfg.AnthropicKey {
		t.Errorf("AnthropicKey: got %q, want %q", loaded.AnthropicKey, cfg.AnthropicKey)
	}
	if loaded.DefaultInstance != cfg.DefaultInstance {
		t.Errorf("DefaultInstance: got %q, want %q", loaded.DefaultInstance, cfg.DefaultInstance)
	}

	for key, want := range cfg.Instances {
		got, ok := loaded.Instances[key]
		if !ok {
			t.Errorf("instance %q missing after Load", key)
			continue
		}
		if got.URL != want.URL {
			t.Errorf("[%s] URL: got %q, want %q", key, got.URL, want.URL)
		}
		if got.APIKey != want.APIKey {
			t.Errorf("[%s] APIKey: got %q, want %q", key, got.APIKey, want.APIKey)
		}
		if got.Name != want.Name {
			t.Errorf("[%s] Name: got %q, want %q", key, got.Name, want.Name)
		}
		if got.APIVersion != want.APIVersion {
			t.Errorf("[%s] APIVersion: got %q, want %q", key, got.APIVersion, want.APIVersion)
		}
	}
}

// TestGetInstancePartialMatch verifies that GetInstance returns exactly the
// requested instance and not a similarly-named one.
func TestGetInstancePartialMatch(t *testing.T) {
	cfg := &types.Config{
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod":    {URL: "https://prod.example.com"},
			"prod-eu": {URL: "https://prod-eu.example.com"},
		},
	}

	inst, key, err := GetInstance(cfg, "prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "prod-eu" {
		t.Errorf("expected key=prod-eu, got %q", key)
	}
	if inst.URL != "https://prod-eu.example.com" {
		t.Errorf("expected prod-eu URL, got %q", inst.URL)
	}
}

// TestExistsAfterSaveAndDelete verifies the full lifecycle: save → exists →
// delete → not exists.
func TestExistsAfterSaveAndDelete(t *testing.T) {
	withTempHome(t)

	cfg := &types.Config{Instances: map[string]types.Instance{}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if !Exists() {
		t.Fatal("Exists() should return true after Save")
	}

	if err := os.Remove(Path()); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	if Exists() {
		t.Error("Exists() should return false after file is deleted")
	}
}

// TestRunInit exercises the interactive RunInit function by piping pre-canned
// answers into os.Stdin.
func TestRunInit(t *testing.T) {
	withTempHome(t)
	replaceStdin(t, []string{
		"",                          // provider choice (default = Anthropic)
		"sk-anthropic-key",          // Anthropic API key
		"myprod",                    // instance name
		"My Production",             // display name
		"https://signoz.myprod.com", // Signoz URL
		"signoz-api-key-prod",       // Signoz API key
	})

	cfg, err := RunInit()
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}
	if cfg.AnthropicKey != "sk-anthropic-key" {
		t.Errorf("AnthropicKey: got %q, want %q", cfg.AnthropicKey, "sk-anthropic-key")
	}
	if cfg.AI.Provider != "anthropic" {
		t.Errorf("AI.Provider: got %q, want %q", cfg.AI.Provider, "anthropic")
	}
	if cfg.DefaultInstance != "myprod" {
		t.Errorf("DefaultInstance: got %q, want %q", cfg.DefaultInstance, "myprod")
	}
	inst, ok := cfg.Instances["myprod"]
	if !ok {
		t.Fatal("instance 'myprod' not found in config after RunInit")
	}
	if inst.URL != "https://signoz.myprod.com" {
		t.Errorf("instance URL: got %q, want %q", inst.URL, "https://signoz.myprod.com")
	}
	if inst.APIKey != "signoz-api-key-prod" {
		t.Errorf("instance APIKey: got %q, want %q", inst.APIKey, "signoz-api-key-prod")
	}
	if inst.Name != "My Production" {
		t.Errorf("instance Name: got %q, want %q", inst.Name, "My Production")
	}

	// Config should have been persisted to disk.
	if !Exists() {
		t.Error("config file should exist on disk after RunInit")
	}
}

// TestRunInitPersistsFile verifies that RunInit saves the config so Load can
// read it back independently.
func TestRunInitPersistsFile(t *testing.T) {
	withTempHome(t)
	replaceStdin(t, []string{
		"", // provider choice (default)
		"sk-persist-check",
		"persistinst",
		"Persist Instance",
		"https://persist.example.com",
		"persist-api-key",
	})

	if _, err := RunInit(); err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after RunInit failed: %v", err)
	}
	if loaded.AnthropicKey != "sk-persist-check" {
		t.Errorf("loaded AnthropicKey: got %q, want %q", loaded.AnthropicKey, "sk-persist-check")
	}
	if _, ok := loaded.Instances["persistinst"]; !ok {
		t.Error("instance 'persistinst' not found after loading RunInit result")
	}
}

// TestAddInstance exercises AddInstance by piping pre-canned answers.
func TestAddInstance(t *testing.T) {
	withTempHome(t)

	// Start with an existing config.
	existing := &types.Config{
		AnthropicKey:    "sk-existing",
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod": {URL: "https://prod.example.com", APIKey: "key-prod", Name: "Prod"},
		},
	}
	if err := Save(existing); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	replaceStdin(t, []string{
		"staging",
		"Staging Env",
		"https://staging.example.com",
		"key-staging",
	})

	if err := AddInstance(existing); err != nil {
		t.Fatalf("AddInstance failed: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after AddInstance failed: %v", err)
	}
	inst, ok := loaded.Instances["staging"]
	if !ok {
		t.Fatal("instance 'staging' not found after AddInstance")
	}
	if inst.URL != "https://staging.example.com" {
		t.Errorf("staging URL: got %q, want %q", inst.URL, "https://staging.example.com")
	}
	if inst.APIKey != "key-staging" {
		t.Errorf("staging APIKey: got %q, want %q", inst.APIKey, "key-staging")
	}
	if inst.Name != "Staging Env" {
		t.Errorf("staging Name: got %q, want %q", inst.Name, "Staging Env")
	}
	// Original instance should still be present.
	if _, ok := loaded.Instances["prod"]; !ok {
		t.Error("original 'prod' instance should still exist after AddInstance")
	}
}

// TestAddInstanceDuplicateKey verifies that AddInstance returns an error when
// the key already exists, without prompting further.
func TestAddInstanceDuplicateKey(t *testing.T) {
	withTempHome(t)

	existing := &types.Config{
		DefaultInstance: "prod",
		Instances: map[string]types.Instance{
			"prod": {URL: "https://prod.example.com"},
		},
	}
	if err := Save(existing); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Pipe only the key — AddInstance should error before reading further lines.
	replaceStdin(t, []string{"prod"})

	err := AddInstance(existing)
	if err == nil {
		t.Fatal("expected error when adding a duplicate instance key")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %q", err.Error())
	}
}

// TestSaveLeavesNoTempFiles verifies that Save uses atomic writes (temp file
// + rename) and leaves no temp files behind, even though the final config
// must roundtrip correctly and be readable with proper 0600 permissions.
func TestSaveLeavesNoTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &types.Config{DefaultInstance: "prod", Instances: map[string]types.Instance{
		"prod": {URL: "https://signoz.example.com"},
	}}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if loaded.DefaultInstance != "prod" {
		t.Errorf("roundtrip lost data: %+v", loaded)
	}

	info, _ := os.Stat(Path())
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config perm = %v, want 0600 (contains API keys)", info.Mode().Perm())
	}
}

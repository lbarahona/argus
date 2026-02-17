package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbarahona/argus/pkg/types"
)

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
	inst, key, err = GetInstance(cfg, "staging")
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

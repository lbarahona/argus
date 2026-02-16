package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lbarahona/argus/pkg/types"
	"gopkg.in/yaml.v3"
)

const (
	configDir  = ".argus"
	configFile = "config.yaml"
)

// Path returns the full path to the config file.
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, configDir, configFile)
}

// Dir returns the config directory.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, configDir)
}

// Load reads the config from disk.
func Load() (*types.Config, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return &types.Config{Instances: make(map[string]Instance)}, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg types.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if cfg.Instances == nil {
		cfg.Instances = make(map[string]types.Instance)
	}
	return &cfg, nil
}

// Instance is an alias for convenience.
type Instance = types.Instance

// Save writes the config to disk.
func Save(cfg *types.Config) error {
	if err := os.MkdirAll(Dir(), 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(Path(), data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Exists returns true if the config file exists.
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// RunInit performs interactive config initialization.
func RunInit() (*types.Config, error) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("🔧 Argus Configuration Setup")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Anthropic key
	fmt.Print("Anthropic API key: ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	// First instance
	fmt.Println()
	fmt.Println("📡 Configure your first Signoz instance:")
	fmt.Println()

	fmt.Print("Instance name (e.g. production): ")
	instName, _ := reader.ReadString('\n')
	instName = strings.TrimSpace(instName)

	fmt.Print("Display name (e.g. Production): ")
	displayName, _ := reader.ReadString('\n')
	displayName = strings.TrimSpace(displayName)

	fmt.Print("Signoz URL (e.g. https://signoz.example.com): ")
	url, _ := reader.ReadString('\n')
	url = strings.TrimSpace(url)

	fmt.Print("Signoz API key: ")
	signozKey, _ := reader.ReadString('\n')
	signozKey = strings.TrimSpace(signozKey)

	cfg := &types.Config{
		AnthropicKey:    apiKey,
		DefaultInstance: instName,
		Instances: map[string]types.Instance{
			instName: {
				URL:    url,
				APIKey: signozKey,
				Name:   displayName,
			},
		},
	}

	if err := Save(cfg); err != nil {
		return nil, err
	}

	fmt.Println()
	fmt.Printf("✅ Config saved to %s\n", Path())
	return cfg, nil
}

// AddInstance interactively adds a new instance.
func AddInstance(cfg *types.Config) error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("📡 Add Signoz Instance")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	fmt.Print("Instance key (e.g. staging): ")
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)

	if _, exists := cfg.Instances[key]; exists {
		return fmt.Errorf("instance %q already exists", key)
	}

	fmt.Print("Display name: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)

	fmt.Print("Signoz URL: ")
	url, _ := reader.ReadString('\n')
	url = strings.TrimSpace(url)

	fmt.Print("Signoz API key: ")
	apiKey, _ := reader.ReadString('\n')
	apiKey = strings.TrimSpace(apiKey)

	cfg.Instances[key] = types.Instance{
		URL:    url,
		APIKey: apiKey,
		Name:   name,
	}

	if err := Save(cfg); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("✅ Instance %q added\n", key)
	return nil
}

// GetInstance returns the named instance, or the default if name is empty.
func GetInstance(cfg *types.Config, name string) (*types.Instance, string, error) {
	if name == "" {
		name = cfg.DefaultInstance
	}
	if name == "" {
		return nil, "", fmt.Errorf("no instance specified and no default set")
	}
	inst, ok := cfg.Instances[name]
	if !ok {
		return nil, "", fmt.Errorf("instance %q not found", name)
	}
	return &inst, name, nil
}

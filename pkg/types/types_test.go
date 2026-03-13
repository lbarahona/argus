package types

import "testing"

func TestGetAPIVersionDefault(t *testing.T) {
	inst := Instance{URL: "http://localhost"}
	if v := inst.GetAPIVersion(); v != "v3" {
		t.Errorf("expected v3, got %s", v)
	}
}

func TestGetAPIVersionExplicit(t *testing.T) {
	inst := Instance{URL: "http://localhost", APIVersion: "v5"}
	if v := inst.GetAPIVersion(); v != "v5" {
		t.Errorf("expected v5, got %s", v)
	}
}

func TestTraceEntryDurationMs(t *testing.T) {
	entry := TraceEntry{DurationNano: 15000000}
	if ms := entry.DurationMs(); ms != 15.0 {
		t.Errorf("expected 15.0ms, got %f", ms)
	}
}

func TestTraceEntryDurationMsSubMs(t *testing.T) {
	entry := TraceEntry{DurationNano: 500000}
	if ms := entry.DurationMs(); ms != 0.5 {
		t.Errorf("expected 0.5ms, got %f", ms)
	}
}

func TestGetAIConfig_LegacyFallback(t *testing.T) {
	cfg := Config{
		AnthropicKey: "sk-ant-legacy",
	}
	ai := cfg.GetAIConfig()
	if ai.Provider != "anthropic" {
		t.Errorf("expected provider=anthropic, got %q", ai.Provider)
	}
	if ai.AnthropicKey != "sk-ant-legacy" {
		t.Errorf("expected key=sk-ant-legacy, got %q", ai.AnthropicKey)
	}
}

func TestGetAIConfig_NewConfigTakesPrecedence(t *testing.T) {
	cfg := Config{
		AnthropicKey: "sk-ant-legacy",
		AI: AIConfig{
			Provider:     "openai",
			OpenAIKey:    "sk-openai",
			AnthropicKey: "sk-ant-new",
		},
	}
	ai := cfg.GetAIConfig()
	if ai.Provider != "openai" {
		t.Errorf("expected provider=openai, got %q", ai.Provider)
	}
	if ai.OpenAIKey != "sk-openai" {
		t.Errorf("expected openai key, got %q", ai.OpenAIKey)
	}
	// New AI config anthropic key takes precedence over legacy
	if ai.AnthropicKey != "sk-ant-new" {
		t.Errorf("expected new key, got %q", ai.AnthropicKey)
	}
}

func TestGetAIConfig_DefaultProvider(t *testing.T) {
	cfg := Config{}
	ai := cfg.GetAIConfig()
	if ai.Provider != "anthropic" {
		t.Errorf("expected default provider=anthropic, got %q", ai.Provider)
	}
}

func TestGetAIConfig_BedrockConfig(t *testing.T) {
	cfg := Config{
		AI: AIConfig{
			Provider: "bedrock",
			Bedrock: BedrockConfig{
				Endpoint: "https://bedrock.us-east-1.amazonaws.com",
				Token:    "my-token",
				Model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
			},
		},
	}
	ai := cfg.GetAIConfig()
	if ai.Provider != "bedrock" {
		t.Errorf("expected provider=bedrock, got %q", ai.Provider)
	}
	if ai.Bedrock.Endpoint != "https://bedrock.us-east-1.amazonaws.com" {
		t.Errorf("unexpected endpoint: %q", ai.Bedrock.Endpoint)
	}
}

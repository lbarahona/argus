package ai

import (
	"testing"
)

func TestNewProvider_Anthropic(t *testing.T) {
	p, err := NewProvider(AIConfig{
		Provider:     "anthropic",
		AnthropicKey: "sk-ant-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name=anthropic, got %q", p.Name())
	}
	if p.Model() != defaultAnthropicModel {
		t.Errorf("expected default model, got %q", p.Model())
	}
}

func TestNewProvider_AnthropicDefault(t *testing.T) {
	// Empty provider should default to anthropic
	p, err := NewProvider(AIConfig{
		Provider:     "",
		AnthropicKey: "sk-ant-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name=anthropic, got %q", p.Name())
	}
}

func TestNewProvider_AnthropicMissingKey(t *testing.T) {
	// Clear env var for test
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := NewProvider(AIConfig{Provider: "anthropic"})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	p, err := NewProvider(AIConfig{
		Provider:  "openai",
		OpenAIKey: "sk-openai-test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected name=openai, got %q", p.Name())
	}
	if p.Model() != defaultOpenAIModel {
		t.Errorf("expected default model, got %q", p.Model())
	}
}

func TestNewProvider_OpenAIMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewProvider(AIConfig{Provider: "openai"})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestNewProvider_Bedrock(t *testing.T) {
	p, err := NewProvider(AIConfig{
		Provider: "bedrock",
		Bedrock: BedrockConfig{
			Endpoint: "https://bedrock.us-east-1.amazonaws.com",
			Token:    "my-token",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "bedrock" {
		t.Errorf("expected name=bedrock, got %q", p.Name())
	}
}

func TestNewProvider_BedrockMissingEndpoint(t *testing.T) {
	_, err := NewProvider(AIConfig{
		Provider: "bedrock",
		Bedrock:  BedrockConfig{Token: "my-token"},
	})
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestNewProvider_BedrockMissingToken(t *testing.T) {
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "")
	_, err := NewProvider(AIConfig{
		Provider: "bedrock",
		Bedrock:  BedrockConfig{Endpoint: "https://bedrock.example.com"},
	})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestNewProvider_UnknownProvider(t *testing.T) {
	_, err := NewProvider(AIConfig{Provider: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewProvider_CustomModel(t *testing.T) {
	p, err := NewProvider(AIConfig{
		Provider:     "anthropic",
		AnthropicKey: "sk-ant-test",
		Model:        "claude-3-haiku-20240307",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Model() != "claude-3-haiku-20240307" {
		t.Errorf("expected custom model, got %q", p.Model())
	}
}

func TestNewProvider_AutoModel(t *testing.T) {
	p, err := NewProvider(AIConfig{
		Provider:     "anthropic",
		AnthropicKey: "sk-ant-test",
		Model:        "auto",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Model() != defaultAnthropicModel {
		t.Errorf("expected default model for auto, got %q", p.Model())
	}
}

func TestResolveModel(t *testing.T) {
	tests := []struct {
		provider, model, want string
	}{
		{"anthropic", "", defaultAnthropicModel},
		{"anthropic", "auto", defaultAnthropicModel},
		{"anthropic", "claude-3-haiku-20240307", "claude-3-haiku-20240307"},
		{"openai", "", defaultOpenAIModel},
		{"openai", "gpt-4o-mini", "gpt-4o-mini"},
		{"bedrock", "", defaultBedrockModel},
		{"unknown", "", ""},
		{"unknown", "my-model", "my-model"},
	}

	for _, tt := range tests {
		got := ResolveModel(tt.provider, tt.model)
		if got != tt.want {
			t.Errorf("ResolveModel(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestNewProvider_EnvVarFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env")
	p, err := NewProvider(AIConfig{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected anthropic from env fallback")
	}
}

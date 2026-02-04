package ai_assistant

import (
	"go.opentelemetry.io/collector/component"
)

// Config holds the configuration for the AI Assistant extension.
type Config struct {
	// LLMProvider specifies the type of backend (e.g., "ollama").
	LLMProvider string `mapstructure:"llm_provider"`

	// ModelName is the specific model to use (e.g., "qwen2.5:0.5b").
	ModelName string `mapstructure:"model_name"`

	// APIEndpoint is the URL of the inference server.
	APIEndpoint string `mapstructure:"api_endpoint"`

	// Prompts allows overriding the default system prompts.
	Prompts PromptConfig `mapstructure:"prompts"`

	// Server defines the HTTP listener settings for the UI.
	Server ServerConfig `mapstructure:"server"`
}

type PromptConfig struct {
	SearchSystemPrompt  string `mapstructure:"search_system_prompt"`
	ExplainSystemPrompt string `mapstructure:"explain_system_prompt"`
}

type ServerConfig struct {
	Endpoint string `mapstructure:"endpoint"` // e.g. "localhost:10000"
}

var _ component.Config = (*Config)(nil)

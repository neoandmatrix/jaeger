package ai_assistant

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
)

const (
	// The value of extension "type" in configuration.
	TypeStr = "ai_assistant"
)

func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType(TypeStr),
		createDefaultConfig,
		createExtension,
		component.StabilityLevelAlpha,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		LLMProvider: "openai",
		ModelName:   "qwen2.5-0.5b",
		APIEndpoint: "http://localhost:6969/v1",
		Server: ServerConfig{
			Endpoint: "0.0.0.0:10000",
		},
	}
}

func createExtension(_ context.Context, params extension.Settings, cfg component.Config) (extension.Extension, error) {
	c := cfg.(*Config)
	return newAIExtension(c, params.Logger), nil
}

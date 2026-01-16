// Package provider implements LLM provider interfaces and implementations
package provider

import (
	"context"
	"fmt"

	"github.com/cloud-shuttle/drover/internal/llmproxy"
)

// Factory creates a provider from its configuration
type Factory func(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error)

// Registry holds all registered provider factories
var Registry = map[llmproxy.ProviderType]Factory{
	llmproxy.ProviderAnthropic: NewAnthropicProvider,
	llmproxy.ProviderOpenAI:    NewOpenAIProvider,
	llmproxy.ProviderGLM:       NewGLMProvider,
	llmproxy.ProviderGroq:      NewGroqProvider,
	llmproxy.ProviderGrok:      NewGrokProvider,
}

// CreateProvider creates a provider from its configuration
func CreateProvider(cfg llmproxy.ProviderConfig) (llmproxy.Provider, error) {
	factory, ok := Registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
	}
	return factory(cfg)
}

// CreateAllProviders creates all configured providers
func CreateAllProviders(configs map[llmproxy.ProviderType]llmproxy.ProviderConfig) (map[llmproxy.ProviderType]llmproxy.Provider, error) {
	providers := make(map[llmproxy.ProviderType]llmproxy.Provider)

	for typ, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		p, err := CreateProvider(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s provider: %w", typ, err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("invalid %s provider config: %w", typ, err)
		}
		providers[typ] = p
	}

	return providers, nil
}

// BaseProvider provides common functionality for all providers
type BaseProvider struct {
	Config    llmproxy.ProviderConfig
	ModelInfo map[string]llmproxy.Model
}

// GetModels returns the list of available models
func (b *BaseProvider) GetModels() []llmproxy.Model {
	models := make([]llmproxy.Model, 0, len(b.ModelInfo))
	for _, m := range b.ModelInfo {
		models = append(models, m)
	}
	return models
}

// CalculateCost calculates the cost based on token usage
func (b *BaseProvider) CalculateCost(model string, usage *llmproxy.Usage) (*llmproxy.Cost, error) {
	m, ok := b.ModelInfo[model]
	if !ok {
		return nil, fmt.Errorf("unknown model: %s", model)
	}

	inputCost := (float64(usage.PromptTokens) / 1_000_000) * m.InputPrice
	outputCost := (float64(usage.CompletionTokens) / 1_000_000) * m.OutputPrice

	return &llmproxy.Cost{
		InputCost:    inputCost,
		OutputCost:   outputCost,
		TotalCost:    inputCost + outputCost,
		Currency:     "USD",
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
		Model:        model,
		Provider:     b.Config.Type,
	}, nil
}

// Validate checks if the provider configuration is valid
func (b *BaseProvider) Validate() error {
	if b.Config.APIKey == "" {
		return fmt.Errorf("API key is required for %s provider", b.Config.Type)
	}
	return nil
}

// ApplyModelAlias applies model alias configuration
func (b *BaseProvider) ApplyModelAlias(model string) string {
	if alias, ok := b.Config.ModelAlias[model]; ok {
		return alias
	}
	return model
}

// Common helper functions

// StreamError sends an error through a chunk channel
func StreamError(ch chan<- llmproxy.ChatChunk, err error) {
	select {
	case ch <- llmproxy.ChatChunk{
		Choices: []llmproxy.Choice{
			{
				Index: 0,
				Delta: &llmproxy.MessageDelta{
					Content: fmt.Sprintf("[Error: %v]", err),
				},
			},
		},
	}:
	default:
	}
}

// StreamDone sends a completion signal through a chunk channel
func StreamDone(ch chan<- llmproxy.ChatChunk) {
	select {
	case ch <- llmproxy.ChatChunk{
		Choices: []llmproxy.Choice{
			{
				Index:        0,
				FinishReason: "stop",
				Delta: &llmproxy.MessageDelta{
					Content: "",
				},
			},
		},
	}:
	default:
	}
}

// MessagesToInternal converts between message formats if needed
func MessagesToInternal(msgs []llmproxy.Message) []llmproxy.Message {
	return msgs
}

// ContextToInternal converts context to provider-specific format
func ContextToInternal(ctx context.Context) context.Context {
	return ctx
}

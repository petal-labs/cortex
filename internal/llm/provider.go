// Package llm provides LLM provider integration using the iris SDK.
package llm

import (
	"fmt"
	"os"

	"github.com/petal-labs/iris/core"
	"github.com/petal-labs/iris/providers/anthropic"
	"github.com/petal-labs/iris/providers/gemini"
	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/iris/providers/openai"
	"github.com/petal-labs/iris/providers/voyageai"
)

// EmbeddingProvider is an alias for core.EmbeddingProvider.
type EmbeddingProvider = core.EmbeddingProvider

// ProviderType identifies the LLM provider.
type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderGemini    ProviderType = "gemini"
	ProviderOllama    ProviderType = "ollama"
	ProviderVoyageAI  ProviderType = "voyageai"
)

// NewProvider creates an iris provider based on the provider type.
// API keys are read from standard environment variables:
// - openai: OPENAI_API_KEY
// - anthropic: ANTHROPIC_API_KEY
// - gemini: GEMINI_API_KEY or GOOGLE_API_KEY
// - voyageai: VOYAGEAI_API_KEY
// - ollama: no API key required (local)
func NewProvider(providerType string) (core.Provider, error) {
	switch ProviderType(providerType) {
	case ProviderOpenAI:
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required for openai provider")
		}
		return openai.New(apiKey), nil

	case ProviderAnthropic:
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required for anthropic provider")
		}
		return anthropic.New(apiKey), nil

	case ProviderGemini:
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("GOOGLE_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("GEMINI_API_KEY or GOOGLE_API_KEY environment variable is required for gemini provider")
		}
		return gemini.New(apiKey), nil

	case ProviderOllama:
		baseURL := os.Getenv("OLLAMA_BASE_URL")
		if baseURL != "" {
			return ollama.New(ollama.WithBaseURL(baseURL)), nil
		}
		return ollama.New(), nil

	case ProviderVoyageAI:
		apiKey := os.Getenv("VOYAGEAI_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("VOYAGEAI_API_KEY environment variable is required for voyageai provider")
		}
		return voyageai.New(apiKey), nil

	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerType)
	}
}

// NewEmbeddingProvider creates an iris embedding provider based on the provider type.
// Returns the provider cast to core.EmbeddingProvider, or an error if the provider
// doesn't support embeddings.
func NewEmbeddingProvider(providerType string) (core.EmbeddingProvider, error) {
	provider, err := NewProvider(providerType)
	if err != nil {
		return nil, err
	}

	embProvider, ok := provider.(core.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support embeddings", providerType)
	}

	return embProvider, nil
}

// NewClient creates an iris client from a provider.
func NewClient(provider core.Provider, opts ...core.ClientOption) *core.Client {
	return core.NewClient(provider, opts...)
}

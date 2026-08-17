package llm

import "sort"

// Capabilities describes what a provider can do. Not every provider offers
// every feature: Anthropic and Gemini expose no embedding API through iris,
// and VoyageAI is embeddings-only — its Chat returns core.ErrNotSupported.
type Capabilities struct {
	Chat       bool
	Embeddings bool
}

// providerCapabilities mirrors what each provider's iris implementation
// reports from Supports. It is a static table rather than a live query
// because callers need the answer before an API key is available —
// NewProvider requires one, but config validation runs at startup and must
// be able to reject an unusable pairing without credentials.
//
// capabilities_test.go asserts this table against iris, so it cannot
// silently drift when the SDK is upgraded.
var providerCapabilities = map[ProviderType]Capabilities{
	ProviderOpenAI:    {Chat: true, Embeddings: true},
	ProviderAnthropic: {Chat: true, Embeddings: false},
	ProviderGemini:    {Chat: true, Embeddings: false},
	ProviderOllama:    {Chat: true, Embeddings: true},
	ProviderVoyageAI:  {Chat: false, Embeddings: true},
}

// ProviderCapabilities returns what the named provider supports. The second
// return is false for a name that is not a known provider at all.
func ProviderCapabilities(name string) (Capabilities, bool) {
	caps, ok := providerCapabilities[ProviderType(name)]
	return caps, ok
}

// SupportedProviders returns every known provider name, sorted.
func SupportedProviders() []string {
	return providersWhere(func(Capabilities) bool { return true })
}

// ChatProviders returns the providers usable for summarization and entity
// extraction, sorted.
func ChatProviders() []string {
	return providersWhere(func(c Capabilities) bool { return c.Chat })
}

// EmbeddingProviders returns the providers usable for embeddings, sorted.
func EmbeddingProviders() []string {
	return providersWhere(func(c Capabilities) bool { return c.Embeddings })
}

// providersWhere collects provider names matching pred. Results are sorted
// so error messages listing alternatives stay stable across runs — Go map
// iteration order is randomized.
func providersWhere(pred func(Capabilities) bool) []string {
	names := make([]string, 0, len(providerCapabilities))
	for name, caps := range providerCapabilities {
		if pred(caps) {
			names = append(names, string(name))
		}
	}
	sort.Strings(names)
	return names
}

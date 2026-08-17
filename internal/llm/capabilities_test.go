package llm

import (
	"testing"

	"github.com/petal-labs/iris/core"
)

// TestProviderCapabilitiesMatchIris guards the static capability table
// against drift in the iris SDK. The table exists so config validation can
// answer "can this provider embed?" without an API key — constructing a
// provider requires one — but that means nothing keeps it honest except
// this test. If an iris upgrade adds embeddings to gemini, or chat to
// voyageai, this fails and the table must be updated.
func TestProviderCapabilitiesMatchIris(t *testing.T) {
	// NewProvider requires a key for every provider except ollama; the
	// value is never used because we only ask about capabilities.
	for _, k := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "VOYAGEAI_API_KEY"} {
		t.Setenv(k, "capability-probe")
	}

	for _, name := range SupportedProviders() {
		t.Run(name, func(t *testing.T) {
			provider, err := NewProvider(name)
			if err != nil {
				t.Fatalf("NewProvider(%q): %v", name, err)
			}

			declared, ok := ProviderCapabilities(name)
			if !ok {
				t.Fatalf("ProviderCapabilities(%q) reported unknown provider", name)
			}

			if got := provider.Supports(core.FeatureChat); got != declared.Chat {
				t.Errorf("%s chat: table says %v, iris says %v", name, declared.Chat, got)
			}
			if got := provider.Supports(core.FeatureEmbeddings); got != declared.Embeddings {
				t.Errorf("%s embeddings: table says %v, iris says %v", name, declared.Embeddings, got)
			}
		})
	}
}

// TestEmbeddingProviderMatchesCapabilities checks the table against the
// other path that decides embedding support — the core.EmbeddingProvider
// type assertion in NewEmbeddingProvider.
func TestEmbeddingProviderMatchesCapabilities(t *testing.T) {
	for _, k := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "VOYAGEAI_API_KEY"} {
		t.Setenv(k, "capability-probe")
	}

	for _, name := range SupportedProviders() {
		caps, _ := ProviderCapabilities(name)
		_, err := NewEmbeddingProvider(name)
		if caps.Embeddings && err != nil {
			t.Errorf("%s: table says it embeds, NewEmbeddingProvider failed: %v", name, err)
		}
		if !caps.Embeddings && err == nil {
			t.Errorf("%s: table says it cannot embed, NewEmbeddingProvider succeeded", name)
		}
	}
}

func TestProviderCapabilitiesUnknownProvider(t *testing.T) {
	if _, ok := ProviderCapabilities("mistral"); ok {
		t.Error("ProviderCapabilities reported an unknown provider as known")
	}
}

// TestProviderListsAreSortedAndDerived keeps the advertised lists in the
// error messages stable and consistent with the table.
func TestProviderListsAreSortedAndDerived(t *testing.T) {
	assertSorted := func(label string, got []string) {
		t.Helper()
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Errorf("%s not sorted: %v", label, got)
				return
			}
		}
	}

	assertSorted("SupportedProviders", SupportedProviders())
	assertSorted("ChatProviders", ChatProviders())
	assertSorted("EmbeddingProviders", EmbeddingProviders())

	for _, name := range ChatProviders() {
		if caps, _ := ProviderCapabilities(name); !caps.Chat {
			t.Errorf("ChatProviders included %q, which does not support chat", name)
		}
	}
	for _, name := range EmbeddingProviders() {
		if caps, _ := ProviderCapabilities(name); !caps.Embeddings {
			t.Errorf("EmbeddingProviders included %q, which does not support embeddings", name)
		}
	}
}

package config

import (
	"strings"
	"testing"
)

// TestValidateProviders covers the provider names in embedding.provider and
// summarization.provider. Before this validation existed, an unusable
// combination passed Validate and failed later at client construction —
// "provider gemini does not support embeddings" arrived on the first search
// rather than at startup with the other config errors.
func TestValidateProviders(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Config)
		wantSubstr string
	}{
		{
			name:       "unknown embedding provider",
			mutate:     func(c *Config) { c.Embedding.Provider = "mistral" },
			wantSubstr: `embedding.provider "mistral" is not supported`,
		},
		{
			name:       "unknown summarization provider",
			mutate:     func(c *Config) { c.Summarization.Provider = "mistral" },
			wantSubstr: `summarization.provider "mistral" is not supported`,
		},
		{
			name:       "gemini cannot embed",
			mutate:     func(c *Config) { c.Embedding.Provider = "gemini" },
			wantSubstr: `embedding.provider "gemini" does not support embeddings`,
		},
		{
			name:       "anthropic cannot embed",
			mutate:     func(c *Config) { c.Embedding.Provider = "anthropic" },
			wantSubstr: `embedding.provider "anthropic" does not support embeddings`,
		},
		{
			name:       "voyageai cannot chat",
			mutate:     func(c *Config) { c.Summarization.Provider = "voyageai" },
			wantSubstr: `summarization.provider "voyageai" does not support summarization`,
		},
		{
			name:       "embedding provider empty",
			mutate:     func(c *Config) { c.Embedding.Provider = "" },
			wantSubstr: "embedding.provider is required",
		},
		{
			name:       "summarization provider empty",
			mutate:     func(c *Config) { c.Summarization.Provider = "" },
			wantSubstr: "summarization.provider is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mutate(t, tc.mutate).Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("expected error containing %q, got:\n%v", tc.wantSubstr, err)
			}
		})
	}
}

// TestValidateProviderErrorListsAlternatives checks the message tells the
// reader what to use instead, matching the style of the other validators.
func TestValidateProviderErrorListsAlternatives(t *testing.T) {
	err := mutate(t, func(c *Config) { c.Embedding.Provider = "gemini" }).Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"openai", "voyageai", "ollama"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the embedding alternatives to mention %q, got:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "anthropic") {
		t.Errorf("embedding alternatives must not list a provider that cannot embed, got:\n%v", err)
	}
}

// TestValidateAcceptsEveryCapableProvider locks each documented pairing so a
// valid combination is never rejected.
func TestValidateAcceptsEveryCapableProvider(t *testing.T) {
	for _, p := range []string{"openai", "voyageai", "ollama"} {
		if err := mutate(t, func(c *Config) { c.Embedding.Provider = p }).Validate(); err != nil {
			t.Errorf("embedding.provider %q should be valid, got: %v", p, err)
		}
	}
	for _, p := range []string{"openai", "anthropic", "gemini", "ollama"} {
		if err := mutate(t, func(c *Config) { c.Summarization.Provider = p }).Validate(); err != nil {
			t.Errorf("summarization.provider %q should be valid, got: %v", p, err)
		}
	}
}

// TestValidateProvidersReportedWithOtherProblems verifies provider errors
// join the aggregated report rather than short-circuiting it.
func TestValidateProvidersReportedWithOtherProblems(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Provider = "gemini"
	cfg.Summarization.Provider = "voyageai"
	cfg.Server.LogLevel = "loud"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		`embedding.provider "gemini"`,
		`summarization.provider "voyageai"`,
		`server.log_level "loud"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got:\n%v", want, err)
		}
	}
}

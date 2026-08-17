package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// documentedCommands lists command paths with argument sets exactly as
// shown in README.md and examples/cli-basics/README.md. If a documented
// invocation stops parsing — a renamed flag, a newly required flag, a
// missing positional — this table fails with the offending invocation.
// When updating README command examples, update this table in the same PR.
var documentedCommands = []struct {
	path string
	args []string
}{
	// README quick start
	{"knowledge ingest", []string{"--collection", "docs", "--title", "README", "--file", "README.md"}},
	{"knowledge search", []string{"how to install"}},
	{"conversation history", []string{"--thread-id", "my-thread"}},
	{"entity list", nil},

	// Knowledge (README + cli-basics)
	{"knowledge ingest", []string{"--collection", "docs", "--title", "API Reference", "--file", "api.md", "--chunk-strategy", "semantic", "--chunk-max-tokens", "512"}},
	{"knowledge ingest-dir", []string{"--collection", "project-docs", "--dir", "./docs", "--pattern", "*.md"}},
	{"knowledge search", []string{"how to configure"}},
	{"knowledge search", []string{"authentication", "--collection", "docs"}},
	{"knowledge search", []string{"user management", "--mode", "hybrid"}},
	// Every mode named in README's Search Modes table. The table drifted to
	// "fts" once while --mode only accepted "text"; locking all three keeps
	// the table and the flag from separating again.
	{"knowledge search", []string{"error handling", "--mode", "text"}},
	{"knowledge search", []string{"machine learning", "--mode", "vector"}},
	{"knowledge collections", nil},
	{"knowledge create-collection", []string{"--name", "research", "--description", "Research papers"}},
	{"knowledge stats", nil},

	// Conversation (README + cli-basics)
	{"conversation append", []string{"--thread-id", "project-chat", "--role", "user", "--content", "Let us discuss"}},
	{"conversation history", []string{"--thread-id", "project-chat", "--limit", "10"}},
	{"conversation search", []string{"architecture decisions"}},
	{"conversation summarize", []string{"--thread-id", "project-chat"}},
	{"conversation clear", []string{"--thread-id", "project-chat"}},

	// Context (README + cli-basics)
	{"context set", []string{"current_task", `"implementing auth"`}},
	{"context set", []string{"temp_token", `"abc123"`, "--ttl", "1h"}},
	{"context get", []string{"current_task"}},
	{"context list", nil},
	{"context list", []string{"--prefix", "project_"}},
	{"context history", []string{"current_task"}},
	{"context delete", []string{"current_task"}},

	// Entity (README + cli-basics)
	{"entity list", []string{"--type", "person"}},
	{"entity search", []string{"software engineer"}},
	{"entity create", []string{"--name", "Acme Corp", "--type", "organization"}},
	{"entity get", []string{"<entity-id>"}},
	{"entity add-alias", []string{"<entity-id>", "--alias", "Acme"}},
	{"entity add-relationship", []string{"<person-id>", "<org-id>", "--type", "works_at"}},
	{"entity merge", []string{"<keep-id>", "<remove-id>"}},
}

// findCommand resolves a "grandchild subcommand" path under rootCmd.
func findCommand(path string) *cobra.Command {
	parts := strings.Split(path, " ")
	cmd := rootCmd
	for _, part := range parts {
		next, _, err := cmd.Find([]string{part})
		if err != nil || next == nil || next.Name() != part {
			return nil
		}
		cmd = next
	}
	return cmd
}

// TestDocumentedCommandsParse walks each documented invocation through the
// same validation Execute performs (ParseFlags + required-flag check) and
// asserts it would be accepted.
func TestDocumentedCommandsParse(t *testing.T) {
	for _, tc := range documentedCommands {
		t.Run(fmt.Sprintf("%s %s", tc.path, strings.Join(tc.args, " ")), func(t *testing.T) {
			cmd := findCommand(tc.path)
			if cmd == nil {
				t.Fatalf("command %q not found", tc.path)
			}
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatalf("documented invocation no longer parses: %v", err)
			}
			if err := cmd.ValidateRequiredFlags(); err != nil {
				t.Fatalf("documented invocation missing a now-required flag: %v", err)
			}
			if err := cmd.ValidateFlagGroups(); err != nil {
				t.Fatalf("documented invocation fails flag-group validation: %v", err)
			}
		})
	}
}

// TestNamespaceDefaultsToDefault locks the documented "namespace optional,
// defaults to default" contract across every data-plane command: omitting
// --namespace must yield "default", never an empty namespace.
func TestNamespaceDefaultsToDefault(t *testing.T) {
	// Commands whose --namespace has other semantics (server restriction,
	// destructive opt-in) are excluded.
	excluded := map[string]bool{
		"cortex serve":            true,
		"cortex tui":              true,
		"cortex namespace delete": true,
		"cortex export":           true,
	}

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.Flags().Lookup("namespace"); f != nil && !excluded[c.CommandPath()] {
			if f.DefValue != "default" {
				t.Errorf("%s: --namespace default is %q, want \"default\"", c.CommandPath(), f.DefValue)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// TestNamespaceNeverRequired asserts via cobra's own required-flag
// validation that omitting --namespace is accepted on every data-plane
// command (docs: "All commands accept --namespace", optional).
func TestNamespaceNeverRequired(t *testing.T) {
	excluded := map[string]bool{
		"cortex serve":            true,
		"cortex tui":              true,
		"cortex namespace delete": true,
		"cortex export":           true,
	}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags() != nil {
			if f := c.Flags().Lookup("namespace"); f != nil && !excluded[c.CommandPath()] {
				_ = c.ParseFlags(nil)
				if err := c.ValidateRequiredFlags(); err != nil {
					if strings.Contains(err.Error(), `"namespace"`) {
						t.Errorf("%s: --namespace is required; docs say it defaults: %v", c.CommandPath(), err)
					}
				}
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cortex",
	Short: "Cortex - PetalFlow Memory & Knowledge Service",
	Long: `Cortex provides persistent context, vector-backed knowledge retrieval,
and conversation memory for PetalFlow agents.

It implements four memory primitives:
  - Conversation Memory: Agent dialogue history
  - Knowledge Store: Vector-indexed documents (RAG)
  - Workflow Context: Shared state across tasks/runs
  - Entity Memory: Auto-extracted knowledge graph`,
}

// SetVersionInfo records the build information injected at link time and
// enables the --version flag. An empty version means the binary was built
// without ldflags (go build / go run), which reports as "dev" rather than
// leaving --version unavailable.
func SetVersionInfo(version, commit, date string) {
	if version == "" {
		version = "dev"
	}

	var details []string
	if commit != "" {
		details = append(details, commit)
	}
	if date != "" {
		details = append(details, date)
	}
	if len(details) > 0 {
		version = fmt.Sprintf("%s (%s)", version, strings.Join(details, ", "))
	}

	rootCmd.Version = version
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default is ~/.cortex/config.yaml)")
}

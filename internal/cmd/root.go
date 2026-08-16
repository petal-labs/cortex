package cmd

import (
	"context"
	"os/signal"
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

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default is ~/.cortex/config.yaml)")
}

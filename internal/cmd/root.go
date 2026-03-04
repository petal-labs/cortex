package cmd

import (
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
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default is ~/.cortex/config.yaml)")
}

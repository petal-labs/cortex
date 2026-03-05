package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/summarization"
)

var conversationCmd = &cobra.Command{
	Use:   "conversation",
	Short: "Manage conversation memory",
	Long:  `Commands for managing conversation threads and messages.`,
}

var conversationHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Get message history for a thread",
	Long: `Retrieve message history for a conversation thread.

Examples:
  cortex conversation history --namespace myapp --thread session-123
  cortex conversation history --namespace myapp --thread session-123 --last 20 --include-summary`,
	RunE: runConversationHistory,
}

var conversationAppendCmd = &cobra.Command{
	Use:   "append",
	Short: "Append a message to a thread",
	Long: `Add a new message to a conversation thread.

Examples:
  cortex conversation append --namespace myapp --thread session-123 --role user --content "Hello"
  cortex conversation append --namespace myapp --thread session-123 --role assistant --content "Hi there"`,
	RunE: runConversationAppend,
}

var conversationSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search across conversations",
	Long: `Perform semantic search across conversation messages.

Examples:
  cortex conversation search --namespace myapp --query "authentication error"
  cortex conversation search --namespace myapp --query "API rate limit" --thread session-123 --top-k 5`,
	RunE: runConversationSearch,
}

var conversationListCmd = &cobra.Command{
	Use:   "list",
	Short: "List conversation threads",
	Long: `List all conversation threads in a namespace.

Examples:
  cortex conversation list --namespace myapp
  cortex conversation list --namespace myapp --limit 50`,
	RunE: runConversationList,
}

var conversationClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear a conversation thread",
	Long: `Delete a conversation thread and all its messages.

Examples:
  cortex conversation clear --namespace myapp --thread session-123`,
	RunE: runConversationClear,
}

var conversationSummarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Summarize a conversation thread",
	Long: `Manually trigger summarization of a conversation thread.

Examples:
  cortex conversation summarize --namespace myapp --thread session-123
  cortex conversation summarize --namespace myapp --thread session-123 --keep-recent 5`,
	RunE: runConversationSummarize,
}

func init() {
	rootCmd.AddCommand(conversationCmd)

	// history command
	conversationCmd.AddCommand(conversationHistoryCmd)
	conversationHistoryCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationHistoryCmd.Flags().StringP("thread", "t", "", "Thread ID (required)")
	conversationHistoryCmd.Flags().Int("last", 0, "Number of recent messages (0 = all)")
	conversationHistoryCmd.Flags().Bool("include-summary", false, "Include thread summary if available")
	conversationHistoryCmd.Flags().Bool("json", false, "Output as JSON")
	conversationHistoryCmd.MarkFlagRequired("namespace")
	conversationHistoryCmd.MarkFlagRequired("thread")

	// append command
	conversationCmd.AddCommand(conversationAppendCmd)
	conversationAppendCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationAppendCmd.Flags().StringP("thread", "t", "", "Thread ID (required)")
	conversationAppendCmd.Flags().StringP("role", "r", "", "Message role: user, assistant, system, tool (required)")
	conversationAppendCmd.Flags().StringP("content", "c", "", "Message content (required)")
	conversationAppendCmd.MarkFlagRequired("namespace")
	conversationAppendCmd.MarkFlagRequired("thread")
	conversationAppendCmd.MarkFlagRequired("role")
	conversationAppendCmd.MarkFlagRequired("content")

	// search command
	conversationCmd.AddCommand(conversationSearchCmd)
	conversationSearchCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationSearchCmd.Flags().StringP("query", "q", "", "Search query (required)")
	conversationSearchCmd.Flags().StringP("thread", "t", "", "Limit to thread (optional)")
	conversationSearchCmd.Flags().Int("top-k", 10, "Number of results")
	conversationSearchCmd.Flags().Float64("min-score", 0.0, "Minimum similarity score (0-1)")
	conversationSearchCmd.MarkFlagRequired("namespace")
	conversationSearchCmd.MarkFlagRequired("query")

	// list command
	conversationCmd.AddCommand(conversationListCmd)
	conversationListCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationListCmd.Flags().Int("limit", 50, "Max threads to return")
	conversationListCmd.MarkFlagRequired("namespace")

	// clear command
	conversationCmd.AddCommand(conversationClearCmd)
	conversationClearCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationClearCmd.Flags().StringP("thread", "t", "", "Thread ID (required)")
	conversationClearCmd.MarkFlagRequired("namespace")
	conversationClearCmd.MarkFlagRequired("thread")

	// summarize command
	conversationCmd.AddCommand(conversationSummarizeCmd)
	conversationSummarizeCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	conversationSummarizeCmd.Flags().StringP("thread", "t", "", "Thread ID (required)")
	conversationSummarizeCmd.Flags().Int("keep-recent", 10, "Number of recent messages to keep unsummarized")
	conversationSummarizeCmd.MarkFlagRequired("namespace")
	conversationSummarizeCmd.MarkFlagRequired("thread")
}

// initConversationEngine creates the storage backend and conversation engine.
func initConversationEngine(cmd *cobra.Command, withSummarizer bool) (*conversation.Engine, error) {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Create embedding provider if Iris is configured
	var emb embedding.Provider
	if cfg.Iris.Endpoint != "" {
		emb, err = embedding.NewIrisClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create embedding client: %w", err)
		}

		if cfg.Embedding.CacheSize > 0 {
			emb, err = embedding.NewCachedProvider(emb, cfg.Embedding.CacheSize)
			if err != nil {
				return nil, fmt.Errorf("failed to create embedding cache: %w", err)
			}
		}
	}

	engine, err := conversation.NewEngine(store, emb, &cfg.Conversation)
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation engine: %w", err)
	}

	// Set up summarization if requested and Iris is configured
	if withSummarizer && cfg.Iris.Endpoint != "" && cfg.Summarization.Model != "" {
		summClient := summarization.NewClient(cfg)
		engine.SetSummarizer(summClient)
	}

	return engine, nil
}

func runConversationHistory(cmd *cobra.Command, args []string) error {
	engine, err := initConversationEngine(cmd, false)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	threadID, _ := cmd.Flags().GetString("thread")
	lastN, _ := cmd.Flags().GetInt("last")
	includeSummary, _ := cmd.Flags().GetBool("include-summary")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	ctx := context.Background()
	result, err := engine.History(ctx, namespace, threadID, &conversation.HistoryOpts{
		LastN:             lastN,
		IncludeSummary:    includeSummary,
		SkipAutoSummarize: true, // Don't auto-summarize during CLI access
	})
	if err != nil {
		return fmt.Errorf("failed to get history: %w", err)
	}

	if jsonOutput {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("Thread: %s\n", threadID)
	fmt.Printf("Messages: %d\n\n", len(result.Messages))

	if result.Summary != "" {
		fmt.Println("[Summary]")
		fmt.Println(result.Summary)
		fmt.Println()
	}

	for _, msg := range result.Messages {
		fmt.Printf("[%s] %s\n", msg.Role, msg.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println(msg.Content)
		fmt.Println()
	}

	if result.NextCursor != "" {
		fmt.Printf("Next cursor: %s\n", result.NextCursor)
	}

	return nil
}

func runConversationAppend(cmd *cobra.Command, args []string) error {
	engine, err := initConversationEngine(cmd, false)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	threadID, _ := cmd.Flags().GetString("thread")
	role, _ := cmd.Flags().GetString("role")
	content, _ := cmd.Flags().GetString("content")

	// Validate role
	validRoles := map[string]bool{"user": true, "assistant": true, "system": true, "tool": true}
	if !validRoles[role] {
		return fmt.Errorf("invalid role: %s (must be user, assistant, system, or tool)", role)
	}

	ctx := context.Background()
	msg, err := engine.Append(ctx, namespace, threadID, role, content, nil)
	if err != nil {
		return fmt.Errorf("failed to append message: %w", err)
	}

	fmt.Printf("Appended message: %s\n", msg.ID)
	fmt.Printf("  Thread: %s\n", msg.ThreadID)
	fmt.Printf("  Role: %s\n", msg.Role)
	fmt.Printf("  Created: %s\n", msg.CreatedAt.Format("2006-01-02 15:04:05"))

	return nil
}

func runConversationSearch(cmd *cobra.Command, args []string) error {
	engine, err := initConversationEngine(cmd, false)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	query, _ := cmd.Flags().GetString("query")
	threadID, _ := cmd.Flags().GetString("thread")
	topK, _ := cmd.Flags().GetInt("top-k")
	minScore, _ := cmd.Flags().GetFloat64("min-score")

	opts := &conversation.SearchOpts{
		TopK:     topK,
		MinScore: minScore,
	}
	if threadID != "" {
		opts.ThreadID = &threadID
	}

	ctx := context.Background()
	result, err := engine.Search(ctx, namespace, query, opts)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	fmt.Printf("Search results for: %q\n", query)
	fmt.Printf("Found: %d results\n\n", len(result.Results))

	for i, r := range result.Results {
		fmt.Printf("--- Result %d (score: %.3f) ---\n", i+1, r.Score)
		fmt.Printf("Thread: %s\n", r.Message.ThreadID)
		fmt.Printf("Role: %s\n", r.Message.Role)
		fmt.Printf("Time: %s\n", r.Message.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("\n%s\n\n", r.Message.Content)
	}

	return nil
}

func runConversationList(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	limit, _ := cmd.Flags().GetInt("limit")

	ctx := context.Background()
	threads, _, err := store.ListThreads(ctx, namespace, "", limit)
	if err != nil {
		return fmt.Errorf("failed to list threads: %w", err)
	}

	if len(threads) == 0 {
		fmt.Println("No threads found")
		return nil
	}

	fmt.Printf("Threads in namespace %q:\n\n", namespace)
	for _, t := range threads {
		fmt.Printf("  %s\n", t.ID)
		if t.Title != "" {
			fmt.Printf("    Title: %s\n", t.Title)
		}
		fmt.Printf("    Created: %s\n", t.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("    Last activity: %s\n", t.UpdatedAt.Format("2006-01-02 15:04:05"))
		if t.Summary != "" {
			summary := t.Summary
			if len(summary) > 100 {
				summary = summary[:100] + "..."
			}
			fmt.Printf("    Summary: %s\n", summary)
		}
		fmt.Println()
	}

	return nil
}

func runConversationClear(cmd *cobra.Command, args []string) error {
	engine, err := initConversationEngine(cmd, false)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	threadID, _ := cmd.Flags().GetString("thread")

	ctx := context.Background()
	if err := engine.Clear(ctx, namespace, threadID); err != nil {
		return fmt.Errorf("failed to clear thread: %w", err)
	}

	fmt.Printf("Cleared thread: %s\n", threadID)
	return nil
}

func runConversationSummarize(cmd *cobra.Command, args []string) error {
	engine, err := initConversationEngine(cmd, true)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	threadID, _ := cmd.Flags().GetString("thread")
	keepRecent, _ := cmd.Flags().GetInt("keep-recent")

	ctx := context.Background()
	result, err := engine.Summarize(ctx, namespace, threadID, &conversation.SummarizeOpts{
		KeepRecent: keepRecent,
	})
	if err != nil {
		return fmt.Errorf("failed to summarize: %w", err)
	}

	fmt.Printf("Summarized thread: %s\n", result.ThreadID)
	fmt.Printf("  Messages summarized: %d\n", result.MessagesSummarized)
	fmt.Printf("  Messages kept: %d\n", result.MessagesKept)
	fmt.Printf("\nSummary:\n%s\n", result.Summary)

	return nil
}

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	ctxengine "github.com/petal-labs/cortex/internal/context"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage workflow context",
	Long:  `Commands for managing workflow context key-value storage.`,
}

var contextGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a context value",
	Long: `Retrieve a value from the workflow context.

Examples:
  cortex context get --namespace myapp --key user_preferences
  cortex context get --namespace myapp --key task_state --run-id run-123`,
	RunE: runContextGet,
}

var contextSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set a context value",
	Long: `Store a value in the workflow context.

Examples:
  cortex context set --namespace myapp --key user_preferences --value '{"theme":"dark"}'
  cortex context set --namespace myapp --key task_state --value '{"step":2}' --run-id run-123 --ttl 3600`,
	RunE: runContextSet,
}

var contextDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a context key",
	Long: `Delete a key from the workflow context.

Examples:
  cortex context delete --namespace myapp --key user_preferences
  cortex context delete --namespace myapp --key task_state --run-id run-123`,
	RunE: runContextDelete,
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List context keys",
	Long: `List all keys in a namespace or matching a prefix.

Examples:
  cortex context list --namespace myapp
  cortex context list --namespace myapp --prefix user_
  cortex context list --namespace myapp --run-id run-123`,
	RunE: runContextList,
}

var contextHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Get version history for a key",
	Long: `Retrieve version history for a context key.

Examples:
  cortex context history --namespace myapp --key user_preferences
  cortex context history --namespace myapp --key task_state --run-id run-123 --limit 10`,
	RunE: runContextHistory,
}

var contextCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Clean up expired or run-scoped context",
	Long: `Clean up expired context entries or all context for a specific run.

Examples:
  cortex context cleanup --expired
  cortex context cleanup --namespace myapp --run-id run-123`,
	RunE: runContextCleanup,
}

func init() {
	rootCmd.AddCommand(contextCmd)

	// get command
	contextCmd.AddCommand(contextGetCmd)
	contextGetCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	contextGetCmd.Flags().StringP("key", "k", "", "Context key (required)")
	contextGetCmd.Flags().String("run-id", "", "Run ID for run-scoped context (optional)")
	contextGetCmd.MarkFlagRequired("namespace")
	contextGetCmd.MarkFlagRequired("key")

	// set command
	contextCmd.AddCommand(contextSetCmd)
	contextSetCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	contextSetCmd.Flags().StringP("key", "k", "", "Context key (required)")
	contextSetCmd.Flags().StringP("value", "v", "", "JSON value (required)")
	contextSetCmd.Flags().String("run-id", "", "Run ID for run-scoped context (optional)")
	contextSetCmd.Flags().Int("ttl", 0, "Time-to-live in seconds (0 = no expiration)")
	contextSetCmd.Flags().Int64("expected-version", 0, "Expected version for optimistic concurrency")
	contextSetCmd.MarkFlagRequired("namespace")
	contextSetCmd.MarkFlagRequired("key")
	contextSetCmd.MarkFlagRequired("value")

	// delete command
	contextCmd.AddCommand(contextDeleteCmd)
	contextDeleteCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	contextDeleteCmd.Flags().StringP("key", "k", "", "Context key (required)")
	contextDeleteCmd.Flags().String("run-id", "", "Run ID for run-scoped context (optional)")
	contextDeleteCmd.MarkFlagRequired("namespace")
	contextDeleteCmd.MarkFlagRequired("key")

	// list command
	contextCmd.AddCommand(contextListCmd)
	contextListCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	contextListCmd.Flags().StringP("prefix", "p", "", "Key prefix filter (optional)")
	contextListCmd.Flags().String("run-id", "", "Run ID filter (optional)")
	contextListCmd.Flags().Int("limit", 100, "Max results")
	contextListCmd.MarkFlagRequired("namespace")

	// history command
	contextCmd.AddCommand(contextHistoryCmd)
	contextHistoryCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	contextHistoryCmd.Flags().StringP("key", "k", "", "Context key (required)")
	contextHistoryCmd.Flags().String("run-id", "", "Run ID for run-scoped context (optional)")
	contextHistoryCmd.Flags().Int("limit", 20, "Max versions to return")
	contextHistoryCmd.MarkFlagRequired("namespace")
	contextHistoryCmd.MarkFlagRequired("key")

	// cleanup command
	contextCmd.AddCommand(contextCleanupCmd)
	contextCleanupCmd.Flags().StringP("namespace", "n", "", "Namespace (for run cleanup)")
	contextCleanupCmd.Flags().String("run-id", "", "Run ID to clean up")
	contextCleanupCmd.Flags().Bool("expired", false, "Clean up all expired entries")
}

// initContextEngine creates the storage backend and context engine.
func initContextEngine(cmd *cobra.Command) (*ctxengine.Engine, error) {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	engine, err := ctxengine.NewEngine(store, &cfg.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to create context engine: %w", err)
	}

	return engine, nil
}

func runContextGet(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	key, _ := cmd.Flags().GetString("key")
	runID, _ := cmd.Flags().GetString("run-id")

	opts := &ctxengine.GetOpts{}
	if runID != "" {
		opts.RunID = &runID
	}

	ctx := context.Background()
	result, err := engine.Get(ctx, namespace, key, opts)
	if err != nil {
		return fmt.Errorf("failed to get context: %w", err)
	}

	if !result.Exists {
		fmt.Println("Key not found")
		return nil
	}

	// Pretty print the value
	data, err := json.MarshalIndent(result.Value, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	fmt.Printf("Key: %s\n", key)
	fmt.Printf("Version: %d\n", result.Version)
	fmt.Printf("Updated: %s\n", result.UpdatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("\nValue:\n%s\n", string(data))

	return nil
}

func runContextSet(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	key, _ := cmd.Flags().GetString("key")
	valueStr, _ := cmd.Flags().GetString("value")
	runID, _ := cmd.Flags().GetString("run-id")
	ttlSeconds, _ := cmd.Flags().GetInt("ttl")
	expectedVersion, _ := cmd.Flags().GetInt64("expected-version")

	// Parse JSON value
	var value any
	if err := json.Unmarshal([]byte(valueStr), &value); err != nil {
		return fmt.Errorf("invalid JSON value: %w", err)
	}

	opts := &ctxengine.SetOpts{}
	if ttlSeconds > 0 {
		opts.TTL = time.Duration(ttlSeconds) * time.Second
	}
	if runID != "" {
		opts.RunID = &runID
	}
	if expectedVersion > 0 {
		opts.ExpectedVersion = &expectedVersion
	}

	ctx := context.Background()
	result, err := engine.Set(ctx, namespace, key, value, opts)
	if err != nil {
		return fmt.Errorf("failed to set context: %w", err)
	}

	fmt.Printf("Set key: %s\n", key)
	fmt.Printf("  Version: %d\n", result.Version)
	if result.PreviousVersion > 0 {
		fmt.Printf("  Previous version: %d\n", result.PreviousVersion)
	}

	return nil
}

func runContextDelete(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	key, _ := cmd.Flags().GetString("key")
	runID, _ := cmd.Flags().GetString("run-id")

	var runIDPtr *string
	if runID != "" {
		runIDPtr = &runID
	}

	ctx := context.Background()
	if err := engine.Delete(ctx, namespace, key, runIDPtr); err != nil {
		return fmt.Errorf("failed to delete context: %w", err)
	}

	fmt.Printf("Deleted key: %s\n", key)
	return nil
}

func runContextList(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	prefix, _ := cmd.Flags().GetString("prefix")
	runID, _ := cmd.Flags().GetString("run-id")
	limit, _ := cmd.Flags().GetInt("limit")

	opts := &ctxengine.ListOpts{
		Limit: limit,
	}
	if prefix != "" {
		opts.Prefix = &prefix
	}
	if runID != "" {
		opts.RunID = &runID
	}

	ctx := context.Background()
	result, err := engine.List(ctx, namespace, opts)
	if err != nil {
		return fmt.Errorf("failed to list context: %w", err)
	}

	if len(result.Keys) == 0 {
		fmt.Println("No keys found")
		return nil
	}

	fmt.Printf("Keys in namespace %q", namespace)
	if prefix != "" {
		fmt.Printf(" (prefix: %q)", prefix)
	}
	fmt.Printf(":\n\n")

	for _, key := range result.Keys {
		fmt.Printf("  %s\n", key)
	}

	if result.NextCursor != "" {
		fmt.Printf("\nNext cursor: %s\n", result.NextCursor)
	}

	return nil
}

func runContextHistory(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	key, _ := cmd.Flags().GetString("key")
	runID, _ := cmd.Flags().GetString("run-id")
	limit, _ := cmd.Flags().GetInt("limit")

	opts := &ctxengine.HistoryOpts{
		Limit: limit,
	}
	if runID != "" {
		opts.RunID = &runID
	}

	ctx := context.Background()
	result, err := engine.History(ctx, namespace, key, opts)
	if err != nil {
		return fmt.Errorf("failed to get history: %w", err)
	}

	if len(result.History) == 0 {
		fmt.Println("No version history found")
		return nil
	}

	fmt.Printf("Version history for key %q:\n\n", key)

	for _, entry := range result.History {
		fmt.Printf("Version %d (%s) - %s\n", entry.Version, entry.UpdatedAt.Format("2006-01-02 15:04:05"), entry.Operation)
		data, _ := json.MarshalIndent(entry.Value, "  ", "  ")
		fmt.Printf("  %s\n\n", string(data))
	}

	if result.NextCursor != "" {
		fmt.Printf("Next cursor: %s\n", result.NextCursor)
	}

	return nil
}

func runContextCleanup(cmd *cobra.Command, args []string) error {
	engine, err := initContextEngine(cmd)
	if err != nil {
		return err
	}

	expired, _ := cmd.Flags().GetBool("expired")
	namespace, _ := cmd.Flags().GetString("namespace")
	runID, _ := cmd.Flags().GetString("run-id")

	ctx := context.Background()

	if expired {
		count, err := engine.CleanupExpired(ctx)
		if err != nil {
			return fmt.Errorf("failed to cleanup expired: %w", err)
		}
		fmt.Printf("Cleaned up %d expired entries\n", count)
		return nil
	}

	if namespace != "" && runID != "" {
		if err := engine.CleanupRun(ctx, namespace, runID); err != nil {
			return fmt.Errorf("failed to cleanup run: %w", err)
		}
		fmt.Printf("Cleaned up context for run: %s\n", runID)
		return nil
	}

	return fmt.Errorf("must specify either --expired or both --namespace and --run-id")
}

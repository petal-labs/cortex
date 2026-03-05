package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/pkg/types"
)

var entityCmd = &cobra.Command{
	Use:   "entity",
	Short: "Manage entity memory",
	Long:  `Commands for managing entities extracted from conversations and documents.`,
}

var entityCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new entity",
	Long: `Create a new entity with name, type, and optional attributes.

Examples:
  cortex entity create --namespace myapp --name "John Doe" --type person
  cortex entity create --namespace myapp --name "Acme Corp" --type organization --summary "A tech company"`,
	RunE: runEntityCreate,
}

var entityGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get an entity by ID or name",
	Long: `Retrieve an entity by its ID or name/alias.

Examples:
  cortex entity get --namespace myapp --id entity-123
  cortex entity get --namespace myapp --name "John Doe"`,
	RunE: runEntityGet,
}

var entityDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete an entity",
	Long: `Delete an entity and its relationships.

Examples:
  cortex entity delete --namespace myapp --id entity-123`,
	RunE: runEntityDelete,
}

var entityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List entities",
	Long: `List all entities in a namespace with optional type filter.

Examples:
  cortex entity list --namespace myapp
  cortex entity list --namespace myapp --type person --limit 50`,
	RunE: runEntityList,
}

var entitySearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search entities semantically",
	Long: `Search for entities using semantic similarity.

Examples:
  cortex entity search --namespace myapp --query "tech company"
  cortex entity search --namespace myapp --query "software developer" --type person --top-k 5`,
	RunE: runEntitySearch,
}

var entityAddAliasCmd = &cobra.Command{
	Use:   "add-alias",
	Short: "Add an alias to an entity",
	Long: `Add an alternative name or alias to an entity.

Examples:
  cortex entity add-alias --namespace myapp --id entity-123 --alias "Johnny"`,
	RunE: runEntityAddAlias,
}

var entityAddRelationshipCmd = &cobra.Command{
	Use:   "add-relationship",
	Short: "Add a relationship between entities",
	Long: `Create a relationship between two entities.

Examples:
  cortex entity add-relationship --namespace myapp --source entity-1 --target entity-2 --type "works_at"
  cortex entity add-relationship --namespace myapp --source entity-1 --target entity-2 --type "knows" --description "colleagues"`,
	RunE: runEntityAddRelationship,
}

var entityMergeCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge two entities",
	Long: `Merge a source entity into a target entity, combining their attributes and relationships.

Examples:
  cortex entity merge --namespace myapp --source entity-dup --target entity-main`,
	RunE: runEntityMerge,
}

var entityQueueStatsCmd = &cobra.Command{
	Use:   "queue-stats",
	Short: "Show extraction queue statistics",
	Long: `Display statistics about the entity extraction queue.

Examples:
  cortex entity queue-stats`,
	RunE: runEntityQueueStats,
}

func init() {
	rootCmd.AddCommand(entityCmd)

	// create command
	entityCmd.AddCommand(entityCreateCmd)
	entityCreateCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityCreateCmd.Flags().String("name", "", "Entity name (required)")
	entityCreateCmd.Flags().String("type", "", "Entity type: person, organization, location, concept, product (required)")
	entityCreateCmd.Flags().String("summary", "", "Entity summary/description")
	entityCreateCmd.Flags().StringSlice("aliases", nil, "Additional aliases")
	entityCreateCmd.MarkFlagRequired("namespace")
	entityCreateCmd.MarkFlagRequired("name")
	entityCreateCmd.MarkFlagRequired("type")

	// get command
	entityCmd.AddCommand(entityGetCmd)
	entityGetCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityGetCmd.Flags().String("id", "", "Entity ID")
	entityGetCmd.Flags().String("name", "", "Entity name or alias (uses resolve)")
	entityGetCmd.MarkFlagRequired("namespace")

	// delete command
	entityCmd.AddCommand(entityDeleteCmd)
	entityDeleteCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityDeleteCmd.Flags().String("id", "", "Entity ID (required)")
	entityDeleteCmd.MarkFlagRequired("namespace")
	entityDeleteCmd.MarkFlagRequired("id")

	// list command
	entityCmd.AddCommand(entityListCmd)
	entityListCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityListCmd.Flags().String("type", "", "Filter by entity type")
	entityListCmd.Flags().Int("limit", 50, "Max entities to return")
	entityListCmd.MarkFlagRequired("namespace")

	// search command
	entityCmd.AddCommand(entitySearchCmd)
	entitySearchCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entitySearchCmd.Flags().StringP("query", "q", "", "Search query (required)")
	entitySearchCmd.Flags().String("type", "", "Filter by entity type")
	entitySearchCmd.Flags().Int("top-k", 10, "Number of results")
	entitySearchCmd.MarkFlagRequired("namespace")
	entitySearchCmd.MarkFlagRequired("query")

	// add-alias command
	entityCmd.AddCommand(entityAddAliasCmd)
	entityAddAliasCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityAddAliasCmd.Flags().String("id", "", "Entity ID (required)")
	entityAddAliasCmd.Flags().String("alias", "", "Alias to add (required)")
	entityAddAliasCmd.MarkFlagRequired("namespace")
	entityAddAliasCmd.MarkFlagRequired("id")
	entityAddAliasCmd.MarkFlagRequired("alias")

	// add-relationship command
	entityCmd.AddCommand(entityAddRelationshipCmd)
	entityAddRelationshipCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityAddRelationshipCmd.Flags().String("source", "", "Source entity ID (required)")
	entityAddRelationshipCmd.Flags().String("target", "", "Target entity ID (required)")
	entityAddRelationshipCmd.Flags().String("type", "", "Relationship type (required)")
	entityAddRelationshipCmd.Flags().String("description", "", "Relationship description")
	entityAddRelationshipCmd.MarkFlagRequired("namespace")
	entityAddRelationshipCmd.MarkFlagRequired("source")
	entityAddRelationshipCmd.MarkFlagRequired("target")
	entityAddRelationshipCmd.MarkFlagRequired("type")

	// merge command
	entityCmd.AddCommand(entityMergeCmd)
	entityMergeCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	entityMergeCmd.Flags().String("source", "", "Source entity ID (will be merged into target)")
	entityMergeCmd.Flags().String("target", "", "Target entity ID (will receive source's data)")
	entityMergeCmd.MarkFlagRequired("namespace")
	entityMergeCmd.MarkFlagRequired("source")
	entityMergeCmd.MarkFlagRequired("target")

	// queue-stats command
	entityCmd.AddCommand(entityQueueStatsCmd)
}

// initEntityEngine creates the storage backend and entity engine.
func initEntityEngine(cmd *cobra.Command) (*entity.Engine, error) {
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

	engine, err := entity.NewEngine(store, emb, &cfg.Entity)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity engine: %w", err)
	}

	return engine, nil
}

func runEntityCreate(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	name, _ := cmd.Flags().GetString("name")
	entityTypeStr, _ := cmd.Flags().GetString("type")
	summary, _ := cmd.Flags().GetString("summary")
	aliases, _ := cmd.Flags().GetStringSlice("aliases")

	// Validate entity type
	entityType := types.EntityType(entityTypeStr)
	validTypes := map[types.EntityType]bool{
		types.EntityTypePerson:       true,
		types.EntityTypeOrganization: true,
		types.EntityTypeLocation:     true,
		types.EntityTypeConcept:      true,
		types.EntityTypeProduct:      true,
	}
	if !validTypes[entityType] {
		return fmt.Errorf("invalid entity type: %s (must be person, organization, location, concept, or product)", entityTypeStr)
	}

	opts := &entity.CreateOpts{
		Summary: summary,
		Aliases: aliases,
	}

	ctx := context.Background()
	result, err := engine.Create(ctx, namespace, name, entityType, opts)
	if err != nil {
		return fmt.Errorf("failed to create entity: %w", err)
	}

	fmt.Printf("Created entity: %s\n", result.Entity.ID)
	fmt.Printf("  Name: %s\n", result.Entity.Name)
	fmt.Printf("  Type: %s\n", result.Entity.Type)
	if result.Entity.Summary != "" {
		fmt.Printf("  Summary: %s\n", result.Entity.Summary)
	}

	return nil
}

func runEntityGet(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	entityID, _ := cmd.Flags().GetString("id")
	name, _ := cmd.Flags().GetString("name")

	if entityID == "" && name == "" {
		return fmt.Errorf("must specify either --id or --name")
	}

	ctx := context.Background()
	var ent *types.Entity

	if entityID != "" {
		ent, err = engine.Get(ctx, namespace, entityID)
	} else {
		// Use Resolve to find by name or alias
		ent, err = engine.Resolve(ctx, namespace, name)
	}
	if err != nil {
		return fmt.Errorf("failed to get entity: %w", err)
	}

	if ent == nil {
		fmt.Println("Entity not found")
		return nil
	}

	// Pretty print the entity
	data, _ := json.MarshalIndent(ent, "", "  ")
	fmt.Println(string(data))

	return nil
}

func runEntityDelete(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	entityID, _ := cmd.Flags().GetString("id")

	ctx := context.Background()
	if err := engine.Delete(ctx, namespace, entityID); err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	fmt.Printf("Deleted entity: %s\n", entityID)
	return nil
}

func runEntityList(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	typeFilter, _ := cmd.Flags().GetString("type")
	limit, _ := cmd.Flags().GetInt("limit")

	opts := &entity.ListOpts{
		Limit: limit,
	}
	if typeFilter != "" {
		entityType := types.EntityType(typeFilter)
		opts.EntityType = &entityType
	}

	ctx := context.Background()
	result, err := engine.List(ctx, namespace, opts)
	if err != nil {
		return fmt.Errorf("failed to list entities: %w", err)
	}

	if len(result.Entities) == 0 {
		fmt.Println("No entities found")
		return nil
	}

	fmt.Printf("Entities in namespace %q", namespace)
	if typeFilter != "" {
		fmt.Printf(" (type: %s)", typeFilter)
	}
	fmt.Printf(":\n\n")

	for _, e := range result.Entities {
		fmt.Printf("  %s [%s]\n", e.Name, e.Type)
		fmt.Printf("    ID: %s\n", e.ID)
		if e.Summary != "" {
			summary := e.Summary
			if len(summary) > 80 {
				summary = summary[:80] + "..."
			}
			fmt.Printf("    Summary: %s\n", summary)
		}
		fmt.Printf("    Mentions: %d\n", e.MentionCount)
		fmt.Println()
	}

	if result.NextCursor != "" {
		fmt.Printf("Next cursor: %s\n", result.NextCursor)
	}

	return nil
}

func runEntitySearch(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	query, _ := cmd.Flags().GetString("query")
	typeFilter, _ := cmd.Flags().GetString("type")
	topK, _ := cmd.Flags().GetInt("top-k")

	opts := &entity.SearchOpts{
		TopK: topK,
	}
	if typeFilter != "" {
		entityType := types.EntityType(typeFilter)
		opts.EntityType = &entityType
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
		fmt.Printf("Name: %s [%s]\n", r.Entity.Name, r.Entity.Type)
		fmt.Printf("ID: %s\n", r.Entity.ID)
		if r.Entity.Summary != "" {
			fmt.Printf("Summary: %s\n", r.Entity.Summary)
		}
		fmt.Println()
	}

	return nil
}

func runEntityAddAlias(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	entityID, _ := cmd.Flags().GetString("id")
	alias, _ := cmd.Flags().GetString("alias")

	ctx := context.Background()
	if err := engine.AddAlias(ctx, namespace, entityID, alias); err != nil {
		return fmt.Errorf("failed to add alias: %w", err)
	}

	fmt.Printf("Added alias %q to entity %s\n", alias, entityID)
	return nil
}

func runEntityAddRelationship(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	sourceID, _ := cmd.Flags().GetString("source")
	targetID, _ := cmd.Flags().GetString("target")
	relType, _ := cmd.Flags().GetString("type")
	description, _ := cmd.Flags().GetString("description")

	opts := &entity.RelationshipOpts{
		Description: description,
	}

	ctx := context.Background()
	rel, err := engine.AddRelationship(ctx, namespace, sourceID, targetID, relType, opts)
	if err != nil {
		return fmt.Errorf("failed to add relationship: %w", err)
	}

	fmt.Printf("Created relationship: %s\n", rel.ID)
	fmt.Printf("  %s --[%s]--> %s\n", rel.SourceEntityID, rel.RelationType, rel.TargetEntityID)

	return nil
}

func runEntityMerge(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	sourceID, _ := cmd.Flags().GetString("source")
	targetID, _ := cmd.Flags().GetString("target")

	ctx := context.Background()
	result, err := engine.Merge(ctx, namespace, sourceID, targetID)
	if err != nil {
		return fmt.Errorf("failed to merge entities: %w", err)
	}

	fmt.Printf("Merged entity %s into %s\n", sourceID, targetID)
	fmt.Printf("  Mentions merged: %d\n", result.MergedMentions)
	fmt.Printf("  Relationships merged: %d\n", result.MergedRelationships)
	fmt.Printf("  Result entity: %s\n", result.KeptEntity.ID)

	return nil
}

func runEntityQueueStats(cmd *cobra.Command, args []string) error {
	engine, err := initEntityEngine(cmd)
	if err != nil {
		return err
	}

	ctx := context.Background()
	stats, err := engine.ExtractionQueueStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get queue stats: %w", err)
	}

	fmt.Println("Extraction Queue Statistics:")
	fmt.Printf("  Pending: %d\n", stats.PendingCount)
	fmt.Printf("  Processing: %d\n", stats.ProcessingCount)
	fmt.Printf("  Failed: %d\n", stats.FailedCount)
	fmt.Printf("  Dead letter: %d\n", stats.DeadLetterCount)

	return nil
}

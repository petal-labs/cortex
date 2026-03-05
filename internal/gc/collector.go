package gc

import (
	"context"
	"sync"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/observability"
	"github.com/petal-labs/cortex/internal/storage"
	"go.uber.org/zap"
)

// Collector performs background garbage collection on storage data.
type Collector struct {
	store  storage.Backend
	config *config.Config

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewCollector creates a new garbage collector.
func NewCollector(store storage.Backend, cfg *config.Config) *Collector {
	return &Collector{
		store:  store,
		config: cfg,
		stopCh: make(chan struct{}),
	}
}

// Start begins the background garbage collection goroutines.
func (c *Collector) Start() {
	// Start the main GC goroutine (runs at GCInterval)
	c.wg.Add(1)
	go c.runGCLoop()

	// Start the TTL cleanup goroutine (runs more frequently)
	c.wg.Add(1)
	go c.runTTLLoop()

	observability.Info(context.Background(), "garbage collector started",
		zap.Duration("gc_interval", c.config.Retention.GCInterval),
		zap.Duration("ttl_cleanup_interval", c.config.Context.TTLCleanupInterval),
	)
}

// Stop gracefully stops the garbage collector.
func (c *Collector) Stop() {
	close(c.stopCh)
	c.wg.Wait()
	observability.Info(context.Background(), "garbage collector stopped")
}

// runGCLoop runs the main garbage collection at the configured interval.
func (c *Collector) runGCLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.Retention.GCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.runFullGC()
		}
	}
}

// runTTLLoop runs TTL cleanup at the configured interval.
func (c *Collector) runTTLLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.Context.TTLCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.RunTTLCleanup(context.Background())
		}
	}
}

// runFullGC runs all garbage collection tasks.
func (c *Collector) runFullGC() {
	ctx := context.Background()
	start := time.Now()

	observability.Info(ctx, "starting garbage collection cycle")

	results := &GCResults{}

	// Run each GC task
	results.ExpiredTTL = c.RunTTLCleanup(ctx)
	results.OldConversations = c.RunConversationCleanup(ctx)
	results.StaleEntities = c.RunEntityPruning(ctx)
	results.OrphanedChunks = c.RunOrphanedChunkCleanup(ctx)
	results.OldContextHistory = c.RunContextHistoryCleanup(ctx)
	results.OldRunContext = c.RunRunContextCleanup(ctx)

	observability.Info(ctx, "garbage collection cycle completed",
		zap.Duration("duration", time.Since(start)),
		zap.Int64("expired_ttl", results.ExpiredTTL),
		zap.Int64("old_conversations", results.OldConversations),
		zap.Int64("stale_entities", results.StaleEntities),
		zap.Int64("orphaned_chunks", results.OrphanedChunks),
		zap.Int64("old_context_history", results.OldContextHistory),
		zap.Int64("old_run_context", results.OldRunContext),
	)
}

// GCResults holds the results of a garbage collection run.
type GCResults struct {
	ExpiredTTL        int64
	OldConversations  int64
	StaleEntities     int64
	OrphanedChunks    int64
	OldContextHistory int64
	OldRunContext     int64
}

// RunTTLCleanup removes context entries past their TTL expiration.
// This runs more frequently than the main GC cycle.
func (c *Collector) RunTTLCleanup(ctx context.Context) int64 {
	deleted, err := c.store.CleanupExpiredContext(ctx)
	if err != nil {
		observability.Error(ctx, "failed to cleanup expired TTL context",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "cleaned up expired TTL context entries",
			zap.Int64("deleted", deleted))
	}
	return deleted
}

// RunConversationCleanup removes old conversations based on retention policy.
func (c *Collector) RunConversationCleanup(ctx context.Context) int64 {
	if c.config.Retention.ConversationRetentionDays <= 0 {
		return 0 // Retention disabled
	}

	duration := time.Duration(c.config.Retention.ConversationRetentionDays) * 24 * time.Hour
	deleted, err := c.store.DeleteOldConversations(ctx, duration)
	if err != nil {
		observability.Error(ctx, "failed to delete old conversations",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "deleted old conversations",
			zap.Int64("deleted", deleted),
			zap.Int("retention_days", c.config.Retention.ConversationRetentionDays))
	}
	return deleted
}

// RunEntityPruning removes stale entities based on retention policy.
func (c *Collector) RunEntityPruning(ctx context.Context) int64 {
	if c.config.Retention.EntityStaleDays <= 0 {
		return 0 // Pruning disabled
	}

	duration := time.Duration(c.config.Retention.EntityStaleDays) * 24 * time.Hour
	minMentions := c.config.Entity.MinMentionsToKeep
	deleted, err := c.store.PruneStaleEntities(ctx, duration, minMentions)
	if err != nil {
		observability.Error(ctx, "failed to prune stale entities",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "pruned stale entities",
			zap.Int64("deleted", deleted),
			zap.Int("stale_days", c.config.Retention.EntityStaleDays),
			zap.Int("min_mentions", minMentions))
	}
	return deleted
}

// RunOrphanedChunkCleanup removes chunks whose parent documents no longer exist.
func (c *Collector) RunOrphanedChunkCleanup(ctx context.Context) int64 {
	deleted, err := c.store.DeleteOrphanedChunks(ctx)
	if err != nil {
		observability.Error(ctx, "failed to delete orphaned chunks",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "deleted orphaned chunks",
			zap.Int64("deleted", deleted))
	}
	return deleted
}

// RunContextHistoryCleanup removes old context version history entries.
func (c *Collector) RunContextHistoryCleanup(ctx context.Context) int64 {
	if c.config.Context.HistoryRetentionDays <= 0 {
		return 0 // Retention disabled
	}

	duration := time.Duration(c.config.Context.HistoryRetentionDays) * 24 * time.Hour
	deleted, err := c.store.CleanupContextHistory(ctx, duration)
	if err != nil {
		observability.Error(ctx, "failed to cleanup context history",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "cleaned up old context history",
			zap.Int64("deleted", deleted),
			zap.Int("retention_days", c.config.Context.HistoryRetentionDays))
	}
	return deleted
}

// RunRunContextCleanup removes old run-scoped context entries.
func (c *Collector) RunRunContextCleanup(ctx context.Context) int64 {
	if c.config.Retention.ContextRunRetentionDays <= 0 {
		return 0 // Retention disabled
	}

	duration := time.Duration(c.config.Retention.ContextRunRetentionDays) * 24 * time.Hour
	deleted, err := c.store.CleanupOldRunContext(ctx, duration)
	if err != nil {
		observability.Error(ctx, "failed to cleanup old run context",
			zap.Error(err))
		return 0
	}
	if deleted > 0 {
		observability.Info(ctx, "cleaned up old run-scoped context",
			zap.Int64("deleted", deleted),
			zap.Int("retention_days", c.config.Retention.ContextRunRetentionDays))
	}
	return deleted
}

// RunAll runs all garbage collection tasks immediately.
// This is useful for manual/CLI invocations.
func (c *Collector) RunAll(ctx context.Context) *GCResults {
	start := time.Now()
	results := &GCResults{}

	results.ExpiredTTL = c.RunTTLCleanup(ctx)
	results.OldConversations = c.RunConversationCleanup(ctx)
	results.StaleEntities = c.RunEntityPruning(ctx)
	results.OrphanedChunks = c.RunOrphanedChunkCleanup(ctx)
	results.OldContextHistory = c.RunContextHistoryCleanup(ctx)
	results.OldRunContext = c.RunRunContextCleanup(ctx)

	observability.Info(ctx, "manual garbage collection completed",
		zap.Duration("duration", time.Since(start)))

	return results
}

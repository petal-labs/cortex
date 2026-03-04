package entity

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// QueueProcessor processes the entity extraction queue asynchronously.
type QueueProcessor struct {
	storage   storage.Backend
	extractor *Extractor
	resolver  *Resolver
	engine    *Engine
	cfg       *config.EntityConfig

	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
}

// NewQueueProcessor creates a new extraction queue processor.
func NewQueueProcessor(
	store storage.Backend,
	extractor *Extractor,
	resolver *Resolver,
	engine *Engine,
	cfg *config.EntityConfig,
) *QueueProcessor {
	return &QueueProcessor{
		storage:   store,
		extractor: extractor,
		resolver:  resolver,
		engine:    engine,
		cfg:       cfg,
		stopChan:  make(chan struct{}),
	}
}

// Start begins processing the extraction queue in the background.
func (q *QueueProcessor) Start(ctx context.Context) {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.stopChan = make(chan struct{})
	q.mu.Unlock()

	go q.run(ctx)
}

// Stop signals the processor to stop.
func (q *QueueProcessor) Stop() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.running {
		return
	}

	close(q.stopChan)
	q.running = false
}

// IsRunning returns whether the processor is running.
func (q *QueueProcessor) IsRunning() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running
}

// run is the main processing loop.
func (q *QueueProcessor) run(ctx context.Context) {
	interval := q.cfg.ExtractionInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.mu.Lock()
			q.running = false
			q.mu.Unlock()
			return
		case <-q.stopChan:
			return
		case <-ticker.C:
			q.processBatch(ctx)
		}
	}
}

// processBatch dequeues and processes a batch of items.
func (q *QueueProcessor) processBatch(ctx context.Context) {
	batchSize := q.cfg.ExtractionBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	// Dequeue a batch of items
	items, err := q.storage.DequeueExtraction(ctx, batchSize)
	if err != nil {
		if err == storage.ErrNotFound {
			// Queue is empty
			return
		}
		log.Printf("failed to dequeue extraction: %v", err)
		return
	}

	for _, item := range items {
		select {
		case <-ctx.Done():
			return
		case <-q.stopChan:
			return
		default:
		}

		// Process the item
		if err := q.processItem(ctx, item); err != nil {
			q.handleFailure(ctx, item, err)
		} else {
			q.handleSuccess(ctx, item)
		}
	}
}

// processItem extracts entities from a queued item.
func (q *QueueProcessor) processItem(ctx context.Context, item *types.ExtractionQueueItem) error {
	if q.cfg.ExtractionMode == "off" {
		return nil // Extraction disabled
	}

	// Check if we should process this item based on mode
	if !q.shouldProcess(item) {
		return nil
	}

	// Extract entities from content
	result, err := q.extractor.Extract(ctx, item.Content)
	if err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	// Process each extracted entity
	for _, extracted := range result.Entities {
		if err := q.processExtractedEntity(ctx, item.Namespace, item, &extracted); err != nil {
			log.Printf("failed to process entity %s: %v", extracted.Name, err)
			continue // Continue with other entities
		}
	}

	// Process relationships between co-mentioned entities
	for _, rel := range result.Relationships {
		if err := q.processExtractedRelationship(ctx, item.Namespace, &rel); err != nil {
			log.Printf("failed to process relationship: %v", err)
			continue
		}
	}

	return nil
}

// shouldProcess determines if an item should be processed based on extraction mode.
func (q *QueueProcessor) shouldProcess(item *types.ExtractionQueueItem) bool {
	switch q.cfg.ExtractionMode {
	case "off":
		return false
	case "sampled":
		// Use a simple hash-based sampling
		hash := 0
		for _, c := range item.SourceID {
			hash = hash*31 + int(c)
		}
		return float64(hash%100)/100.0 < q.cfg.SampleRate
	case "whitelist":
		// Check if content contains any whitelist keywords
		contentLower := strings.ToLower(item.Content)
		for _, keyword := range q.cfg.WhitelistKeywords {
			if strings.Contains(contentLower, strings.ToLower(keyword)) {
				return true
			}
		}
		return false
	case "full":
		return true
	default:
		return true
	}
}

// processExtractedEntity resolves and upserts an extracted entity.
func (q *QueueProcessor) processExtractedEntity(
	ctx context.Context,
	namespace string,
	item *types.ExtractionQueueItem,
	extracted *ExtractedEntity,
) error {
	// Skip low confidence extractions
	if extracted.Confidence < q.cfg.MinConfidence {
		return nil
	}

	// Resolve to existing entity or identify as new
	resolved, err := q.resolver.ResolveExtracted(ctx, namespace, extracted)
	if err != nil {
		return fmt.Errorf("resolution failed: %w", err)
	}

	var entity *types.Entity

	if resolved.Entity != nil {
		// Update existing entity
		entity = resolved.Entity

		// Merge attributes
		if entity.Attributes == nil {
			entity.Attributes = make(map[string]string)
		}
		for k, v := range extracted.Attributes {
			entity.Attributes[k] = v
		}

		// Update via engine
		updateOpts := UpdateOpts{
			Attributes: entity.Attributes,
		}

		entity, err = q.engine.Update(ctx, namespace, entity.ID, updateOpts)
		if err != nil {
			return fmt.Errorf("failed to update entity: %w", err)
		}

		// Add any new aliases from extraction
		for _, alias := range extracted.Aliases {
			if err := q.engine.AddAlias(ctx, namespace, entity.ID, alias); err != nil {
				// Log but don't fail - alias might already exist
				log.Printf("failed to add alias %s: %v", alias, err)
			}
		}
	} else {
		// Create new entity
		result, err := q.engine.Create(ctx, namespace, extracted.Name, ToEntityType(extracted.Type), &CreateOpts{
			Aliases:    extracted.Aliases,
			Attributes: extracted.Attributes,
		})
		if err != nil {
			return fmt.Errorf("failed to create entity: %w", err)
		}
		entity = result.Entity
	}

	// Record mention
	mentionOpts := &MentionOpts{
		Context: item.Content,
	}

	if err := q.engine.RecordMention(ctx, namespace, entity.ID, item.SourceType, item.SourceID, mentionOpts); err != nil {
		log.Printf("failed to record mention: %v", err)
	}

	return nil
}

// processExtractedRelationship creates or updates a relationship.
func (q *QueueProcessor) processExtractedRelationship(
	ctx context.Context,
	namespace string,
	rel *ExtractedRelationship,
) error {
	// Resolve source and target entities
	sourceResolved, err := q.resolver.Resolve(ctx, namespace, rel.SourceName)
	if err != nil || sourceResolved.Entity == nil {
		return nil // Skip if source not found
	}

	targetResolved, err := q.resolver.Resolve(ctx, namespace, rel.TargetName)
	if err != nil || targetResolved.Entity == nil {
		return nil // Skip if target not found
	}

	// Create relationship
	_, err = q.engine.AddRelationship(ctx, namespace, sourceResolved.Entity.ID, targetResolved.Entity.ID, rel.RelationType, &RelationshipOpts{
		Description: rel.Description,
		Confidence:  rel.Confidence,
	})
	if err != nil {
		return fmt.Errorf("failed to add relationship: %w", err)
	}

	return nil
}

// handleSuccess marks the queue item as completed.
func (q *QueueProcessor) handleSuccess(ctx context.Context, item *types.ExtractionQueueItem) {
	if err := q.storage.CompleteExtraction(ctx, item.ID, "completed"); err != nil {
		log.Printf("failed to complete extraction: %v", err)
	}
}

// handleFailure handles a failed extraction attempt.
func (q *QueueProcessor) handleFailure(ctx context.Context, item *types.ExtractionQueueItem, processErr error) {
	item.Attempts++

	maxAttempts := q.cfg.ExtractionMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	if item.Attempts >= maxAttempts {
		// Max attempts reached
		status := "dead_letter"
		if q.cfg.ExtractionDeadLetterPolicy == "drop" {
			status = "dropped"
		}

		if err := q.storage.CompleteExtraction(ctx, item.ID, status); err != nil {
			log.Printf("failed to mark as dead letter: %v", err)
		}
		return
	}

	// Calculate backoff delay
	delay := q.calculateBackoff(item.Attempts)

	// Re-enqueue with updated attempt count and next retry time
	// For now, just log - the item will be retried on next dequeue
	log.Printf("extraction failed (attempt %d/%d), will retry after %v: %v",
		item.Attempts, maxAttempts, delay, processErr)
}

// calculateBackoff calculates the backoff delay for a retry.
func (q *QueueProcessor) calculateBackoff(attemptCount int) time.Duration {
	baseDelay := time.Second

	if q.cfg.ExtractionBackoff == "fixed" {
		return baseDelay * 5
	}

	// Exponential backoff: delay = baseDelay * 2^attemptCount
	multiplier := math.Pow(2, float64(attemptCount))
	delay := time.Duration(float64(baseDelay) * multiplier)

	// Cap at 5 minutes
	maxDelay := 5 * time.Minute
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

// ProcessSingle processes a single text for entity extraction immediately.
// This is useful for testing or for on-demand extraction.
func (q *QueueProcessor) ProcessSingle(ctx context.Context, namespace, content, sourceType, sourceID string) (*ExtractionResult, error) {
	// Extract entities
	result, err := q.extractor.Extract(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("extraction failed: %w", err)
	}

	// Create a virtual queue item for processing
	item := &types.ExtractionQueueItem{
		Namespace:  namespace,
		SourceType: sourceType,
		SourceID:   sourceID,
		Content:    content,
	}

	// Process each extracted entity
	for _, extracted := range result.Entities {
		if err := q.processExtractedEntity(ctx, namespace, item, &extracted); err != nil {
			log.Printf("failed to process entity %s: %v", extracted.Name, err)
		}
	}

	// Process relationships
	for _, rel := range result.Relationships {
		if err := q.processExtractedRelationship(ctx, namespace, &rel); err != nil {
			log.Printf("failed to process relationship: %v", err)
		}
	}

	return result, nil
}

// ExtractionEnqueuerAdapter adapts the storage backend to the ExtractionEnqueuer
// interface expected by conversation and knowledge engines.
type ExtractionEnqueuerAdapter struct {
	storage storage.Backend
}

// NewExtractionEnqueuerAdapter creates a new adapter that wraps a storage backend.
func NewExtractionEnqueuerAdapter(store storage.Backend) *ExtractionEnqueuerAdapter {
	return &ExtractionEnqueuerAdapter{storage: store}
}

// EnqueueForExtraction queues content for entity extraction.
// Implements the interface expected by conversation.ExtractionEnqueuer and
// knowledge.ExtractionEnqueuer.
func (a *ExtractionEnqueuerAdapter) EnqueueForExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error {
	item := &types.ExtractionQueueItem{
		Namespace:  namespace,
		SourceType: sourceType,
		SourceID:   sourceID,
		Content:    content,
		Status:     "pending",
		CreatedAt:  time.Now().UTC(),
	}
	return a.storage.EnqueueExtraction(ctx, item)
}

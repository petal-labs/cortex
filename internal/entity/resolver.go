package entity

import (
	"context"
	"strings"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Resolver handles name resolution for entity deduplication.
// It implements blocking and fuzzy matching to efficiently resolve
// extracted entity names to existing entities.
type Resolver struct {
	storage        storage.Backend
	fuzzyThreshold float64 // Similarity threshold for fuzzy matching (0-1)
}

// ResolvedEntity represents the result of resolving a name.
type ResolvedEntity struct {
	Entity     *types.Entity // The existing entity (nil if no match)
	Confidence float64       // Resolution confidence (0-1)
	MatchType  string        // "exact", "alias", "fuzzy", or "new"
}

// NewResolver creates a new name resolver.
func NewResolver(store storage.Backend, fuzzyThreshold float64) *Resolver {
	if fuzzyThreshold <= 0 || fuzzyThreshold > 1 {
		fuzzyThreshold = 0.8 // Default threshold
	}

	return &Resolver{
		storage:        store,
		fuzzyThreshold: fuzzyThreshold,
	}
}

// Resolve attempts to match an extracted entity name to an existing entity.
// It uses a multi-phase approach:
// 1. Exact name match
// 2. Alias match
// 3. Blocked candidates with fuzzy matching
func (r *Resolver) Resolve(ctx context.Context, namespace, name string) (*ResolvedEntity, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return &ResolvedEntity{MatchType: "new"}, nil
	}

	// Phase 1: Try exact name match
	entity, err := r.storage.GetEntityByName(ctx, namespace, name)
	if err == nil {
		return &ResolvedEntity{
			Entity:     entity,
			Confidence: 1.0,
			MatchType:  "exact",
		}, nil
	}
	if err != storage.ErrNotFound {
		return nil, err
	}

	// Phase 2: Try alias resolution
	entity, err = r.storage.ResolveAlias(ctx, namespace, name)
	if err == nil {
		return &ResolvedEntity{
			Entity:     entity,
			Confidence: 0.95,
			MatchType:  "alias",
		}, nil
	}
	if err != storage.ErrNotFound {
		return nil, err
	}

	// Phase 3: Get blocked candidates for fuzzy matching
	candidates, err := r.getBlockedCandidates(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	// Phase 4: Fuzzy match against candidates
	if len(candidates) > 0 {
		bestMatch, confidence := r.findBestFuzzyMatch(name, candidates)
		if bestMatch != nil && confidence >= r.fuzzyThreshold {
			return &ResolvedEntity{
				Entity:     bestMatch,
				Confidence: confidence,
				MatchType:  "fuzzy",
			}, nil
		}
	}

	// No match found - this is a new entity
	return &ResolvedEntity{
		MatchType: "new",
	}, nil
}

// getBlockedCandidates retrieves entities that match the blocking criteria.
// Blocking reduces the search space by only considering entities that:
// 1. Share the first 3 characters (case-insensitive)
// 2. Match an initialism pattern
func (r *Resolver) getBlockedCandidates(ctx context.Context, namespace, name string) ([]*types.Entity, error) {
	// Get all entities in namespace for blocking
	// Note: In production, this should be optimized with proper indexing
	entities, _, err := r.storage.ListEntities(ctx, namespace, storage.EntityListOpts{
		Limit: 1000, // Reasonable limit for blocking candidates
	})
	if err != nil {
		return nil, err
	}

	nameLower := strings.ToLower(name)
	namePrefix := ""
	if len(nameLower) >= 3 {
		namePrefix = nameLower[:3]
	}

	initialism := extractInitialism(name)

	var candidates []*types.Entity
	for _, entity := range entities {
		if r.matchesBlockingCriteria(entity, namePrefix, initialism) {
			candidates = append(candidates, entity)
		}
	}

	return candidates, nil
}

// matchesBlockingCriteria checks if an entity passes the blocking filter.
func (r *Resolver) matchesBlockingCriteria(entity *types.Entity, namePrefix, initialism string) bool {
	// Check canonical name prefix
	entityNameLower := strings.ToLower(entity.Name)
	if namePrefix != "" && len(entityNameLower) >= 3 {
		if strings.HasPrefix(entityNameLower, namePrefix) {
			return true
		}
	}

	// Check if name matches entity's initialism
	entityInitialism := extractInitialism(entity.Name)
	if initialism != "" && strings.EqualFold(initialism, entityInitialism) {
		return true
	}

	// Check if extracted name IS an initialism of the entity
	if isInitialismOf(initialism, entity.Name) {
		return true
	}

	return false
}

// findBestFuzzyMatch finds the best fuzzy match among candidates.
func (r *Resolver) findBestFuzzyMatch(name string, candidates []*types.Entity) (*types.Entity, float64) {
	nameLower := strings.ToLower(name)
	var bestEntity *types.Entity
	bestSimilarity := 0.0

	for _, entity := range candidates {
		// Check against canonical name
		similarity := stringSimilarity(nameLower, strings.ToLower(entity.Name))
		if similarity > bestSimilarity {
			bestSimilarity = similarity
			bestEntity = entity
		}
	}

	return bestEntity, bestSimilarity
}

// extractInitialism extracts an initialism from a multi-word name.
// "International Business Machines" -> "IBM"
func extractInitialism(name string) string {
	words := strings.Fields(name)
	if len(words) <= 1 {
		return ""
	}

	// Common words to skip in initialisms (case-insensitive)
	skipWords := map[string]bool{
		"the": true,
		"of":  true,
		"and": true,
		"a":   true,
		"an":  true,
	}

	var initials strings.Builder
	for _, word := range words {
		wordLower := strings.ToLower(word)
		if skipWords[wordLower] {
			continue
		}
		if len(word) > 0 {
			initials.WriteByte(word[0])
		}
	}

	return strings.ToUpper(initials.String())
}

// isInitialismOf checks if 'initialism' could be an abbreviation of 'fullName'.
// "IBM" is an initialism of "International Business Machines"
func isInitialismOf(initialism, fullName string) bool {
	if initialism == "" || fullName == "" {
		return false
	}

	fullInitialism := extractInitialism(fullName)
	return strings.EqualFold(initialism, fullInitialism)
}

// stringSimilarity computes the similarity between two strings using Levenshtein distance.
// Returns a value between 0 (no similarity) and 1 (identical).
func stringSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}

	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := levenshteinDistance(a, b)
	maxLen := max(len(a), len(b))

	return 1.0 - float64(distance)/float64(maxLen)
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	aLen := len(a)
	bLen := len(b)

	// Use two rows instead of full matrix for memory efficiency
	prevRow := make([]int, bLen+1)
	currRow := make([]int, bLen+1)

	// Initialize first row
	for j := range bLen + 1 {
		prevRow[j] = j
	}

	// Fill matrix
	for i := 1; i <= aLen; i++ {
		currRow[0] = i

		for j := 1; j <= bLen; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			currRow[j] = min(
				prevRow[j]+1,      // Deletion
				currRow[j-1]+1,    // Insertion
				prevRow[j-1]+cost, // Substitution
			)
		}

		// Swap rows
		prevRow, currRow = currRow, prevRow
	}

	return prevRow[bLen]
}

// ResolveExtracted resolves an extracted entity and returns the best match or creates info for a new entity.
func (r *Resolver) ResolveExtracted(ctx context.Context, namespace string, extracted *ExtractedEntity) (*ResolvedEntity, error) {
	// First try the canonical name
	result, err := r.Resolve(ctx, namespace, extracted.Name)
	if err != nil {
		return nil, err
	}

	if result.Entity != nil {
		return result, nil
	}

	// Try each alias if canonical name didn't match
	for _, alias := range extracted.Aliases {
		aliasResult, err := r.Resolve(ctx, namespace, alias)
		if err != nil {
			continue // Skip on error, try next alias
		}

		if aliasResult.Entity != nil {
			// Found a match through alias
			return aliasResult, nil
		}
	}

	// No match found
	return &ResolvedEntity{MatchType: "new"}, nil
}

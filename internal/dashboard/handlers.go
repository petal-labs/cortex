package dashboard

import (
	"net/http"
	"strings"

	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/pkg/types"
)

// PageData holds common data for all pages.
type PageData struct {
	Title       string
	ActivePage  string
	Namespace   string
	Content     any
	Error       string
	IsHTMX      bool
}

// handleHome serves the dashboard home page.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()

	// Gather stats for the overview
	var stats struct {
		Collections   int
		Documents     int64
		Conversations int
		Entities      int
	}

	// Get collection count
	if collections, _, err := s.knowledge.ListCollections(ctx, s.namespace, "", 100); err == nil {
		stats.Collections = len(collections)
		for _, col := range collections {
			if colStats, err := s.knowledge.CollectionStats(ctx, s.namespace, col.ID); err == nil {
				stats.Documents += colStats.DocumentCount
			}
		}
	}

	// Get conversation count
	if threads, _, err := s.conversation.ListThreads(ctx, s.namespace, "", 100); err == nil {
		stats.Conversations = len(threads)
	}

	// Get entity count
	if result, err := s.entity.List(ctx, s.namespace, &entity.ListOpts{Limit: 100}); err == nil {
		stats.Entities = result.Count
	}

	data := PageData{
		Title:      "Dashboard",
		ActivePage: "home",
		Namespace:  s.namespace,
		Content:    stats,
	}

	s.render(w, "home.html", data)
}

// handleKnowledge serves the knowledge overview page.
func (s *Server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/knowledge/collections", http.StatusFound)
}

// handleKnowledgeCollections lists all collections.
func (s *Server) handleKnowledgeCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	collections, _, err := s.knowledge.ListCollections(ctx, s.namespace, "", 100)
	if err != nil {
		s.render(w, "knowledge_collections.html", PageData{
			Title:      "Collections",
			ActivePage: "knowledge",
			Namespace:  s.namespace,
			Error:      err.Error(),
		})
		return
	}

	// Get stats for each collection
	type CollectionWithStats struct {
		*types.Collection
		Stats *types.CollectionStats
	}

	collectionsWithStats := make([]CollectionWithStats, 0, len(collections))
	for _, col := range collections {
		cws := CollectionWithStats{Collection: col}
		if stats, err := s.knowledge.CollectionStats(ctx, s.namespace, col.ID); err == nil {
			cws.Stats = stats
		}
		collectionsWithStats = append(collectionsWithStats, cws)
	}

	data := PageData{
		Title:      "Collections",
		ActivePage: "knowledge",
		Namespace:  s.namespace,
		Content:    collectionsWithStats,
		IsHTMX:     isHTMX(r),
	}

	if isHTMX(r) {
		s.renderPartial(w, "knowledge_collections_list", data)
	} else {
		s.render(w, "knowledge_collections.html", data)
	}
}

// handleKnowledgeCollection shows a single collection with its documents.
func (s *Server) handleKnowledgeCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	collectionID := strings.TrimPrefix(r.URL.Path, "/knowledge/collections/")
	if collectionID == "" {
		http.Redirect(w, r, "/knowledge/collections", http.StatusFound)
		return
	}

	collection, err := s.knowledge.GetCollection(ctx, s.namespace, collectionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	stats, _ := s.knowledge.CollectionStats(ctx, s.namespace, collectionID)

	content := struct {
		Collection *types.Collection
		Stats      *types.CollectionStats
	}{
		Collection: collection,
		Stats:      stats,
	}

	data := PageData{
		Title:      collection.Name,
		ActivePage: "knowledge",
		Namespace:  s.namespace,
		Content:    content,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "knowledge_collection.html", data)
}

// handleKnowledgeDocument shows a single document with its chunks.
func (s *Server) handleKnowledgeDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	docID := strings.TrimPrefix(r.URL.Path, "/knowledge/documents/")
	if docID == "" {
		http.Redirect(w, r, "/knowledge/collections", http.StatusFound)
		return
	}

	doc, err := s.knowledge.GetDocument(ctx, s.namespace, docID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := PageData{
		Title:      doc.Title,
		ActivePage: "knowledge",
		Namespace:  s.namespace,
		Content:    doc,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "knowledge_document.html", data)
}

// handleConversations lists all conversation threads.
func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	threads, _, err := s.conversation.ListThreads(ctx, s.namespace, "", 50)
	if err != nil {
		s.render(w, "conversations.html", PageData{
			Title:      "Conversations",
			ActivePage: "conversations",
			Namespace:  s.namespace,
			Error:      err.Error(),
		})
		return
	}

	data := PageData{
		Title:      "Conversations",
		ActivePage: "conversations",
		Namespace:  s.namespace,
		Content:    threads,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "conversations.html", data)
}

// handleConversation shows a single conversation thread.
func (s *Server) handleConversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	threadID := strings.TrimPrefix(r.URL.Path, "/conversations/")
	if threadID == "" {
		http.Redirect(w, r, "/conversations", http.StatusFound)
		return
	}

	thread, err := s.conversation.GetThread(ctx, s.namespace, threadID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Get messages directly from storage
	messages, _, err := s.storage.GetMessages(ctx, s.namespace, threadID, 100, "")
	if err != nil {
		messages = nil
	}

	content := struct {
		Thread   *types.Thread
		Messages []*types.Message
	}{
		Thread:   thread,
		Messages: messages,
	}

	data := PageData{
		Title:      thread.Title,
		ActivePage: "conversations",
		Namespace:  s.namespace,
		Content:    content,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "conversation.html", data)
}

// handleContext shows the context key-value store.
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	// Context listing not yet implemented - show placeholder
	// The context system is key-value based, accessed by specific keys
	data := PageData{
		Title:      "Context",
		ActivePage: "context",
		Namespace:  s.namespace,
		Content:    nil,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "context.html", data)
}

// handleEntities lists all entities.
func (s *Server) handleEntities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	result, err := s.entity.List(ctx, s.namespace, &entity.ListOpts{
		Limit:  50,
		SortBy: types.EntitySortByMentionCount,
	})
	if err != nil {
		s.render(w, "entities.html", PageData{
			Title:      "Entities",
			ActivePage: "entities",
			Namespace:  s.namespace,
			Error:      err.Error(),
		})
		return
	}

	data := PageData{
		Title:      "Entities",
		ActivePage: "entities",
		Namespace:  s.namespace,
		Content:    result.Entities,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "entities.html", data)
}

// handleEntity shows a single entity with its relationships.
func (s *Server) handleEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entityID := strings.TrimPrefix(r.URL.Path, "/entities/")
	if entityID == "" {
		http.Redirect(w, r, "/entities", http.StatusFound)
		return
	}

	ent, err := s.entity.Get(ctx, s.namespace, entityID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	relationships, _ := s.entity.GetRelationships(ctx, s.namespace, entityID, nil)
	mentions, _ := s.entity.GetMentions(ctx, entityID, 10)

	content := struct {
		Entity        *types.Entity
		Relationships []*types.EntityRelationship
		Mentions      []*types.EntityMention
	}{
		Entity:        ent,
		Relationships: relationships,
		Mentions:      mentions,
	}

	data := PageData{
		Title:      ent.Name,
		ActivePage: "entities",
		Namespace:  s.namespace,
		Content:    content,
		IsHTMX:     isHTMX(r),
	}

	s.render(w, "entity.html", data)
}

// handleSearchKnowledge handles knowledge search via htmx.
func (s *Server) handleSearchKnowledge(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")
	if query == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	results, err := s.knowledge.Search(ctx, s.namespace, query, &knowledge.SearchOpts{
		TopK:       10,
		SearchMode: knowledge.SearchModeHybrid,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Query   string
		Results []*types.ChunkResult
	}{
		Query:   query,
		Results: results.Results,
	}

	s.renderPartial(w, "search_results", data)
}

// handleSearchEntities handles entity search via htmx.
func (s *Server) handleSearchEntities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")
	if query == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	results, err := s.entity.Search(ctx, s.namespace, query, &entity.SearchOpts{
		TopK:       10,
		SearchMode: entity.SearchModeHybrid,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := struct {
		Query   string
		Results []*types.EntityResult
	}{
		Query:   query,
		Results: results.Results,
	}

	s.renderPartial(w, "entity_search_results", data)
}

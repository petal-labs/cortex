package dashboard

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/petal-labs/cortex/internal/conversation"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server serves the Cortex web dashboard.
type Server struct {
	storage      storage.Backend
	knowledge    *knowledge.Engine
	conversation *conversation.Engine
	context      *ctxengine.Engine
	entity       *entity.Engine
	templates    *template.Template
	httpServer   *http.Server
	namespace    string
}

// Config holds dashboard server configuration.
type Config struct {
	Addr      string // Listen address (default: ":8080")
	Namespace string // Default namespace to display
}

// New creates a new dashboard server.
func New(
	store storage.Backend,
	know *knowledge.Engine,
	conv *conversation.Engine,
	ctx *ctxengine.Engine,
	ent *entity.Engine,
	cfg *Config,
) (*Server, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}

	// Parse templates with custom functions
	funcMap := template.FuncMap{
		"truncate": truncateString,
		"timeAgo":  timeAgo,
		"json":     toJSON,
		"mul":      func(a, b float64) float64 { return a * b },
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	s := &Server{
		storage:      store,
		knowledge:    know,
		conversation: conv,
		context:      ctx,
		entity:       ent,
		templates:    tmpl,
		namespace:    cfg.Namespace,
	}

	// Set up routes
	mux := http.NewServeMux()

	// Static files
	staticContent, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticContent))))

	// Pages
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/knowledge", s.handleKnowledge)
	mux.HandleFunc("/knowledge/collections", s.handleKnowledgeCollections)
	mux.HandleFunc("/knowledge/collections/", s.handleKnowledgeCollection)
	mux.HandleFunc("/knowledge/documents/", s.handleKnowledgeDocument)
	mux.HandleFunc("/conversations", s.handleConversations)
	mux.HandleFunc("/conversations/", s.handleConversation)
	mux.HandleFunc("/context", s.handleContext)
	mux.HandleFunc("/entities", s.handleEntities)
	mux.HandleFunc("/entities/", s.handleEntity)

	// API endpoints for htmx
	mux.HandleFunc("/api/search/knowledge", s.handleSearchKnowledge)
	mux.HandleFunc("/api/search/entities", s.handleSearchEntities)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// Start starts the dashboard server.
func (s *Server) Start() error {
	log.Printf("Dashboard server starting on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// Addr returns the server's listen address.
func (s *Server) Addr() string {
	return s.httpServer.Addr
}

// render executes a template and writes the result to the response.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// renderPartial renders a partial template (for htmx requests).
func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("Template error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// isHTMX checks if the request is an htmx request.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Template helper functions

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func timeAgo(t time.Time) string {
	duration := time.Since(t)
	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		mins := int(duration.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case duration < 24*time.Hour:
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case duration < 7*24*time.Hour:
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func toJSON(v any) string {
	// Simple JSON representation for debugging
	return fmt.Sprintf("%+v", v)
}

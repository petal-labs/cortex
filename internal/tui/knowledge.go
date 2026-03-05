package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petal-labs/cortex/pkg/types"
)

// KnowledgeModel handles the knowledge browser view
type KnowledgeModel struct {
	parent      *Model
	collections []CollectionWithStats
	selected    int
	loaded      bool

	// Detail view
	showingDetail      bool
	selectedCollection *types.Collection
	collectionStats    *types.CollectionStats
}

// CollectionWithStats holds collection with its stats
type CollectionWithStats struct {
	Collection *types.Collection
	Stats      *types.CollectionStats
}

// NewKnowledgeModel creates a new knowledge model
func NewKnowledgeModel(parent *Model) *KnowledgeModel {
	return &KnowledgeModel{
		parent: parent,
	}
}

// collectionsLoadedMsg is sent when collections are loaded
type collectionsLoadedMsg struct {
	collections []CollectionWithStats
}

// collectionDetailMsg is sent when collection detail is loaded
type collectionDetailMsg struct {
	collection *types.Collection
	stats      *types.CollectionStats
}

// LoadCollections loads all collections
func (m *KnowledgeModel) LoadCollections() tea.Cmd {
	return func() tea.Msg {
		collections, _, err := m.parent.knowledge.ListCollections(
			m.parent.ctx, m.parent.namespace, "", 100,
		)
		if err != nil {
			return errMsg{err: err}
		}

		var result []CollectionWithStats
		for _, col := range collections {
			cws := CollectionWithStats{Collection: col}
			if stats, err := m.parent.knowledge.CollectionStats(
				m.parent.ctx, m.parent.namespace, col.ID,
			); err == nil {
				cws.Stats = stats
			}
			result = append(result, cws)
		}

		return collectionsLoadedMsg{collections: result}
	}
}

// LoadCollectionDetail loads a single collection's details
func (m *KnowledgeModel) LoadCollectionDetail(id string) tea.Cmd {
	return func() tea.Msg {
		collection, err := m.parent.knowledge.GetCollection(
			m.parent.ctx, m.parent.namespace, id,
		)
		if err != nil {
			return errMsg{err: err}
		}

		stats, _ := m.parent.knowledge.CollectionStats(
			m.parent.ctx, m.parent.namespace, id,
		)

		return collectionDetailMsg{collection: collection, stats: stats}
	}
}

// Update handles messages for the knowledge view
func (m *KnowledgeModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case collectionsLoadedMsg:
		m.collections = msg.collections
		m.loaded = true
		m.selected = 0

	case collectionDetailMsg:
		m.selectedCollection = msg.collection
		m.collectionStats = msg.stats
		m.showingDetail = true

	case tea.KeyMsg:
		if m.showingDetail {
			if key.Matches(msg, m.parent.keys.Back) {
				m.showingDetail = false
				m.selectedCollection = nil
				return nil
			}
			return nil
		}

		switch {
		case key.Matches(msg, m.parent.keys.Up):
			if m.selected > 0 {
				m.selected--
			}
		case key.Matches(msg, m.parent.keys.Down):
			if m.selected < len(m.collections)-1 {
				m.selected++
			}
		case key.Matches(msg, m.parent.keys.Enter):
			if len(m.collections) > 0 && m.selected < len(m.collections) {
				col := m.collections[m.selected]
				return m.LoadCollectionDetail(col.Collection.ID)
			}
		case key.Matches(msg, m.parent.keys.Refresh):
			return m.LoadCollections()
		}
	}

	return nil
}

// View renders the knowledge view
func (m *KnowledgeModel) View() string {
	if m.showingDetail {
		return m.viewDetail()
	}

	return m.viewList()
}

func (m *KnowledgeModel) viewList() string {
	var b strings.Builder

	title := CardTitleStyle.Render("Collections")
	b.WriteString(title)
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString("Loading collections...")
		return ContentStyle.Render(b.String())
	}

	if len(m.collections) == 0 {
		b.WriteString(StatLabelStyle.Render("No collections found."))
		b.WriteString("\n")
		b.WriteString(StatLabelStyle.Render("Create a collection using the MCP tools to get started."))
		return ContentStyle.Render(b.String())
	}

	// Header
	header := fmt.Sprintf("%-30s %-8s %-8s", "Name", "Docs", "Chunks")
	b.WriteString(TableHeaderStyle.Render(header))
	b.WriteString("\n")

	// Rows
	for i, col := range m.collections {
		docs := int64(0)
		chunks := int64(0)
		if col.Stats != nil {
			docs = col.Stats.DocumentCount
			chunks = col.Stats.ChunkCount
		}

		name := truncate(col.Collection.Name, 28)
		row := fmt.Sprintf("%-30s %-8d %-8d", name, docs, chunks)

		if i == m.selected {
			b.WriteString(TableRowSelectedStyle.Render("> " + row))
		} else {
			b.WriteString(TableRowStyle.Render("  " + row))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("↑/↓: navigate  enter: view  r: refresh"))

	return ContentStyle.Render(b.String())
}

func (m *KnowledgeModel) viewDetail() string {
	var b strings.Builder

	if m.selectedCollection == nil {
		b.WriteString("Loading...")
		return ContentStyle.Render(b.String())
	}

	col := m.selectedCollection

	// Title
	title := CardTitleStyle.Render(col.Name)
	b.WriteString(title)
	b.WriteString("\n")

	if col.Description != "" {
		b.WriteString(StatLabelStyle.Render(col.Description))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Stats
	if m.collectionStats != nil {
		stats := m.collectionStats

		docBox := CardStyle.Copy().Width(20).Render(
			lipgloss.JoinVertical(lipgloss.Left,
				StatValueStyle.Copy().Foreground(ColorBlue).Render(fmt.Sprintf("%d", stats.DocumentCount)),
				StatLabelStyle.Render("Documents"),
			),
		)

		chunkBox := CardStyle.Copy().Width(20).Render(
			lipgloss.JoinVertical(lipgloss.Left,
				StatValueStyle.Copy().Foreground(ColorGreen).Render(fmt.Sprintf("%d", stats.ChunkCount)),
				StatLabelStyle.Render("Chunks"),
			),
		)

		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, docBox, chunkBox))
		b.WriteString("\n")
	}

	// Details
	detailsTitle := CardTitleStyle.Render("Details")
	b.WriteString(detailsTitle)
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("  ID: %s\n", StatLabelStyle.Render(col.ID)))
	b.WriteString(fmt.Sprintf("  Created: %s\n", StatLabelStyle.Render(timeAgo(col.CreatedAt))))
	b.WriteString(fmt.Sprintf("  Chunk Strategy: %s\n", StatLabelStyle.Render(col.ChunkConfig.Strategy)))

	b.WriteString("\n")
	b.WriteString(HelpStyle.Render("esc: back"))

	return ContentStyle.Render(b.String())
}

// Helper functions
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

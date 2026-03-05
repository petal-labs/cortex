package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petal-labs/cortex/internal/entity"
)

// DashboardStats holds the dashboard overview statistics
type DashboardStats struct {
	Collections   int
	Documents     int64
	Conversations int
	Entities      int
}

// DashboardModel handles the dashboard view
type DashboardModel struct {
	parent *Model
	stats  DashboardStats
	loaded bool
}

// NewDashboardModel creates a new dashboard model
func NewDashboardModel(parent *Model) *DashboardModel {
	return &DashboardModel{
		parent: parent,
	}
}

// statsLoadedMsg is sent when stats are loaded
type statsLoadedMsg struct {
	stats DashboardStats
}

// LoadStats loads the dashboard statistics
func (m *DashboardModel) LoadStats() tea.Cmd {
	return func() tea.Msg {
		var stats DashboardStats

		// Get collection count and document count
		if collections, _, err := m.parent.knowledge.ListCollections(
			m.parent.ctx, m.parent.namespace, "", 100,
		); err == nil {
			stats.Collections = len(collections)
			for _, col := range collections {
				if colStats, err := m.parent.knowledge.CollectionStats(
					m.parent.ctx, m.parent.namespace, col.ID,
				); err == nil {
					stats.Documents += colStats.DocumentCount
				}
			}
		}

		// Get conversation count
		if threads, _, err := m.parent.conversation.ListThreads(
			m.parent.ctx, m.parent.namespace, "", 100,
		); err == nil {
			stats.Conversations = len(threads)
		}

		// Get entity count
		if result, err := m.parent.entity.List(
			m.parent.ctx, m.parent.namespace, &entity.ListOpts{Limit: 100},
		); err == nil {
			stats.Entities = result.Count
		}

		return statsLoadedMsg{stats: stats}
	}
}

// Update handles messages for the dashboard view
func (m *DashboardModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case statsLoadedMsg:
		m.stats = msg.stats
		m.loaded = true
	}
	return nil
}

// View renders the dashboard view
func (m *DashboardModel) View() string {
	var b strings.Builder

	// Title
	title := CardTitleStyle.Render("Dashboard Overview")
	b.WriteString(title)
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString("Loading statistics...")
		return ContentStyle.Render(b.String())
	}

	// Stats grid
	stats := []struct {
		label string
		value interface{}
		color lipgloss.Color
	}{
		{"Collections", m.stats.Collections, ColorBlue},
		{"Documents", m.stats.Documents, ColorGreen},
		{"Conversations", m.stats.Conversations, ColorPurple},
		{"Entities", m.stats.Entities, ColorOrange},
	}

	// Render stats in a grid
	var statBoxes []string
	for _, stat := range stats {
		valueStyle := StatValueStyle.Copy().Foreground(stat.color)
		value := valueStyle.Render(fmt.Sprintf("%v", stat.value))
		label := StatLabelStyle.Render(stat.label)

		box := CardStyle.Copy().Width(20).Render(
			lipgloss.JoinVertical(lipgloss.Left, value, label),
		)
		statBoxes = append(statBoxes, box)
	}

	// Join stat boxes horizontally
	statsRow := lipgloss.JoinHorizontal(lipgloss.Top, statBoxes...)
	b.WriteString(statsRow)
	b.WriteString("\n\n")

	// Quick actions
	actionsTitle := CardTitleStyle.Render("Quick Navigation")
	b.WriteString(actionsTitle)
	b.WriteString("\n\n")

	actions := []string{
		"  [2] Knowledge  - Browse and search document collections",
		"  [3] Conversations - View conversation threads and messages",
		"  [4] Context    - Inspect stored context key-value pairs",
		"  [5] Entities   - Explore extracted entities and relationships",
	}

	for _, action := range actions {
		b.WriteString(StatLabelStyle.Render(action))
		b.WriteString("\n")
	}

	return ContentStyle.Render(b.String())
}

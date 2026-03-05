package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/pkg/types"
)

// EntityModel handles the entities view
type EntityModel struct {
	parent   *Model
	entities []*types.Entity
	selected int
	loaded   bool

	// Detail view
	showingDetail  bool
	selectedEntity *types.Entity
	relationships  []*types.EntityRelationship
	mentions       []*types.EntityMention
}

// NewEntityModel creates a new entity model
func NewEntityModel(parent *Model) *EntityModel {
	return &EntityModel{
		parent: parent,
	}
}

// entitiesLoadedMsg is sent when entities are loaded
type entitiesLoadedMsg struct {
	entities []*types.Entity
}

// entityDetailMsg is sent when entity detail is loaded
type entityDetailMsg struct {
	entity        *types.Entity
	relationships []*types.EntityRelationship
	mentions      []*types.EntityMention
}

// LoadEntities loads all entities
func (m *EntityModel) LoadEntities() tea.Cmd {
	return func() tea.Msg {
		result, err := m.parent.entity.List(
			m.parent.ctx, m.parent.namespace, &entity.ListOpts{
				Limit:  50,
				SortBy: types.EntitySortByMentionCount,
			},
		)
		if err != nil {
			return errMsg{err: err}
		}

		return entitiesLoadedMsg{entities: result.Entities}
	}
}

// LoadEntityDetail loads a single entity with relationships and mentions
func (m *EntityModel) LoadEntityDetail(entityID string) tea.Cmd {
	return func() tea.Msg {
		ent, err := m.parent.entity.Get(
			m.parent.ctx, m.parent.namespace, entityID,
		)
		if err != nil {
			return errMsg{err: err}
		}

		relationships, _ := m.parent.entity.GetRelationships(
			m.parent.ctx, m.parent.namespace, entityID, nil,
		)

		mentions, _ := m.parent.entity.GetMentions(
			m.parent.ctx, entityID, 10,
		)

		return entityDetailMsg{
			entity:        ent,
			relationships: relationships,
			mentions:      mentions,
		}
	}
}

// Update handles messages for the entity view
func (m *EntityModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case entitiesLoadedMsg:
		m.entities = msg.entities
		m.loaded = true
		m.selected = 0

	case entityDetailMsg:
		m.selectedEntity = msg.entity
		m.relationships = msg.relationships
		m.mentions = msg.mentions
		m.showingDetail = true

	case tea.KeyMsg:
		if m.showingDetail {
			if key.Matches(msg, m.parent.keys.Back) {
				m.showingDetail = false
				m.selectedEntity = nil
				m.relationships = nil
				m.mentions = nil
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
			if m.selected < len(m.entities)-1 {
				m.selected++
			}
		case key.Matches(msg, m.parent.keys.Enter):
			if len(m.entities) > 0 && m.selected < len(m.entities) {
				ent := m.entities[m.selected]
				return m.LoadEntityDetail(ent.ID)
			}
		case key.Matches(msg, m.parent.keys.Refresh):
			return m.LoadEntities()
		}
	}

	return nil
}

// View renders the entity view
func (m *EntityModel) View() string {
	if m.showingDetail {
		return m.viewDetail()
	}

	return m.viewList()
}

func (m *EntityModel) viewList() string {
	var b strings.Builder

	title := CardTitleStyle.Render("Entities")
	b.WriteString(title)
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString("Loading entities...")
		return ContentStyle.Render(b.String())
	}

	if len(m.entities) == 0 {
		b.WriteString(StatLabelStyle.Render("No entities found."))
		b.WriteString("\n")
		b.WriteString(StatLabelStyle.Render("Entities are extracted from conversations and documents."))
		return ContentStyle.Render(b.String())
	}

	// Header
	header := fmt.Sprintf("%-30s %-15s %-10s %-15s", "Name", "Type", "Mentions", "Updated")
	b.WriteString(TableHeaderStyle.Render(header))
	b.WriteString("\n")

	// Rows
	for i, ent := range m.entities {
		name := truncate(ent.Name, 28)
		entType := truncate(string(ent.Type), 13)
		mentions := fmt.Sprintf("%d", ent.MentionCount)
		updated := timeAgo(ent.LastSeenAt)

		row := fmt.Sprintf("%-30s %-15s %-10s %-15s", name, entType, mentions, updated)

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

func (m *EntityModel) viewDetail() string {
	var b strings.Builder

	if m.selectedEntity == nil {
		b.WriteString("Loading...")
		return ContentStyle.Render(b.String())
	}

	ent := m.selectedEntity

	// Title
	b.WriteString(CardTitleStyle.Render(ent.Name))
	b.WriteString("\n")

	// Type badge
	typeStyle := BadgeStyle
	switch ent.Type {
	case types.EntityTypePerson:
		typeStyle = BadgeBlueStyle
	case types.EntityTypeOrganization:
		typeStyle = BadgeGreenStyle
	case types.EntityTypeLocation:
		typeStyle = BadgeOrangeStyle
	case types.EntityTypeConcept:
		typeStyle = BadgePurpleStyle
	}
	b.WriteString(typeStyle.Render(string(ent.Type)))
	b.WriteString("\n")

	if ent.Summary != "" {
		b.WriteString(StatLabelStyle.Render(ent.Summary))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	// Stats
	mentionBox := CardStyle.Width(20).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			StatValueStyle.Foreground(ColorBlue).Render(fmt.Sprintf("%d", ent.MentionCount)),
			StatLabelStyle.Render("Mentions"),
		),
	)

	relBox := CardStyle.Width(20).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			StatValueStyle.Foreground(ColorPurple).Render(fmt.Sprintf("%d", len(m.relationships))),
			StatLabelStyle.Render("Relationships"),
		),
	)

	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, mentionBox, relBox))
	b.WriteString("\n")

	// Relationships
	if len(m.relationships) > 0 {
		b.WriteString(CardTitleStyle.Render("Relationships"))
		b.WriteString("\n\n")

		for _, rel := range m.relationships {
			b.WriteString("  ")
			b.WriteString(BadgePurpleStyle.Render(rel.RelationType))
			b.WriteString(" -> ")
			b.WriteString(rel.TargetEntityID)
			b.WriteString(fmt.Sprintf(" (%.0f%%)", rel.Confidence*100))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Mentions
	if len(m.mentions) > 0 {
		b.WriteString(CardTitleStyle.Render("Recent Mentions"))
		b.WriteString("\n\n")

		for _, mention := range m.mentions {
			b.WriteString("  ")
			b.WriteString(StatLabelStyle.Render(fmt.Sprintf("%s - %s", mention.SourceType, timeAgo(mention.CreatedAt))))
			b.WriteString("\n")
			if mention.Context != "" {
				context := truncate(mention.Context, 100)
				b.WriteString("  ")
				b.WriteString(context)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	b.WriteString(HelpStyle.Render("esc: back"))

	return ContentStyle.Render(b.String())
}

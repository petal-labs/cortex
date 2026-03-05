package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petal-labs/cortex/internal/conversation"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage"
)

// View represents different views in the TUI
type View int

const (
	ViewDashboard View = iota
	ViewKnowledge
	ViewConversations
	ViewContext
	ViewEntities
)

// KeyMap defines the key bindings for the TUI
type KeyMap struct {
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Enter    key.Binding
	Back     key.Binding
	Tab      key.Binding
	Search   key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Quit     key.Binding
	Nav1     key.Binding
	Nav2     key.Binding
	Nav3     key.Binding
	Nav4     key.Binding
	Nav5     key.Binding
}

// DefaultKeyMap returns the default key bindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Left: key.NewBinding(
			key.WithKeys("left", "h"),
			key.WithHelp("←/h", "left"),
		),
		Right: key.NewBinding(
			key.WithKeys("right", "l"),
			key.WithHelp("→/l", "right"),
		),
		Enter: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc", "backspace"),
			key.WithHelp("esc", "back"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next section"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Nav1: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "dashboard"),
		),
		Nav2: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "knowledge"),
		),
		Nav3: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "conversations"),
		),
		Nav4: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "context"),
		),
		Nav5: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "entities"),
		),
	}
}

// Model is the main TUI application model
type Model struct {
	// Dependencies
	storage      storage.Backend
	knowledge    *knowledge.Engine
	conversation *conversation.Engine
	context      *ctxengine.Engine
	entity       *entity.Engine
	namespace    string
	ctx          context.Context

	// UI state
	currentView    View
	width          int
	height         int
	keys           KeyMap
	showHelp       bool
	loading        bool
	spinner        spinner.Model
	err            error

	// View-specific state
	dashboardModel *DashboardModel
	knowledgeModel *KnowledgeModel
	conversationModel *ConversationModel
	entityModel    *EntityModel
}

// Config holds TUI configuration
type Config struct {
	Namespace string
}

// New creates a new TUI model
func New(
	ctx context.Context,
	store storage.Backend,
	know *knowledge.Engine,
	conv *conversation.Engine,
	ctxEng *ctxengine.Engine,
	ent *entity.Engine,
	cfg *Config,
) *Model {
	if cfg == nil {
		cfg = &Config{Namespace: "default"}
	}

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = SpinnerStyle

	m := &Model{
		storage:      store,
		knowledge:    know,
		conversation: conv,
		context:      ctxEng,
		entity:       ent,
		namespace:    cfg.Namespace,
		ctx:          ctx,
		currentView:  ViewDashboard,
		keys:         DefaultKeyMap(),
		spinner:      s,
	}

	// Initialize view models
	m.dashboardModel = NewDashboardModel(m)
	m.knowledgeModel = NewKnowledgeModel(m)
	m.conversationModel = NewConversationModel(m)
	m.entityModel = NewEntityModel(m)

	return m
}

// Init implements tea.Model
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.loadDashboard(),
	)
}

// Update implements tea.Model
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Global key handlers
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil
		case key.Matches(msg, m.keys.Nav1):
			return m.switchView(ViewDashboard)
		case key.Matches(msg, m.keys.Nav2):
			return m.switchView(ViewKnowledge)
		case key.Matches(msg, m.keys.Nav3):
			return m.switchView(ViewConversations)
		case key.Matches(msg, m.keys.Nav4):
			return m.switchView(ViewContext)
		case key.Matches(msg, m.keys.Nav5):
			return m.switchView(ViewEntities)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case loadingMsg:
		m.loading = msg.loading
		return m, nil
	}

	// Delegate to current view
	switch m.currentView {
	case ViewDashboard:
		cmd := m.dashboardModel.Update(msg)
		cmds = append(cmds, cmd)
	case ViewKnowledge:
		cmd := m.knowledgeModel.Update(msg)
		cmds = append(cmds, cmd)
	case ViewConversations:
		cmd := m.conversationModel.Update(msg)
		cmds = append(cmds, cmd)
	case ViewEntities:
		cmd := m.entityModel.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

// View implements tea.Model
func (m *Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Build the layout
	sidebar := m.renderSidebar()
	content := m.renderContent()
	statusBar := m.renderStatusBar()

	// Calculate dimensions
	sidebarWidth := 24
	contentWidth := m.width - sidebarWidth - 2

	// Style content area
	contentStyled := lipgloss.NewStyle().
		Width(contentWidth).
		Height(m.height - 2).
		Render(content)

	// Join sidebar and content horizontally
	mainArea := lipgloss.JoinHorizontal(
		lipgloss.Top,
		sidebar,
		contentStyled,
	)

	// Join main area and status bar vertically
	return lipgloss.JoinVertical(
		lipgloss.Left,
		mainArea,
		statusBar,
	)
}

func (m *Model) renderSidebar() string {
	var items []string

	navItems := []struct {
		view  View
		label string
		key   string
	}{
		{ViewDashboard, "Dashboard", "1"},
		{ViewKnowledge, "Knowledge", "2"},
		{ViewConversations, "Conversations", "3"},
		{ViewContext, "Context", "4"},
		{ViewEntities, "Entities", "5"},
	}

	// Header
	header := TitleStyle.Render("  Cortex")
	items = append(items, header)
	items = append(items, "")

	for _, nav := range navItems {
		label := fmt.Sprintf(" %s %s", nav.key, nav.label)
		if m.currentView == nav.view {
			items = append(items, NavItemActiveStyle.Render(label))
		} else {
			items = append(items, NavItemStyle.Render(label))
		}
	}

	// Namespace footer
	items = append(items, "")
	for i := 0; i < m.height-len(items)-4; i++ {
		items = append(items, "")
	}
	nsLabel := StatLabelStyle.Render(fmt.Sprintf("  ns: %s", m.namespace))
	items = append(items, nsLabel)

	content := strings.Join(items, "\n")
	return SidebarStyle.Height(m.height - 2).Render(content)
}

func (m *Model) renderContent() string {
	if m.loading {
		return ContentStyle.Render(
			fmt.Sprintf("%s Loading...", m.spinner.View()),
		)
	}

	if m.err != nil {
		return ContentStyle.Render(
			ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err)),
		)
	}

	switch m.currentView {
	case ViewDashboard:
		return m.dashboardModel.View()
	case ViewKnowledge:
		return m.knowledgeModel.View()
	case ViewConversations:
		return m.conversationModel.View()
	case ViewContext:
		return m.renderContextView()
	case ViewEntities:
		return m.entityModel.View()
	default:
		return ContentStyle.Render("Unknown view")
	}
}

func (m *Model) renderContextView() string {
	var b strings.Builder

	title := CardTitleStyle.Render("Context Store")
	b.WriteString(title)
	b.WriteString("\n\n")

	b.WriteString(StatLabelStyle.Render("The context store holds key-value pairs for workflow state."))
	b.WriteString("\n")
	b.WriteString(StatLabelStyle.Render("Use the CLI or MCP tools to get/set values by key."))
	b.WriteString("\n\n")

	b.WriteString(StatLabelStyle.Render("Available operations:"))
	b.WriteString("\n")
	b.WriteString("  • context_get - Retrieve a value by key\n")
	b.WriteString("  • context_set - Store a value with optional TTL\n")
	b.WriteString("  • context_merge - Merge values with strategy\n")
	b.WriteString("  • context_list - List keys with prefix filter\n")

	return ContentStyle.Render(b.String())
}

func (m *Model) renderStatusBar() string {
	help := "  q: quit  ?: help  1-5: navigate  r: refresh"
	if m.showHelp {
		help = "  ↑/k: up  ↓/j: down  enter: select  esc: back  /: search"
	}

	return StatusBarStyle.
		Width(m.width).
		Render(help)
}

func (m *Model) switchView(view View) (tea.Model, tea.Cmd) {
	m.currentView = view
	m.err = nil

	switch view {
	case ViewDashboard:
		return m, m.loadDashboard()
	case ViewKnowledge:
		return m, m.knowledgeModel.LoadCollections()
	case ViewConversations:
		return m, m.conversationModel.LoadThreads()
	case ViewEntities:
		return m, m.entityModel.LoadEntities()
	}

	return m, nil
}

// Message types for async operations
type errMsg struct{ err error }
type loadingMsg struct{ loading bool }

func (m *Model) loadDashboard() tea.Cmd {
	return m.dashboardModel.LoadStats()
}

// Run starts the TUI application
func Run(
	ctx context.Context,
	store storage.Backend,
	know *knowledge.Engine,
	conv *conversation.Engine,
	ctxEng *ctxengine.Engine,
	ent *entity.Engine,
	cfg *Config,
) error {
	model := New(ctx, store, know, conv, ctxEng, ent, cfg)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

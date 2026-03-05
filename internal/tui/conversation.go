package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/petal-labs/cortex/pkg/types"
)

// ConversationModel handles the conversations view
type ConversationModel struct {
	parent   *Model
	threads  []*types.Thread
	selected int
	loaded   bool

	// Detail view
	showingDetail   bool
	selectedThread  *types.Thread
	messages        []*types.Message
}

// NewConversationModel creates a new conversation model
func NewConversationModel(parent *Model) *ConversationModel {
	return &ConversationModel{
		parent: parent,
	}
}

// threadsLoadedMsg is sent when threads are loaded
type threadsLoadedMsg struct {
	threads []*types.Thread
}

// threadDetailMsg is sent when thread detail is loaded
type threadDetailMsg struct {
	thread   *types.Thread
	messages []*types.Message
}

// LoadThreads loads all conversation threads
func (m *ConversationModel) LoadThreads() tea.Cmd {
	return func() tea.Msg {
		threads, _, err := m.parent.conversation.ListThreads(
			m.parent.ctx, m.parent.namespace, "", 50,
		)
		if err != nil {
			return errMsg{err: err}
		}

		return threadsLoadedMsg{threads: threads}
	}
}

// LoadThreadDetail loads a single thread with messages
func (m *ConversationModel) LoadThreadDetail(threadID string) tea.Cmd {
	return func() tea.Msg {
		thread, err := m.parent.conversation.GetThread(
			m.parent.ctx, m.parent.namespace, threadID,
		)
		if err != nil {
			return errMsg{err: err}
		}

		messages, _, err := m.parent.storage.GetMessages(
			m.parent.ctx, m.parent.namespace, threadID, 100, "",
		)
		if err != nil {
			messages = nil
		}

		return threadDetailMsg{thread: thread, messages: messages}
	}
}

// Update handles messages for the conversation view
func (m *ConversationModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case threadsLoadedMsg:
		m.threads = msg.threads
		m.loaded = true
		m.selected = 0

	case threadDetailMsg:
		m.selectedThread = msg.thread
		m.messages = msg.messages
		m.showingDetail = true

	case tea.KeyMsg:
		if m.showingDetail {
			if key.Matches(msg, m.parent.keys.Back) {
				m.showingDetail = false
				m.selectedThread = nil
				m.messages = nil
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
			if m.selected < len(m.threads)-1 {
				m.selected++
			}
		case key.Matches(msg, m.parent.keys.Enter):
			if len(m.threads) > 0 && m.selected < len(m.threads) {
				thread := m.threads[m.selected]
				return m.LoadThreadDetail(thread.ID)
			}
		case key.Matches(msg, m.parent.keys.Refresh):
			return m.LoadThreads()
		}
	}

	return nil
}

// View renders the conversation view
func (m *ConversationModel) View() string {
	if m.showingDetail {
		return m.viewDetail()
	}

	return m.viewList()
}

func (m *ConversationModel) viewList() string {
	var b strings.Builder

	title := CardTitleStyle.Render("Conversations")
	b.WriteString(title)
	b.WriteString("\n\n")

	if !m.loaded {
		b.WriteString("Loading conversations...")
		return ContentStyle.Render(b.String())
	}

	if len(m.threads) == 0 {
		b.WriteString(StatLabelStyle.Render("No conversations found."))
		b.WriteString("\n")
		b.WriteString(StatLabelStyle.Render("Start a conversation using the MCP tools to see it here."))
		return ContentStyle.Render(b.String())
	}

	// Header
	header := fmt.Sprintf("%-50s %-15s", "Title", "Updated")
	b.WriteString(TableHeaderStyle.Render(header))
	b.WriteString("\n")

	// Rows
	for i, thread := range m.threads {
		title := thread.Title
		if title == "" {
			title = "Untitled"
		}
		title = truncate(title, 48)

		updated := timeAgo(thread.UpdatedAt)

		row := fmt.Sprintf("%-50s %-15s", title, updated)

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

func (m *ConversationModel) viewDetail() string {
	var b strings.Builder

	if m.selectedThread == nil {
		b.WriteString("Loading...")
		return ContentStyle.Render(b.String())
	}

	thread := m.selectedThread

	// Title
	title := thread.Title
	if title == "" {
		title = "Untitled Conversation"
	}
	b.WriteString(CardTitleStyle.Render(title))
	b.WriteString("\n")

	// Thread info
	b.WriteString(StatLabelStyle.Render(fmt.Sprintf("Updated %s", timeAgo(thread.UpdatedAt))))
	b.WriteString("\n\n")

	// Messages
	msgTitle := CardTitleStyle.Render("Messages")
	b.WriteString(msgTitle)
	b.WriteString("\n\n")

	if len(m.messages) == 0 {
		b.WriteString(StatLabelStyle.Render("No messages in this conversation."))
	} else {
		for _, msg := range m.messages {
			roleStyle := BadgeStyle
			switch msg.Role {
			case "user":
				roleStyle = BadgeGreenStyle
			case "assistant":
				roleStyle = BadgeBlueStyle
			case "system":
				roleStyle = BadgePurpleStyle
			}

			b.WriteString(roleStyle.Render(msg.Role))
			b.WriteString(" ")
			b.WriteString(StatLabelStyle.Render(timeAgo(msg.CreatedAt)))
			b.WriteString("\n")

			content := truncate(msg.Content, 200)
			contentStyle := lipgloss.NewStyle().
				Foreground(ColorTextPrimary).
				PaddingLeft(2)
			b.WriteString(contentStyle.Render(content))
			b.WriteString("\n\n")
		}
	}

	b.WriteString(HelpStyle.Render("esc: back"))

	return ContentStyle.Render(b.String())
}

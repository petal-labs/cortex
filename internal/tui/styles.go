package tui

import "github.com/charmbracelet/lipgloss"

// Colors matching the web dashboard dark theme
var (
	ColorBgPrimary   = lipgloss.Color("#0d1117")
	ColorBgSecondary = lipgloss.Color("#161b22")
	ColorBgTertiary  = lipgloss.Color("#21262d")
	ColorBorder      = lipgloss.Color("#30363d")
	ColorTextPrimary = lipgloss.Color("#c9d1d9")
	ColorTextMuted   = lipgloss.Color("#6e7681")
	ColorBlue        = lipgloss.Color("#58a6ff")
	ColorGreen       = lipgloss.Color("#3fb950")
	ColorPurple      = lipgloss.Color("#a371f7")
	ColorOrange      = lipgloss.Color("#d29922")
	ColorRed         = lipgloss.Color("#f85149")
)

// Styles for the TUI components
var (
	// Base styles
	BaseStyle = lipgloss.NewStyle().
			Background(ColorBgPrimary).
			Foreground(ColorTextPrimary)

	// Title bar
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorTextPrimary).
			Background(ColorBgSecondary).
			Padding(0, 1)

	// Sidebar navigation
	SidebarStyle = lipgloss.NewStyle().
			Width(24).
			Background(ColorBgSecondary).
			BorderStyle(lipgloss.NormalBorder()).
			BorderRight(true).
			BorderForeground(ColorBorder)

	NavItemStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(ColorTextMuted)

	NavItemActiveStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Foreground(ColorTextPrimary).
				Background(ColorBgTertiary).
				Bold(true)

	// Main content area
	ContentStyle = lipgloss.NewStyle().
			Padding(1, 2)

	// Cards/boxes
	CardStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2).
			MarginBottom(1)

	CardTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorTextPrimary).
			MarginBottom(1)

	// Stats
	StatValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBlue)

	StatLabelStyle = lipgloss.NewStyle().
			Foreground(ColorTextMuted)

	// Tables
	TableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorTextMuted).
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderForeground(ColorBorder)

	TableRowStyle = lipgloss.NewStyle().
			Foreground(ColorTextPrimary)

	TableRowSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorTextPrimary).
				Background(ColorBgTertiary)

	// Badges
	BadgeStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(ColorBgTertiary).
			Foreground(ColorTextMuted)

	BadgeBlueStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(lipgloss.Color("#1a3a5c")).
			Foreground(ColorBlue)

	BadgeGreenStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Background(lipgloss.Color("#1a3d2a")).
			Foreground(ColorGreen)

	BadgePurpleStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(lipgloss.Color("#2d1f4a")).
				Foreground(ColorPurple)

	BadgeOrangeStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Background(lipgloss.Color("#3d2e1a")).
				Foreground(ColorOrange)

	// Help text
	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			MarginTop(1)

	// Status bar
	StatusBarStyle = lipgloss.NewStyle().
			Background(ColorBgSecondary).
			Foreground(ColorTextMuted).
			Padding(0, 1)

	// Error/warning messages
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorRed)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorGreen)

	// Input fields
	InputStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	InputFocusedStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.RoundedBorder()).
				BorderForeground(ColorBlue).
				Padding(0, 1)

	// List items
	ListItemStyle = lipgloss.NewStyle().
			Foreground(ColorTextPrimary)

	ListItemSelectedStyle = lipgloss.NewStyle().
				Foreground(ColorBlue).
				Bold(true)

	// Spinner
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(ColorBlue)
)

// Stat colors for different stat types
func StatColor(index int) lipgloss.Color {
	colors := []lipgloss.Color{ColorBlue, ColorGreen, ColorPurple, ColorOrange}
	return colors[index%len(colors)]
}

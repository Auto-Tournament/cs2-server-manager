package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))

	headerBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("63")).
				Padding(0, 1)

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	checkboxStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212"))

	mainStyle = lipgloss.NewStyle().
			MarginLeft(2)

	menuItemStyle = lipgloss.NewStyle().
			PaddingLeft(2).
			Foreground(lipgloss.Color("250"))

	// warningStyle is used for inline warning messages (e.g. "requires sudo")
	// shown between the navbar and the options list.
	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	menuSelectedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("63")).
				Bold(true).
				PaddingLeft(2)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("60")).
			Padding(0, 1)

	outputTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("69"))

	outputBodyStyle = lipgloss.NewStyle().
			MarginTop(1)

	versionBannerStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("220")).
				Bold(true).
				Padding(0, 1)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241")).
				Padding(0, 1)

	tabBarStyle = lipgloss.NewStyle().
			MarginBottom(1)
)

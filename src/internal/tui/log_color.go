package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	logOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)
	logInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)
	logWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")).
			Bold(true)
)

// colorizeLog applies simple severity-based coloring to log-like text used in
// the TUI (lastOutput/detailContent) without modifying the underlying log
// files on disk. It looks for common prefixes like [OK], [i], [*], [!] at the
// start of each line and wraps just those tokens with lipgloss styles.
func colorizeLog(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "[OK]"):
			lines[i] = strings.Replace(line, "[OK]", logOKStyle.Render("[OK]"), 1)
		case strings.HasPrefix(trimmed, "[✓]"):
			lines[i] = strings.Replace(line, "[✓]", logOKStyle.Render("[✓]"), 1)
		case strings.HasPrefix(trimmed, "[i]"):
			lines[i] = strings.Replace(line, "[i]", logInfoStyle.Render("[i]"), 1)
		case strings.HasPrefix(trimmed, "[*]"):
			lines[i] = strings.Replace(line, "[*]", logInfoStyle.Render("[*]"), 1)
		case strings.HasPrefix(trimmed, "[!]"):
			lines[i] = strings.Replace(line, "[!]", logWarnStyle.Render("[!]"), 1)
		}
	}
	return strings.Join(lines, "\n")
}

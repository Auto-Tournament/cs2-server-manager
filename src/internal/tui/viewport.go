package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

func (m model) viewViewport() string {
	var b strings.Builder

	// If we know the terminal height, resize the viewport so it uses most of
	// the available vertical space instead of a fixed height.
	if m.height > 0 {
		h := m.height - 8
		if h < 8 {
			h = 8
		}
		if m.vp.Height != h {
			m.vp.Height = h
		}
	}

	title := strings.TrimSpace(m.vpTitle)
	if title == "" {
		title = "Details"
	}

	header := headerBorderStyle.Render(titleStyle.Render(title)) +
		"\n" +
		headerBorderStyle.Render("Scroll with ↑/↓, PgUp/PgDn • Enter/q/Esc to return")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.vp.View())
	fmt.Fprintln(&b)

	statusText := m.status
	fmt.Fprintln(&b, statusBarStyle.Render(statusText))

	return b.String()
}

// viewActionResult renders a generic "detail" page for an action result.
// It behaves like a separate page on a website: the user runs an action,
// sees the result on this screen, then presses Enter (or q/Esc) to return.
func (m model) viewActionResult() string {
	var b strings.Builder

	title := m.detailTitle
	if strings.TrimSpace(title) == "" {
		title = "Action result"
	}

	// For generic action results, keep the header simple (title only) and show
	// the "Press Enter to continue." hint in the status bar to avoid
	// duplicating the message on screen.
	header := headerBorderStyle.Render(titleStyle.Render(title))

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	if strings.TrimSpace(m.detailContent) != "" {
		fmt.Fprintln(&b, colorizeLog(m.detailContent))
		fmt.Fprintln(&b)
	}

	statusText := "Press Enter to continue."
	fmt.Fprintln(&b, statusBarStyle.Render(statusText))

	return b.String()
}

// viewPublicIP renders a simple, focused screen showing the last-resolved
// public IP address. Users dismiss this screen with Enter (or q/Esc), which
// returns them to the main menu.
func (m model) viewPublicIP() string {
	var b strings.Builder

	// Simple header: just the title, without extra instructions (the footer
	// already shows "Press Enter to return.").
	header := headerBorderStyle.Render(titleStyle.Render("Public IP"))
	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	if strings.TrimSpace(m.publicIP) == "" {
		fmt.Fprintln(&b, "Public IP: (not available)")
	} else {
		fmt.Fprintln(&b, m.publicIP)
	}
	fmt.Fprintln(&b)

	statusText := "Press Enter to continue."
	fmt.Fprintln(&b, statusBarStyle.Render(statusText))

	return b.String()
}

func (m model) viewLogsPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Server logs (scrollable)")) +
		"\n" +
		headerBorderStyle.Render("Enter server number to view logs")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Server number:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, "Press Enter to load logs, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateLogsPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.view = viewMain

		return m, nil
	case "ctrl+c", "q":
		return m, tea.Quit
	case "enter":
		value := strings.TrimSpace(m.wizard.input.Value())
		if value == "" {
			m.wizard.errMsg = "Please enter a server number."
			return m, nil
		}
		if _, err := strconv.Atoi(value); err != nil {
			m.wizard.errMsg = "Server number must be an integer."
			return m, nil
		}

		m.running = true
		m.status = fmt.Sprintf("Loading logs for server %s...", value)
		m.lastOutput = ""
		// Use 200 lines as a reasonable default.
		cmd := runTmuxLogsViewport(value, 200)
		return m, tea.Batch(cmd, m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewAddServersPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Add servers")) +
		"\n" +
		headerBorderStyle.Render("Enter how many servers to add")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Number of servers to add:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, "Press Enter to add servers, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateAddServersPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.view = viewMain
		m.status = "Select an action and press Enter to run it."
		return m, nil
	case "ctrl+c", "q":
		return m, tea.Quit
	case "enter":
		value := strings.TrimSpace(m.wizard.input.Value())
		if value == "" {
			m.wizard.errMsg = "Please enter how many servers to add."
			return m, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			m.wizard.errMsg = "Server count must be a positive integer."
			return m, nil
		}

		m.view = viewMain
		m.running = true
		m.scaling = true
		m.scalePercent = 0
		m.scaleProgress = progress.New(progress.WithDefaultGradient())
		m.scaleProgress.Width = 60
		m.status = fmt.Sprintf("Adding %d server(s)...", n)
		m.lastOutput = ""

		return m, tea.Batch(runAddServersGo(n), m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewRemoveServersPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Remove servers")) +
		"\n" +
		headerBorderStyle.Render("Enter how many servers to remove (from the end)")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Number of servers to remove:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, "Press Enter to remove servers, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateRemoveServersPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.view = viewMain
		m.status = "Select an action and press Enter to run it."
		return m, nil
	case "ctrl+c", "q":
		return m, tea.Quit
	case "enter":
		value := strings.TrimSpace(m.wizard.input.Value())
		if value == "" {
			m.wizard.errMsg = "Please enter how many servers to remove."
			return m, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			m.wizard.errMsg = "Server count must be a positive integer."
			return m, nil
		}

		m.view = viewMain
		m.running = true
		m.scaling = true
		m.scalePercent = 0
		m.scaleProgress = progress.New(progress.WithDefaultGradient())
		m.scaleProgress.Width = 60
		m.status = fmt.Sprintf("Removing %d server(s)...", n)
		m.lastOutput = ""

		return m, tea.Batch(runRemoveServersGo(n), m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func runTmuxStatusViewport() tea.Cmd {
	return func() tea.Msg {
		manager, err := csm.NewTmuxManager()
		if err != nil {
			return viewportFinishedMsg{
				title:   "Servers dashboard",
				content: fmt.Sprintf("Failed to load tmux status: %v\n\nIf you haven't installed servers yet, run the install wizard first from the Setup tab.", err),
				err:     err,
			}
		}
		out, err := manager.Status()
		return viewportFinishedMsg{
			title:   "Servers dashboard",
			content: out,
			err:     err,
		}
	}
}

func runTmuxLogsViewport(server string, lines int) tea.Cmd {
	return func() tea.Msg {
		manager, err := csm.NewTmuxManager()
		if err != nil {
			return viewportFinishedMsg{
				title:   fmt.Sprintf("Server %s logs", server),
				content: "",
				err:     err,
			}
		}

		n, err := strconv.Atoi(server)
		if err != nil {
			return viewportFinishedMsg{
				title:   fmt.Sprintf("Server %s logs", server),
				content: "",
				err:     fmt.Errorf("invalid server number %q", server),
			}
		}

		out, err := manager.Logs(n, lines)
		logPath := manager.ServerLogPath(n)
		if strings.TrimSpace(logPath) != "" {
			header := fmt.Sprintf("Underlying log file: %s\n\n", logPath)
			out = header + out
		}
		return viewportFinishedMsg{
			title:   fmt.Sprintf("Server %d logs", n),
			content: out,
			err:     err,
		}
	}
}

// Debugging servers is only supported via the CLI (csm debug <server>) to
// avoid conflicts with the TUI's own terminal control.

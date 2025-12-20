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
	if strings.TrimSpace(statusText) != "" {
		fmt.Fprintln(&b, statusBarStyle.Render(statusText))
	}

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
		cmd := runTmuxLogsDetail(value, 200)
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

	// Rough disk space estimate for adding N additional servers, based on the
	// same per-server footprint used by the install wizard's disk estimate.
	value := strings.TrimSpace(m.wizard.input.Value())
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		const perServerGB = csm.DefaultPerServerDiskGB
		additionalGB := perServerGB * float64(n)

		// Derive the CS2 user from the tmux manager so we match the actual
		// install layout on disk.
		if mgr, err := csm.NewTmuxManager(); err == nil {
			_, freeGB, ok := estimateDiskSpace(mgr.CS2User)
			if ok {
				afterGB := freeGB - additionalGB
				fmt.Fprintln(&b)
				summary := fmt.Sprintf(
					"Disk: adding %d server(s) will use ~%.1f GB; currently ~%.1f GB free, ~%.1f GB free after add.",
					n, additionalGB, freeGB, afterGB,
				)
				if afterGB < 0 {
					fmt.Fprintln(&b, warningStyle.Render(summary))
				} else {
					fmt.Fprintln(&b, subtleStyle.Render(summary))
				}
			}
		}
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

	// Rough disk space estimate for removing N servers. This mirrors the wizard
	// disk estimate style but focuses on space freed instead of required.
	value := strings.TrimSpace(m.wizard.input.Value())
	if n, err := strconv.Atoi(value); err == nil && n > 0 {
		if mgr, err := csm.NewTmuxManager(); err == nil && mgr.NumServers > 0 {
			// Clamp to the number of existing servers so the estimate remains
			// realistic even if the user types a larger number.
			if n > mgr.NumServers {
				n = mgr.NumServers
			}

			const perServerGB = csm.DefaultPerServerDiskGB
			freedGB := perServerGB * float64(n)

			_, freeGB, ok := estimateDiskSpace(mgr.CS2User)
			if ok {
				afterGB := freeGB + freedGB
				fmt.Fprintln(&b)
				summary := fmt.Sprintf(
					"Disk: removing %d server(s) will free ~%.1f GB; currently ~%.1f GB free, ~%.1f GB free after removal.",
					n, freedGB, freeGB, afterGB,
				)
				fmt.Fprintln(&b, subtleStyle.Render(summary))
			}
		}
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

// Note: the old scrollable logs viewport has been replaced with a simpler
// non-scrollable detail view (see runTmuxLogsDetail in commands.go) to avoid
// nested scrolling complexity in the TUI.

// Debugging servers is only supported via the CLI (csm debug <server>) to
// avoid conflicts with the TUI's own terminal control.

package tui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
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

func (m model) viewReinstallServerPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Reinstall a server")) +
		"\n" +
		headerBorderStyle.Render("Completely rebuild a server from master-install")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Server number to reinstall:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, subtleStyle.Render("This will delete the server directory and copy fresh files from master."))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Press Enter to reinstall server, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateReinstallServerPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
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
			m.wizard.errMsg = "Please enter a server number."
			return m, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			m.wizard.errMsg = "Server number must be a positive integer."
			return m, nil
		}

		m.view = viewMain
		m.running = true
		m.scaling = true // Reuse scaling flag for progress bar
		m.scalePercent = 0
		m.scaleProgress = progress.New(progress.WithDefaultGradient())
		m.scaleProgress.Width = 60
		m.status = fmt.Sprintf("Reinstalling server %d...", n)
		m.lastOutput = ""

		return m, tea.Batch(runReinstallServerGo(n), m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewUnbanIPPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Unban IP address")) +
		"\n" +
		headerBorderStyle.Render("Remove an IP from banned RCON requests")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Enter server number and IP (e.g., '1 172.19.0.3'):")
	fmt.Fprintln(&b, "  Use '0' as server number to unban from all servers")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, subtleStyle.Render("Removes IP from banned_ip.cfg (e.g., Docker IPs incorrectly banned)."))
		fmt.Fprintln(&b, subtleStyle.Render("Example: '1 172.19.0.3' or '0 172.19.0.3' (all servers)"))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Press Enter to unban IP, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateUnbanIPPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
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
			m.wizard.errMsg = "Please enter server number and IP address."
			return m, nil
		}

		// Parse input: "server ip" or "ip" (defaults to server 0)
		parts := strings.Fields(value)
		var serverNum int
		var ip string
		var err error

		if len(parts) == 1 {
			// Just IP provided, unban from all servers
			serverNum = 0
			ip = parts[0]
		} else if len(parts) == 2 {
			// Server number and IP provided
			serverNum, err = strconv.Atoi(parts[0])
			if err != nil || serverNum < 0 {
				m.wizard.errMsg = "Server number must be 0 (all servers) or a positive integer."
				return m, nil
			}
			ip = parts[1]
		} else {
			m.wizard.errMsg = "Invalid format. Use 'server ip' or 'ip' (e.g., '1 172.19.0.3' or '172.19.0.3')."
			return m, nil
		}

		// Validate IP format
		if net.ParseIP(ip) == nil {
			m.wizard.errMsg = "Invalid IP address format."
			return m, nil
		}

		m.view = viewMain
		m.running = true
		m.status = fmt.Sprintf("Unbanning %s from server %d...", ip, serverNum)
		m.lastOutput = ""

		return m, tea.Batch(runUnbanIPGo(serverNum, ip), m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewUnbanAllIPsPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Unban all IP addresses")) +
		"\n" +
		headerBorderStyle.Render("Clear all IPs banned for RCON hacking attempts")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Enter server number (0 for all servers):")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, subtleStyle.Render("This will remove all IP bans from the server's banned_ip.cfg file."))
		fmt.Fprintln(&b, subtleStyle.Render("Useful when multiple IPs were incorrectly banned for RCON attempts."))
		fmt.Fprintln(&b, subtleStyle.Render("Use '0' to clear bans from all servers."))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Press Enter to clear all bans, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateUnbanAllIPsPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
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
			m.wizard.errMsg = "Please enter a server number (0 for all servers)."
			return m, nil
		}

		serverNum, err := strconv.Atoi(value)
		if err != nil || serverNum < 0 {
			m.wizard.errMsg = "Server number must be 0 (all servers) or a positive integer."
			return m, nil
		}

		m.view = viewMain
		m.running = true
		if serverNum == 0 {
			m.status = "Clearing all IP bans from all servers..."
		} else {
			m.status = fmt.Sprintf("Clearing all IP bans from server %d...", serverNum)
		}
		m.lastOutput = ""

		return m, tea.Batch(runUnbanAllIPsGo(serverNum), m.spin.Tick)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewAttachServerPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Attach to server console")) +
		"\n" +
		headerBorderStyle.Render("Enter server number to attach to tmux console")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Server number:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, subtleStyle.Render("The TUI will exit and attach to the server console."))
		fmt.Fprintln(&b, subtleStyle.Render("Press Ctrl+B then D to detach and return to your shell."))
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Press Enter to attach, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateAttachServerPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
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
			m.wizard.errMsg = "Please enter a server number."
			return m, nil
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			m.wizard.errMsg = "Server number must be a positive integer."
			return m, nil
		}

		// Attach needs to quit the TUI and take over the terminal
		return m, runAttachServer(n)
	}

	var cmd tea.Cmd
	m.wizard.input, cmd = m.wizard.input.Update(key)
	return m, cmd
}

func (m model) viewServerConfigPrompt() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("View server.cfg")) +
		"\n" +
		headerBorderStyle.Render("Enter server number to view server.cfg")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	fmt.Fprintln(&b, "Server number:")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, m.wizard.input.View())
	fmt.Fprintln(&b)

	if m.wizard.errMsg != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	} else {
		fmt.Fprintln(&b, "Press Enter to view config, Esc to cancel.")
	}

	return b.String()
}

func (m model) updateServerConfigPromptKey(key tea.KeyMsg) (model, tea.Cmd) {
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
			m.wizard.errMsg = "Please enter a server number."
			return m, nil
		}
		if _, err := strconv.Atoi(value); err != nil {
			m.wizard.errMsg = "Server number must be an integer."
			return m, nil
		}

		m.running = true
		m.status = fmt.Sprintf("Loading server.cfg for server %s...", value)
		m.lastOutput = ""
		cmd := runViewServerConfigViewport(value)
		return m, tea.Batch(cmd, m.spin.Tick)
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

func runViewServerConfigViewport(server string) tea.Cmd {
	return func() tea.Msg {
		manager, err := csm.NewTmuxManager()
		if err != nil {
			return viewportFinishedMsg{
				title:   fmt.Sprintf("Server %s server.cfg", server),
				content: fmt.Sprintf("Failed to load tmux manager: %v", err),
				err:     err,
			}
		}

		n, err := strconv.Atoi(server)
		if err != nil {
			return viewportFinishedMsg{
				title:   fmt.Sprintf("Server %s server.cfg", server),
				content: "",
				err:     fmt.Errorf("invalid server number %q", server),
			}
		}

		// Construct path to server.cfg
		user := manager.CS2User
		cfgPath := filepath.Join("/home", user, fmt.Sprintf("server-%d", n), "game", "csgo", "cfg", "server.cfg")

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return viewportFinishedMsg{
				title:   fmt.Sprintf("Server %d server.cfg", n),
				content: fmt.Sprintf("Failed to read server.cfg from %s: %v\n\nIf the server hasn't been installed yet, run the install wizard first.", cfgPath, err),
				err:     err,
			}
		}

		content := string(data)
		if strings.TrimSpace(content) == "" {
			content = "(server.cfg is empty)"
		}

		header := fmt.Sprintf("File: %s\n\n", cfgPath)
		return viewportFinishedMsg{
			title:   fmt.Sprintf("Server %d server.cfg", n),
			content: header + content,
			err:     nil,
		}
	}
}

// Debugging servers is only supported via the CLI (csm debug <server>) to
// avoid conflicts with the TUI's own terminal control.

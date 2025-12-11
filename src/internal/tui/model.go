package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	huh "github.com/charmbracelet/huh"
	tea "github.com/charmbracelet/bubbletea"
)

type viewMode int

const (
	viewMain viewMode = iota
	viewInstallWizard
	viewViewport
	viewLogsPrompt
)

type itemKind int

const (
	itemRunCommand itemKind = iota
	itemInstallWizard
	itemInstallDepsGo
	itemUpdateNow
	itemServersStatusViewport
	itemMatchzyDBViewport
	itemLogsViewport
	itemUpdateGameGo
	itemDeployPluginsGo
	itemInstallMonitorGo
	itemStartAllGo
	itemStopAllGo
	itemRestartAllGo
	itemPublicIPGo
	itemExtractThumbnailsGo
)

// top-level tabs for grouping actions
type tab int

const (
	tabSetup tab = iota
	tabServers
	tabMaintenance
	tabUtilities
)

// menuItem represents a single entry in the main menu.
type menuItem struct {
	title       string
	description string
	command     []string // underlying command to run (relative to repo root)
	kind        itemKind
}

type commandFinishedMsg struct {
	item   menuItem
	output string
	err    error
}

type viewportFinishedMsg struct {
	title   string
	content string
	err     error
}

type installConfig struct {
	dbMode            string // "docker" or "external"
	numServers        int
	basePort          int
	tvPort            int
	cs2User           string
	enableMetamod     bool
	freshInstall      bool
	updateMaster      bool
	rconPassword      string
	updatePlugins     bool
	matchzySkipDocker bool
}

type installWizard struct {
	active bool
	cfg    installConfig

	// Huh form for the install wizard.
	form      *huh.Form
	reviewing bool

	// Numeric fields bound to the form as strings; parsed into cfg on submit.
	numServersStr string
	basePortStr   string
	tvPortStr     string

	// Shared text input + error message used for the server logs prompt.
	input  textinput.Model
	errMsg string
}

type model struct {
	view   viewMode
	tab    tab
	items  []menuItem
	cursor int
	status string

	lastOutput string
	running    bool
	spin       spinner.Model

	wizard installWizard

	vp      viewport.Model
	vpTitle string

	version        string
	latestVersion  string
	updateChecked  bool
	updateAvailable bool
}

// New constructs the initial Bubble Tea model for the CS2 TUI.
func New() tea.Model {
	return initialModel()
}

func initialModel() model {
	t := tabSetup
	items := buildItemsForTab(t)

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = titleStyle

	m := model{
		view:     viewMain,
		tab:      t,
		items:    items,
		status:   "",
		spin:     spin,
		version:  currentVersion,
		updateChecked: false,
	}

	// Initialize wizard defaults
	m.initWizardDefaults()
	return m
}

// buildItemsForTab returns the menu items for a given top-level tab.
func buildItemsForTab(t tab) []menuItem {
	switch t {
	case tabSetup:
		return []menuItem{
			{
				title:       "Install system dependencies (sudo)",
				description: "",
				kind:        itemInstallDepsGo,
			},
			{
				title:       "Install / redeploy servers",
				description: "",
				kind:        itemInstallWizard,
			},
			{
				title:       "Install/reinstall auto-update monitor (sudo)",
				description: "",
				kind:        itemInstallMonitorGo,
			},
		}
	case tabServers:
		return []menuItem{
			{
				title:       "Servers dashboard",
				description: "",
				kind:        itemServersStatusViewport,
			},
			{
				title:       "Server logs (scrollable)",
				description: "",
				kind:        itemLogsViewport,
			},
			{
				title:       "Start all servers",
				description: "",
				kind:        itemStartAllGo,
			},
			{
				title:       "Stop all servers",
				description: "",
				kind:        itemStopAllGo,
			},
			{
				title:       "Restart all servers",
				description: "",
				kind:        itemRestartAllGo,
			},
		}
	case tabMaintenance:
		return []menuItem{
			{
				title:       "Update CS2 game files",
				description: "",
				kind:        itemUpdateGameGo,
			},
			{
				title:       "Deploy plugins to all servers",
				description: "",
				kind:        itemDeployPluginsGo,
			},
			{
				title:       "MatchZy DB: verify/repair",
				description: "Verify MatchZy database setup and repair in a scrollable view.",
				kind:        itemMatchzyDBViewport,
			},
		}
	case tabUtilities:
		return []menuItem{
			{
				title:       "Show public IP",
				description: "",
				kind:        itemPublicIPGo,
			},
			{
				title:       "Extract map thumbnails",
				description: "",
				kind:        itemExtractThumbnailsGo,
			},
		}
	default:
		return nil
	}
}

func (m *model) initWizardDefaults() {
	cfg := installConfig{
		dbMode:            "docker",
		numServers:        3,
		basePort:          27015,
		tvPort:            27020,
		cs2User:           "cs2",
		enableMetamod:     true,
		freshInstall:      false,
		updateMaster:      true,
		rconPassword:      "ntlan2025",
		updatePlugins:     true,
		matchzySkipDocker: false,
	}

	ti := textinput.New()
	ti.Placeholder = ""
	ti.Focus()

	m.wizard = installWizard{
		active:    false,
		cfg:       cfg,
		form:      nil,
		reviewing: false,
		input:     ti,
	}
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	// Always check for updates in the background.
	cmds = append(cmds, checkForUpdates())
	if m.view == viewInstallWizard {
		cmds = append(cmds, textinput.Blink)
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Update spinner while a command is running.
	if m.running {
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		cmds = append(cmds, cmd)
	}

	// If the install wizard is active, delegate messages to the huh form.
	if m.view == viewInstallWizard && m.wizard.form != nil {
		var cmd tea.Cmd
		m, cmd = m.updateInstallWizard(msg)
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:

		// While a command is running (e.g. install wizard, updates), lock the UI
		// so the user can't navigate to other tabs or trigger new actions.
		// Allow quitting with ctrl+c or q.
		if m.running {
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			default:
				return m, tea.Batch(cmds...)
			}
		}

		if m.view == viewLogsPrompt {
			var cmd tea.Cmd
			m, cmd = m.updateLogsPromptKey(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		if m.view == viewViewport {
			switch msg.String() {
			case "q", "esc":
				// Return to main menu.
				m.view = viewMain
				m.status = "Select an action and press Enter to run it."
				return m, nil
			default:
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

		switch msg.String() {
		case "left", "h":
			if m.view == viewMain && m.tab > tabSetup {
				m.tab--
				m.items = buildItemsForTab(m.tab)
				m.cursor = 0
			}
		case "right", "l":
			if m.view == viewMain && m.tab < tabUtilities {
				m.tab++
				m.items = buildItemsForTab(m.tab)
				m.cursor = 0
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			if m.running || len(m.items) == 0 {
				return m, tea.Batch(cmds...)
			}

			selected := m.items[m.cursor]
			switch selected.kind {
			case itemInstallDepsGo:
				m.running = true
				m.status = "Installing system dependencies (this may take a few minutes)..."
				m.lastOutput = ""
				cmds = append(cmds, runInstallDepsGo(), m.spin.Tick)
			case itemUpdateNow:
				if m.updateAvailable && m.latestVersion != "" {
					m.running = true
					m.status = fmt.Sprintf("Updating CSM to %s...", m.latestVersion)
					m.lastOutput = ""
					cmds = append(cmds, runSelfUpdate(m.latestVersion), m.spin.Tick)
				}
			case itemInstallWizard:
				m.view = viewInstallWizard
				m.wizard.active = true
				m.status = "Install wizard: configure your servers."
				// (Re)build the huh form for the wizard and initialize it.
				m.wizard.form = buildInstallForm(&m.wizard)
				cmds = append(cmds, m.wizard.form.Init())
			case itemServersStatusViewport:
				m.running = true
				m.status = "Loading server status (checking for tmux sessions and installed servers)..."
				m.lastOutput = ""
				cmds = append(cmds, runTmuxStatusViewport(), m.spin.Tick)
			case itemMatchzyDBViewport:
				m.running = true
				m.status = "Verifying MatchZy database..."
				m.lastOutput = ""
				cmds = append(cmds, runMatchzyDBViewport(), m.spin.Tick)
			case itemLogsViewport:
				m.view = viewLogsPrompt
				m.status = "Logs: enter server number."
				m.wizard.errMsg = ""
				m.wizard.input.SetValue("")
				m.wizard.input.Focus()
				cmds = append(cmds, textinput.Blink)
			case itemUpdateGameGo:
				m.running = true
				m.status = "Updating CS2 game files on all servers (Go)..."
				m.lastOutput = ""
				cmds = append(cmds, runUpdateGameGo(), m.spin.Tick)
			case itemDeployPluginsGo:
				m.running = true
				m.status = "Deploying plugins to all servers (Go)..."
				m.lastOutput = ""
				cmds = append(cmds, runDeployPluginsGo(), m.spin.Tick)
			case itemInstallMonitorGo:
				m.running = true
				m.status = "Installing auto-update monitor cronjob..."
				m.lastOutput = ""
				cmds = append(cmds, runInstallMonitorGo(), m.spin.Tick)
			case itemStartAllGo:
				m.running = true
				m.status = "Starting all servers..."
				m.lastOutput = ""
				cmds = append(cmds, runStartAllServers(), m.spin.Tick)
			case itemStopAllGo:
				m.running = true
				m.status = "Stopping all servers..."
				m.lastOutput = ""
				cmds = append(cmds, runStopAllServers(), m.spin.Tick)
			case itemRestartAllGo:
				m.running = true
				m.status = "Restarting all servers..."
				m.lastOutput = ""
				cmds = append(cmds, runRestartAllServers(), m.spin.Tick)
			case itemPublicIPGo:
				m.running = true
				m.status = "Resolving public IP..."
				m.lastOutput = ""
				cmds = append(cmds, runPublicIP(), m.spin.Tick)
			case itemExtractThumbnailsGo:
				m.running = true
				m.status = "Extracting and converting map thumbnails..."
				m.lastOutput = ""
				cmds = append(cmds, runExtractThumbnailsGo(), m.spin.Tick)
			case itemRunCommand:
				m.running = true
				m.status = fmt.Sprintf("Running: %s ...", selected.title)
				m.lastOutput = ""
				cmds = append(cmds, runCommand(selected), m.spin.Tick)
			}
		}

	case commandFinishedMsg:
		m.running = false

		// Special-case commands that want minimal UI chrome.
		switch msg.item.kind {
		case itemPublicIPGo:
			// For public IP we only show the IP itself in the status bar and
			// suppress the "Last command output" section entirely.
			if msg.err != nil {
				m.status = fmt.Sprintf("Public IP lookup failed: %v", msg.err)
				m.lastOutput = ""
				break
			}
			ip := strings.TrimSpace(msg.output)
			if ip == "" {
				m.status = "Public IP: (not available)"
			} else {
				m.status = ip
			}
			m.lastOutput = ""

		default:
			out := strings.TrimSpace(msg.output)

			if out != "" {
				// Truncate output to last ~24 lines to keep the view readable.
				lines := strings.Split(out, "\n")
				if len(lines) > 24 {
					lines = lines[len(lines)-24:]
				}
				m.lastOutput = strings.Join(lines, "\n")
			} else {
				// If the command produced no output, don't show a noisy
				// "(no output)" block – just keep the status line.
				m.lastOutput = ""
			}

			if msg.err != nil {
				m.status = fmt.Sprintf("Command failed: %v", msg.err)
			} else {
				m.status = fmt.Sprintf("Finished: %s", msg.item.title)
			}
		}

	case viewportFinishedMsg:
		// A long-running viewport operation (status, logs, DB verify, etc.)
		// has completed. Show the content in a scrollable viewport.
		m.running = false
		m.view = viewViewport
		m.vpTitle = msg.title

		// Lazily initialize the viewport with a sensible default size.
		if m.vp.Width == 0 || m.vp.Height == 0 {
			m.vp = viewport.New(80, 20)
		}
		m.vp.SetContent(msg.content)

		if msg.err != nil && strings.TrimSpace(msg.content) == "" {
			m.status = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.status = ""
		}

	case updateInfoMsg:
		m.updateChecked = true
		if msg.err == nil && msg.latest != "" {
			m.latestVersion = msg.latest
			m.updateAvailable = isNewerVersion(m.version, msg.latest)

			if m.updateAvailable {
				updateItem := menuItem{
					title:       fmt.Sprintf("Update CSM to %s now (sudo)", m.latestVersion),
					description: fmt.Sprintf("Download and replace the current CSM binary (%s → %s). May require sudo if installed globally.", m.version, m.latestVersion),
					kind:        itemUpdateNow,
				}
				// Prepend update item to the existing menu.
				m.items = append([]menuItem{updateItem}, m.items...)
				// Keep cursor on the update item initially.
				m.cursor = 0
			}
		}

	case selfUpdateFinishedMsg:
		m.running = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Update failed: %v", msg.err)
		} else {
			m.status = fmt.Sprintf("CSM updated to %s. Restart CSM to use the new version.", msg.newVersion)
			m.version = msg.newVersion
			m.updateAvailable = false
		}
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	switch m.view {
	case viewInstallWizard:
		return m.viewInstallWizard()
	case viewViewport:
		return m.viewViewport()
	case viewLogsPrompt:
		return m.viewLogsPrompt()
	}

	var b strings.Builder

	// Header
	fmt.Fprintln(&b, headerBorderStyle.Render(titleStyle.Render("CS2 Server Manager")))
	fmt.Fprintln(&b)

	// Tab bar (disabled visually while a command is running).
	tabs := []string{"Setup", "Servers", "Maintenance", "Utilities"}
	var tabParts []string
	for i, name := range tabs {
		style := tabInactiveStyle
		if tab(i) == m.tab {
			style = tabActiveStyle
		}
		if m.running {
			// Dim the tabs slightly when locked.
			style = style.Faint(true)
		}
		tabParts = append(tabParts, style.Render(name))
	}
	fmt.Fprintln(&b, tabBarStyle.Render(strings.Join(tabParts, "  ")))
	fmt.Fprintln(&b)

	// Version / update banner
	if !m.updateChecked {
		// Optional: show a subtle one-time checking message.
		fmt.Fprintln(&b, subtleStyle.Render("Checking for updates..."))
	} else if m.updateAvailable && m.latestVersion != "" {
		text := fmt.Sprintf("New update available! CSM %s → %s", m.version, m.latestVersion)
		fmt.Fprintln(&b, versionBannerStyle.Render(text))
	}
	fmt.Fprintln(&b)

	// Menu list.
	for i, item := range m.items {
		selected := m.cursor == i && !m.running

		label := item.title
		lineStyle := menuItemStyle
		if selected {
			lineStyle = menuSelectedStyle
		}
		checkbox := checkboxStyle.Render("[x] ")
		if !selected {
			checkbox = subtleStyle.Render("[ ] ")
		}

		if m.running {
			// When locked, make the whole menu look disabled.
			lineStyle = lineStyle.Faint(true)
			checkbox = subtleStyle.Render("[ ] ")
		}

		fmt.Fprintln(&b, lineStyle.Render(checkbox+label))
		fmt.Fprintln(&b)
	}

	// Status bar with spinner.
	statusText := m.status
	if m.running {
		statusText = fmt.Sprintf("%s %s", m.spin.View(), m.status)
	}
	if strings.TrimSpace(statusText) != "" {
		fmt.Fprintln(&b, statusBarStyle.Render(statusText))
	}

	// Output section.
	if m.lastOutput != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, outputTitleStyle.Render("Last command output (truncated):"))
		fmt.Fprintln(&b, outputBodyStyle.Render(m.lastOutput))
	}

	return mainStyle.Render("\n" + b.String() + "\n")
}



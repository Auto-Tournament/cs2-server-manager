package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	huh "github.com/charmbracelet/huh"
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
	itemForceUpdateNow
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

// installStep represents the high-level phases of the install wizard so we can
// show progress and run each step sequentially with its own status.
type installStep int

const (
	installStepPlugins installStep = iota
	installStepBootstrap
	installStepMonitor
	installStepStartServers
)

// installStepMsg is emitted after each install step completes so the TUI can
// update status/output and schedule the next step.
type installStepMsg struct {
	step installStep
	out  string
	err  error
}

// installLogTickMsg carries a snapshot of the latest tail of the install log
// so we can render live progress while long-running steps (like steamcmd) run.
type installLogTickMsg struct {
	lines string
}

// selfUpdateProgressMsg represents download progress for the self-update flow.
// Percent is 0-100; a negative value means "unknown/streaming without size".
type selfUpdateProgressMsg struct {
	Percent int
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

	version         string
	latestVersion   string
	updateChecked   bool
	updateAvailable bool

	// Self-update UI state
	selfUpdating   bool
	updateProgress progress.Model

	// When true, the next 'q' while a command is running will confirm quitting
	// and cancel any active operations (steamcmd, installs, etc.).
	confirmQuit bool
}

// New constructs the initial Bubble Tea model for the CS2 TUI.
func New() tea.Model {
	return initialModel()
}

func initialModel() model {
	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = titleStyle

	up := progress.New(progress.WithDefaultGradient())

	m := model{
		view:           viewMain,
		tab:            tabSetup,
		items:          nil, // will be set by initWizardDefaults + rebuildItems
		status:         "",
		spin:           spin,
		updateProgress: up,
		version:        currentVersion,
		updateChecked:  false,
	}

	// Initialize wizard defaults
	m.initWizardDefaults()
	m.rebuildItems()
	return m
}

// rebuildItems rebuilds the menu for the current tab, optionally appending
// dynamic items like the self-update action at the bottom.
func (m *model) rebuildItems() {
	items := buildItemsForTab(m.tab)

	// Append self-update item at the bottom of the Setup tab when an update is
	// available. This keeps the main actions visually grouped and the update
	// affordance easy to discover without dominating the menu.
	if m.tab == tabSetup && m.updateAvailable && m.latestVersion != "" {
		updateItem := menuItem{
			title:       fmt.Sprintf("Update CSM to %s now (sudo)", m.latestVersion),
			description: fmt.Sprintf("Download and replace the current CSM binary (%s → %s). May require sudo if installed globally.", m.version, m.latestVersion),
			kind:        itemUpdateNow,
		}
		items = append(items, updateItem)
	}

	m.items = items
	// Keep cursor in range.
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}
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
				title:       "Force update CSM now (sudo)",
				description: "",
				kind:        itemForceUpdateNow,
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

	// Ensure the menu reflects any existing update state.
	m.rebuildItems()
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
				// First press: ask for confirmation so users don't accidentally
				// kill long-running installs or updates.
				if !m.confirmQuit {
					m.confirmQuit = true
					m.status = "Press Q again to abort the current operation and exit, or press C to continue."
					return m, tea.Batch(cmds...)
				}
				// Second Q (or ctrl+c twice): cancel and quit.
				CancelInstall()
				return m, tea.Quit
			case "c":
				// Allow users to back out of the quit confirmation and keep
				// the current operation running.
				if m.confirmQuit {
					m.confirmQuit = false
					// Don't overwrite more specific status messages; just
					// clear the confirmation.
				}
				return m, tea.Batch(cmds...)
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

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// In viewport mode, q navigates back; from the main menu, q quits.
			if m.view == viewViewport {
				m.view = viewMain
				// Keep whatever status we already had; no extra noise.
				return m, nil
			}
			if m.view == viewMain {
				return m, tea.Quit
			}

			// In other views (e.g. wizard), let huh/textinput handle q normally.
			return m, tea.Batch(cmds...)

		case "left", "h":
			if m.view == viewMain && m.tab > tabSetup {
				m.tab--
				m.rebuildItems()
				m.cursor = 0
			}
		case "right", "l":
			if m.view == viewMain && m.tab < tabUtilities {
				m.tab++
				m.rebuildItems()
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
					m.selfUpdating = true
					// Reset progress bar.
					m.updateProgress = progress.New(progress.WithDefaultGradient())
					cmds = append(cmds, runSelfUpdate(m.latestVersion), m.spin.Tick)
				}
			case itemForceUpdateNow:
				// Force update ignores the local cache TTL and always hits the
				// GitHub API, then immediately runs the self-update if a newer
				// version is available.
				m.running = true
				m.selfUpdating = false
				m.status = "Forcing CSM update check (bypassing local cache)..."
				m.lastOutput = ""
				cmds = append(cmds, checkForUpdatesForce(), m.spin.Tick)
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
		m.confirmQuit = false

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
				lines := strings.Split(out, "\n")

				// When a command fails, keep the full output to make debugging
				// easier (especially for long bootstrap/steamcmd logs).
				if msg.err == nil {
					// On success, truncate output to keep the view readable.
					maxLines := 24
					if msg.item.kind == itemInstallWizard {
						maxLines = 20
					}
					if len(lines) > maxLines {
						lines = lines[len(lines)-maxLines:]
					}
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

	case installStepMsg:
		// Keep showing the spinner while we chain through install steps; we'll
		// only mark running=false after the final step or on error.
		out := strings.TrimSpace(msg.out)
		if out != "" {
			lines := strings.Split(out, "\n")

			// For successful steps, keep the log view tight (last N lines). On
			// failures, show the full output to aid debugging.
			if msg.err == nil {
				maxLines := 10
				if len(lines) > maxLines {
					lines = lines[len(lines)-maxLines:]
				}
			}
			m.lastOutput = strings.Join(lines, "\n")
		} else {
			m.lastOutput = ""
		}

		if msg.err != nil {
			m.confirmQuit = false
			CancelInstall()
			m.running = false
			m.status = fmt.Sprintf("Install failed during step: %v", msg.err)
			return m, tea.Batch(cmds...)
		}

		// Chain to the next step with an updated status line.
		switch msg.step {
		case installStepPlugins:
			m.status = "Step 2/4: Setting up CS2 servers (steamcmd)..."
			return m, tea.Batch(append(cmds, runInstallStep(m.wizard.cfg, installStepBootstrap), m.spin.Tick)...)
		case installStepBootstrap:
			m.status = "Step 3/4: Configuring auto-update monitor (cron)..."
			return m, tea.Batch(append(cmds, runInstallStep(m.wizard.cfg, installStepMonitor), m.spin.Tick)...)
		case installStepMonitor:
			m.status = "Step 4/4: Starting all servers..."
			return m, tea.Batch(append(cmds, runInstallStep(m.wizard.cfg, installStepStartServers), m.spin.Tick)...)
		case installStepStartServers:
			CancelInstall()
			m.running = false
			m.confirmQuit = false
			m.status = "Install wizard finished successfully."
			return m, tea.Batch(cmds...)
		}

	case installLogTickMsg:
		// Live tail of the bootstrap log while steamcmd and other long-running
		// operations are in progress. We keep this very small so it feels like a
		// "what's happening right now" view rather than a full log.
		out := strings.TrimSpace(msg.lines)
		if out != "" {
			m.lastOutput = out
		}
		// No new commands scheduled here; the tailer goroutine drives further
		// updates by sending more installLogTickMsg values.
		return m, tea.Batch(cmds...)

	case selfUpdateProgressMsg:
		// Live progress for self-update: drive the progress bar and keep an
		// optional textual label for additional clarity.
		if msg.Percent >= 0 {
			pct := float64(msg.Percent) / 100.0
			cmd := m.updateProgress.SetPercent(pct)
			cmds = append(cmds, cmd)
			m.lastOutput = fmt.Sprintf("Downloading update: %d%%", msg.Percent)
		} else {
			m.lastOutput = "Downloading update..."
		}
		return m, tea.Batch(cmds...)

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
				// Rebuild the menu so the Setup tab gets an "Update CSM…" item
				// appended at the bottom. We don't force-move the cursor; users
				// can choose when to trigger the update.
				m.rebuildItems()
			}
		}

	case forceUpdateInfoMsg:
		// Result of an explicit "Force update CSM now" action from Utilities.
		if msg.err != nil {
			m.running = false
			m.selfUpdating = false
			m.status = fmt.Sprintf("Force update check failed: %v", msg.err)
			return m, tea.Batch(cmds...)
		}

		m.latestVersion = msg.latest
		m.updateAvailable = isNewerVersion(m.version, msg.latest)

		if !m.updateAvailable {
			m.running = false
			m.selfUpdating = false
			m.status = fmt.Sprintf("CSM is already up to date (remote %s).", m.latestVersion)
			return m, tea.Batch(cmds...)
		}

		// Newer version available: immediately run the self-update with a
		// proper progress bar.
		m.selfUpdating = true
		m.status = fmt.Sprintf("Updating CSM to %s...", m.latestVersion)
		m.lastOutput = ""
		m.updateProgress = progress.New(progress.WithDefaultGradient())
		cmds = append(cmds, runSelfUpdate(m.latestVersion), m.spin.Tick)
		return m, tea.Batch(cmds...)

	case selfUpdateFinishedMsg:
		m.running = false
		m.selfUpdating = false
		m.confirmQuit = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Update failed: %v", msg.err)
		} else {
			m.status = fmt.Sprintf("CSM updated to %s. Restart CSM to use the new version.", msg.newVersion)
			m.version = msg.newVersion
			m.updateAvailable = false
			// Rebuild menu so the update item disappears once we're on the new version.
			m.rebuildItems()
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

	// Tab bar. While a long-running command is active, we hide the tabs to
	// reduce visual clutter and focus attention on the status/output.
	if !m.running {
		tabs := []string{"Setup", "Servers", "Maintenance", "Utilities"}
		var tabParts []string
		for i, name := range tabs {
			style := tabInactiveStyle
			if tab(i) == m.tab {
				style = tabActiveStyle
			}
			tabParts = append(tabParts, style.Render(name))
		}
		fmt.Fprintln(&b, tabBarStyle.Render(strings.Join(tabParts, "  ")))
		fmt.Fprintln(&b)
	} else {
		fmt.Fprintln(&b)
	}

	// Version / update banner: only on the main Setup tab. Other tabs focus on
	// their own content without the global banner noise.
	if m.tab == tabSetup {
		if !m.updateChecked {
			fmt.Fprintln(&b, subtleStyle.Render("Checking for updates..."))
		} else if m.updateAvailable && m.latestVersion != "" {
			text := fmt.Sprintf("New update available! CSM %s → %s", m.version, m.latestVersion)
			fmt.Fprintln(&b, versionBannerStyle.Render(text))
		}
		fmt.Fprintln(&b)
	} else {
		fmt.Fprintln(&b)
	}

	// Menu list. While a command is running we hide the menu entirely so the
	// user isn't staring at disabled options they can't interact with.
	if !m.running {
		for i, item := range m.items {
			selected := m.cursor == i

			label := item.title
			lineStyle := menuItemStyle
			if selected {
				lineStyle = menuSelectedStyle
			}
			checkbox := checkboxStyle.Render("[x] ")
			if !selected {
				checkbox = subtleStyle.Render("[ ] ")
			}

			fmt.Fprintln(&b, lineStyle.Render(checkbox+label))
			fmt.Fprintln(&b)
		}
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
	if m.selfUpdating {
		// For self-update, show a proper progress bar plus an optional label
		// instead of the generic "Last command output" box.
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, outputTitleStyle.Render("Downloading update:"))
		bar := m.updateProgress.View()
		label := strings.TrimSpace(m.lastOutput)
		if label == "" {
			fmt.Fprintln(&b, outputBodyStyle.Render(bar))
		} else {
			fmt.Fprintln(&b, outputBodyStyle.Render(bar))
			fmt.Fprintln(&b, outputBodyStyle.Render(label))
		}
	} else if m.lastOutput != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, outputTitleStyle.Render("Last command output:"))
		fmt.Fprintln(&b, outputBodyStyle.Render(m.lastOutput))
	}

	// Footer: always show current version in subtle gray so users can quickly
	// see which build they're running.
	if strings.TrimSpace(m.version) != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, footerVersionStyle.Render(fmt.Sprintf("CSM %s", m.version)))
	}

	return mainStyle.Render("\n" + b.String() + "\n")
}

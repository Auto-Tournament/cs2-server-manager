package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type viewMode int

const (
	viewMain viewMode = iota
	viewInstallWizard
	viewViewport
	viewActionResult
	viewPublicIP
	viewCleanupConfirm
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
	itemCleanupAllGo
	itemAttachHelp
	itemDebugHelp
)

// top-level tabs for grouping actions
type tab int

const (
	tabSetup tab = iota
	tabServers
	tabMaintenance
	tabUtilities
	tabDanger
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

	// Numeric fields bound to the form as strings; parsed into cfg on submit.
	numServersStr string
	basePortStr   string
	tvPortStr     string

	// Cursor + editing/scroll state for the one-page wizard view.
	cursor      int
	editing     bool
	windowStart int

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

// installElapsedMsg drives live "elapsed time" updates while each install
// wizard step is running.
type installElapsedMsg struct{}

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

	// Terminal height (rows), captured from Bubble Tea's WindowSizeMsg so we
	// can size scrollable views (like the install wizard) dynamically.
	height int

	// menuWindowStart controls which slice of m.items is visible in the main
	// menu so that the list feels scrollable on smaller terminals.
	menuWindowStart int

	// When true, the next 'q' while a command is running will confirm quitting
	// and cancel any active operations (steamcmd, installs, etc.).
	confirmQuit bool

	// publicIP holds the last-resolved public IP so we can show it on a
	// dedicated screen that the user dismisses with Enter.
	publicIP string

	// detailTitle/detailContent hold the result of the last completed action
	// when shown on a dedicated "detail" page that the user dismisses with
	// Enter (or q/Esc), similar to a separate page on a website.
	detailTitle   string
	detailContent string

	// Live timing info for the multi-step install wizard so we can display
	// "elapsed vs expected" while each step is running.
	installStepStart   time.Time
	currentInstallStep installStep
	installStatusBase  string
	installExpected    string
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
				title:       fmt.Sprintf("Update CSM to %s now", m.latestVersion),
				description: fmt.Sprintf("Download and replace the current CSM binary (%s → %s).", m.version, m.latestVersion),
				kind:        itemUpdateNow,
			}
			items = append(items, updateItem)
		}

	m.items = items
	// Keep cursor in range.
	if m.cursor >= len(m.items) {
		m.cursor = max(0, len(m.items)-1)
	}

	// Ensure the menu window start keeps the cursor visible when the visible
	// window is smaller than the full list.
	windowSize := wizardWindowSizeFor(m.height)
	if windowSize <= 0 {
		windowSize = len(m.items)
	}
	if m.cursor < m.menuWindowStart {
		m.menuWindowStart = m.cursor
	} else if m.cursor >= m.menuWindowStart+windowSize {
		m.menuWindowStart = m.cursor - windowSize + 1
	}
}

// buildItemsForTab returns the menu items for a given top-level tab.
func buildItemsForTab(t tab) []menuItem {
	switch t {
	case tabSetup:
		return []menuItem{
			{
				title:       "Install system dependencies",
				description: "",
				kind:        itemInstallDepsGo,
			},
			{
				title:       "Install / redeploy servers",
				description: "",
				kind:        itemInstallWizard,
			},
			{
				title:       "Install/reinstall auto-update monitor",
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
			{
				title:       "Attach to server (CLI)",
				description: "",
				kind:        itemAttachHelp,
			},
			{
				title:       "Debug server (CLI)",
				description: "",
				kind:        itemDebugHelp,
			},
		}
	case tabMaintenance:
		return []menuItem{
			{
				title:       "Update CS2 after Valve update",
				description: "Run SteamCMD on the master install and sync updated game files to all servers.",
				kind:        itemUpdateGameGo,
			},
			{
				title:       "Update plugins on all servers",
				description: "Sync plugins from game_files/ and overrides/ to every server instance.",
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
				title:       "Force update CSM now",
				description: "",
				kind:        itemForceUpdateNow,
			},
			{
				title:       "Extract map thumbnails",
				description: "",
				kind:        itemExtractThumbnailsGo,
			},
		}
	case tabDanger:
		return []menuItem{
			{
				title:       "Wipe all servers and CS2 user",
				description: "",
				kind:        itemCleanupAllGo,
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
		cs2User:           "cs2servermanager",
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
		active:        false,
		cfg:           cfg,
		numServersStr: fmt.Sprintf("%d", cfg.numServers),
		basePortStr:   fmt.Sprintf("%d", cfg.basePort),
		tvPortStr:     fmt.Sprintf("%d", cfg.tvPort),
		cursor:        0,
		windowStart:   0,
		input:         ti,
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

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Track terminal height so scrollable views (like the install wizard
		// and scrollable viewports) can adapt their visible window dynamically.
		m.height = msg.Height

		// Resize the viewport height to make better use of the available space.
		if m.vp.Width != 0 {
			h := msg.Height - 8
			if h < 8 {
				h = 8
			}
			m.vp.Height = h
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:

		// Dedicated handling while inside the install wizard:
		// - q      → exit wizard back to main menu
		// - ctrl+c → double-press to quit CSM
		// - others → forwarded to the huh form / review logic
		if m.view == viewInstallWizard {
			switch msg.String() {
			case "q":
				m.view = viewMain
				m.wizard.active = false
				m.status = "Select an action and press Enter to run it."
				m.confirmQuit = false
				return m, nil
			case "ctrl+c":
				if !m.confirmQuit {
					m.confirmQuit = true
					m.status = "Press Ctrl+C again to quit CSM, or C to continue."
					return m, tea.Batch(cmds...)
				}
				return m, tea.Quit
			default:
				// Any other key breaks a pending quit sequence; require
				// consecutive Ctrl+C presses.
				if m.confirmQuit {
					m.confirmQuit = false
				}
				var cmd tea.Cmd
				m, cmd = m.updateInstallWizard(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

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
				}
				return m, tea.Batch(cmds...)
			default:
				// Any other key breaks a pending quit sequence; require
				// consecutive Q/Ctrl+C presses.
				if m.confirmQuit {
					m.confirmQuit = false
				}
				return m, tea.Batch(cmds...)
			}
		}

		if m.view == viewLogsPrompt {
			var cmd tea.Cmd
			m, cmd = m.updateLogsPromptKey(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

		// While in a scrollable viewport (servers dashboard, logs, MatchZy DB,
		// etc.), delegate navigation keys to the viewport component and use
		// q/Esc to return to the main menu.
		if m.view == viewViewport {
			switch msg.String() {
			case "q", "esc":
				m.view = viewMain
				return m, nil
			default:
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

		// Dedicated handling for the dangerous cleanup confirmation screen.
		if m.view == viewCleanupConfirm {
			switch msg.String() {
			case "enter", "y":
				// Start the irreversible cleanup operation.
				m.view = viewMain
				m.running = true
				m.status = "Danger zone: wiping all servers and CS2 user..."
				m.lastOutput = ""
				cmds = append(cmds, runCleanupAllGo(), m.spin.Tick)
				return m, tea.Batch(cmds...)
			case "n", "esc", "q":
				// Cancel and return to the main menu without running cleanup.
				m.view = viewMain
				m.status = "Select an action and press Enter to run it."
				return m, tea.Batch(cmds...)
			default:
				return m, tea.Batch(cmds...)
			}
		}

		// Simple, generic "action result" view: show the result of the last
		// action and wait for Enter (or q/Esc) before returning to the main
		// menu, similar to a "detail page" on a website.
		if m.view == viewActionResult {
			switch msg.String() {
			case "enter", "q", "esc":
				m.view = viewMain
				m.status = "Select an action and press Enter to run it."
				m.lastOutput = ""
				return m, nil
			default:
				return m, tea.Batch(cmds...)
			}
		}

		// Dedicated handling for the simple "Public IP" detail view. While on
		// this screen, we ignore all keys except Enter/Q/Esc, and those simply
		// return to the main menu without triggering additional actions.
		if m.view == viewPublicIP {
			switch msg.String() {
			case "enter", "q", "esc":
				m.view = viewMain
				m.status = "Select an action and press Enter to run it."
				return m, nil
			default:
				return m, tea.Batch(cmds...)
			}
		}

		switch msg.String() {
		case "ctrl+c":
			// Double-press Ctrl+C to quit CSM when no command is running.
			if !m.confirmQuit {
				m.confirmQuit = true
				m.status = "Press Ctrl+C again to quit CSM, or C to continue."
				return m, tea.Batch(cmds...)
			}
			return m, tea.Quit
		case "q":
			// In viewport mode, q navigates back.
			if m.view == viewViewport {
				m.view = viewMain
				// Keep whatever status we already had; no extra noise.
				return m, nil
			}
			// From the main menu, q quits (double-press).
			if m.view == viewMain {
				if !m.confirmQuit {
					m.confirmQuit = true
					m.status = "Press Q again to quit CSM, or C to continue."
					return m, tea.Batch(cmds...)
				}
				return m, tea.Quit
			}

			return m, tea.Batch(cmds...)
		case "c":
			// Allow users to cancel a pending quit confirmation even when no
			// command is running.
			if m.confirmQuit {
				m.confirmQuit = false
			}
			return m, tea.Batch(cmds...)

		case "left", "h":
			if m.view == viewMain && m.tab > tabSetup {
				m.tab--
				m.rebuildItems()
				m.cursor = 0
				m.menuWindowStart = 0
				// Switching tabs clears the last output/status to reduce
				// cross-page noise.
				m.lastOutput = ""
				m.status = ""
				if m.confirmQuit {
					m.confirmQuit = false
				}
			}
		case "right", "l":
			if m.view == viewMain && m.tab < tabDanger {
				m.tab++
				m.rebuildItems()
				m.cursor = 0
				m.menuWindowStart = 0
				m.lastOutput = ""
				m.status = ""
				if m.confirmQuit {
					m.confirmQuit = false
				}
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				// Moving the selection hides previous results/errors.
				m.lastOutput = ""
				m.status = ""

				// Keep cursor within the visible window when scrolling up.
				windowSize := wizardWindowSizeFor(m.height)
				if m.cursor < m.menuWindowStart {
					m.menuWindowStart = m.cursor
				} else if m.cursor >= m.menuWindowStart+windowSize {
					m.menuWindowStart = m.cursor - windowSize + 1
				}
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				// Moving the selection hides previous results/errors.
				m.lastOutput = ""
				m.status = ""

				// Keep cursor within the visible window when scrolling down.
				windowSize := wizardWindowSizeFor(m.height)
				if m.cursor < m.menuWindowStart {
					m.menuWindowStart = m.cursor
				} else if m.cursor >= m.menuWindowStart+windowSize {
					m.menuWindowStart = m.cursor - windowSize + 1
				}
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
					m.updateProgress.Width = 60
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
				// Reset wizard navigation/editing state; keep existing config
				// values so users can reopen and adjust.
				m.wizard.cursor = 0
				m.wizard.windowStart = 0
				m.wizard.editing = false
				m.wizard.errMsg = ""
				m.status = "Install wizard: configure your servers, then choose Start install."
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
			case itemAttachHelp:
				// Help-only page for CLI-based attach.
				m.detailTitle = "Attach to server (CLI)"
				m.detailContent = "Attach your terminal directly to a server's tmux session.\n\n" +
					"1. Note the server number from the Servers dashboard.\n" +
					"2. Exit CSM.\n" +
					"3. From your shell, run:\n\n" +
					"   csm attach <server>\n\n" +
					"Example for server 1:\n\n" +
					"   csm attach 1\n\n" +
					"Press Enter to return to the main menu."
				m.view = viewActionResult
				m.lastOutput = ""
				m.status = ""
			case itemDebugHelp:
				// Help-only page for CLI-based debug.
				m.detailTitle = "Debug server (CLI)"
				m.detailContent = "Run a CS2 server in foreground debug mode (no tmux) to see all output.\n\n" +
					"1. Note the server number from the Servers dashboard.\n" +
					"2. Exit CSM.\n" +
					"3. From your shell, run:\n\n" +
					"   csm debug <server>\n\n" +
					"Example for server 1:\n\n" +
					"   csm debug 1\n\n" +
					"Press Enter to return to the main menu."
				m.view = viewActionResult
				m.lastOutput = ""
				m.status = ""
			case itemCleanupAllGo:
				// Enter a dedicated confirmation view before running the
				// irreversible cleanup operation.
				m.view = viewCleanupConfirm
				m.status = ""
				m.lastOutput = ""
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
		case itemCleanupAllGo, itemInstallDepsGo:
			// For long-running maintenance actions (cleanup, deps install),
			// show the full log output in a scrollable viewport instead of the
			// compact one-page detail view. This makes it easier to review
			// every step the operation performed.
			title := msg.item.title
			if strings.TrimSpace(title) == "" {
				title = "Action log"
			}

			content := strings.TrimSpace(msg.output)
			if content == "" {
				if msg.err != nil {
					content = fmt.Sprintf("Error: %v", msg.err)
				} else {
					content = "No detailed log output was produced for this action."
				}
			}

			m.view = viewViewport
			m.vpTitle = title

			// Initialize/resize the viewport similarly to viewportFinishedMsg.
			if m.vp.Width == 0 || m.vp.Height == 0 {
				h := 20
				if m.height > 0 {
					h = m.height - 8
					if h < 8 {
						h = 8
					}
				}
				m.vp = viewport.New(80, h)
			} else if m.height > 0 {
				h := m.height - 8
				if h < 8 {
					h = 8
				}
				m.vp.Height = h
			}
			m.vp.SetContent(content)

			m.status = ""
			m.lastOutput = ""
			return m, tea.Batch(cmds...)

		case itemPublicIPGo:
			if msg.err != nil {
				m.publicIP = fmt.Sprintf("Public IP lookup failed:\n\n%v", msg.err)
			} else {
				ip := strings.TrimSpace(msg.output)
				if ip == "" {
					m.publicIP = "Public IP: (not available)"
				} else {
					m.publicIP = ip
				}
			}
			// Switch to a dedicated detail screen that the user dismisses with Enter.
			m.view = viewPublicIP
			m.lastOutput = ""
			m.status = ""

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
				m.detailContent = strings.Join(lines, "\n")
			} else {
				// If the command produced no output, keep the detail view simple.
				m.detailContent = ""
			}

			if msg.err != nil {
				m.detailTitle = fmt.Sprintf("%s (failed)", msg.item.title)
				if m.detailContent == "" {
					m.detailContent = fmt.Sprintf("Command failed: %v", msg.err)
				} else {
					m.detailContent = fmt.Sprintf("%s\n\nError: %v", m.detailContent, msg.err)
				}
			} else {
				if msg.item.title != "" {
					m.detailTitle = msg.item.title
				} else {
					m.detailTitle = "Action finished"
				}
			}

			// Show a dedicated "detail" page for the result and clear the
			// scrolling output section on the main menu.
			m.view = viewActionResult
			m.lastOutput = ""
			m.status = ""
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
				if m.height > 0 {
					if h := m.height - 12; h > maxLines {
						maxLines = h
					}
				}
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

		// Chain to the next step with an updated status line and reset timing.
		switch msg.step {
		case installStepPlugins:
			m.currentInstallStep = installStepBootstrap
			m.installStepStart = time.Now()
			m.installStatusBase = "Step 2/4: Setting up CS2 servers (steamcmd)..."
			m.installExpected = "~10–30 minutes (steamcmd + bootstrap)"
			m.status = m.installStatusBase
			return m, tea.Batch(append(cmds,
				runInstallStep(m.wizard.cfg, installStepBootstrap),
				m.spin.Tick,
				tea.Tick(time.Second, func(time.Time) tea.Msg { return installElapsedMsg{} }),
			)...)
		case installStepBootstrap:
			m.currentInstallStep = installStepMonitor
			m.installStepStart = time.Now()
			m.installStatusBase = "Step 3/4: Configuring auto-update monitor (cron)..."
			m.installExpected = "~10–60 seconds"
			m.status = m.installStatusBase
			return m, tea.Batch(append(cmds,
				runInstallStep(m.wizard.cfg, installStepMonitor),
				m.spin.Tick,
				tea.Tick(time.Second, func(time.Time) tea.Msg { return installElapsedMsg{} }),
			)...)
		case installStepMonitor:
			m.currentInstallStep = installStepStartServers
			m.installStepStart = time.Now()
			m.installStatusBase = "Step 4/4: Starting all servers..."
			m.installExpected = "~10–60 seconds"
			m.status = m.installStatusBase
			return m, tea.Batch(append(cmds,
				runInstallStep(m.wizard.cfg, installStepStartServers),
				m.spin.Tick,
				tea.Tick(time.Second, func(time.Time) tea.Msg { return installElapsedMsg{} }),
			)...)
		case installStepStartServers:
			CancelInstall()
			m.running = false
			m.confirmQuit = false
			m.status = ""

			// Build a summary page the user can review after the wizard
			// completes, then dismiss with Enter.
			lines := []string{
				"Install wizard finished successfully.",
				"",
				fmt.Sprintf("CS2 user       : %s", m.wizard.cfg.cs2User),
				fmt.Sprintf("Servers        : %d", m.wizard.cfg.numServers),
				fmt.Sprintf("Base ports     : game %d, GOTV %d", m.wizard.cfg.basePort, m.wizard.cfg.tvPort),
				fmt.Sprintf("Metamod        : %v", m.wizard.cfg.enableMetamod),
				fmt.Sprintf("Fresh install  : %v", m.wizard.cfg.freshInstall),
				fmt.Sprintf("Update master  : %v", m.wizard.cfg.updateMaster),
				fmt.Sprintf("Update plugins : %v", m.wizard.cfg.updatePlugins),
				"",
				"Per-server summary:",
			}
			for i := 1; i <= m.wizard.cfg.numServers; i++ {
				gamePort := m.wizard.cfg.basePort + (i-1)*10
				tvPort := m.wizard.cfg.tvPort + (i-1)*10
				lines = append(lines,
					fmt.Sprintf("  Server %d: game %d, GOTV %d, Metamod: %v", i, gamePort, tvPort, m.wizard.cfg.enableMetamod))
			}

			m.detailTitle = "Install wizard summary"
			m.detailContent = strings.Join(lines, "\n")
			m.view = viewActionResult
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

	case installElapsedMsg:
		// Live "elapsed vs expected" timing for the current install step. We
		// keep this lightweight and only run while the multi-step install is
		// active.
		if !m.running || m.installStepStart.IsZero() {
			return m, tea.Batch(cmds...)
		}

		elapsed := time.Since(m.installStepStart).Round(time.Second)
		if m.installStatusBase != "" && m.installExpected != "" {
			m.status = fmt.Sprintf("%s (elapsed: %s, expected: %s)", m.installStatusBase, elapsed, m.installExpected)
		}
		return m, tea.Batch(append(cmds, tea.Tick(time.Second, func(time.Time) tea.Msg {
			return installElapsedMsg{}
		}))...)

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

	case progress.FrameMsg:
		// Drive the internal animation of the progress bar while a self-update
		// is in progress.
		if m.selfUpdating {
			pm, cmd := m.updateProgress.Update(msg)
			if p, ok := pm.(progress.Model); ok {
				m.updateProgress = p
			}
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}

	case viewportFinishedMsg:
		// A long-running viewport operation (status, logs, DB verify, etc.)
		// has completed. Show the content in a scrollable viewport.
		m.running = false
		m.view = viewViewport
		m.vpTitle = msg.title

		// Lazily initialize the viewport with a sensible default size, then
		// resize it based on the current terminal height if known.
		if m.vp.Width == 0 || m.vp.Height == 0 {
			h := 20
			if m.height > 0 {
				h = m.height - 8
				if h < 8 {
					h = 8
				}
			}
			m.vp = viewport.New(80, h)
		} else if m.height > 0 {
			h := m.height - 8
			if h < 8 {
				h = 8
			}
			m.vp.Height = h
		}
		m.vp.SetContent(msg.content)

		// If the content is shorter than the available viewport height, shrink
		// the viewport so we don't render a huge block of empty space.
		if m.height > 0 {
			contentLines := strings.Count(msg.content, "\n") + 1
			maxH := m.height - 8
			if maxH < 4 {
				maxH = 4
			}
			if contentLines < maxH {
				if contentLines < 4 {
					contentLines = 4
				}
				m.vp.Height = contentLines
			}
		}

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
		m.updateProgress.Width = 60
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
	case viewActionResult:
		return m.viewActionResult()
	case viewPublicIP:
		return m.viewPublicIP()
	case viewCleanupConfirm:
		return m.viewCleanupConfirm()
	case viewLogsPrompt:
		return m.viewLogsPrompt()
	}

	var b strings.Builder

	// Header
	fmt.Fprintln(&b, headerBorderStyle.Render(titleStyle.Render("CS2 Server Manager")))
	if strings.TrimSpace(m.version) != "" {
		fmt.Fprintln(&b, subtleStyle.Render(fmt.Sprintf("v%s", m.version)))
	}
	fmt.Fprintln(&b)

	// Tab bar. While a long-running command is active, we hide the tabs to
	// reduce visual clutter and focus attention on the status/output.
	if !m.running {
		tabs := []string{"Setup", "Servers", "Maintenance", "Utilities", "DANGER ZONE"}
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
		// Compute which rows should be visible in the current window so the
		// main menu feels scrollable on smaller terminals.
		start := m.menuWindowStart
		if start < 0 {
			start = 0
		}
		windowSize := wizardWindowSizeFor(m.height)
		if windowSize <= 0 {
			windowSize = len(m.items)
		}
		end := start + windowSize
		if end > len(m.items) {
			end = len(m.items)
		}

		for i, item := range m.items {
			if i < start || i >= end {
				continue
			}
			selected := m.cursor == i

			label := item.title
			lineStyle := menuItemStyle
			checkbox := checkboxStyle.Render("[x] ")

			if selected {
				// Always use the standard selected style for the focused row so
				// navigation feels consistent.
				lineStyle = menuSelectedStyle
			} else {
				checkbox = subtleStyle.Render("[ ] ")
			}

			fmt.Fprintln(&b, lineStyle.Render(checkbox+label))
			fmt.Fprintln(&b)
		}
	}

	// Contextual description for the currently selected menu item, similar to
	// the install wizard's field description.
	if !m.running && len(m.items) > 0 && m.cursor >= 0 && m.cursor < len(m.items) {
		selected := m.items[m.cursor]
		var desc string
		switch selected.kind {
		case itemInstallDepsGo:
			desc = "Install system dependencies (tmux, steamcmd, rsync, jq, and others)."
		case itemInstallWizard:
			desc = "Configure server count, ports, and options, then run a full install."
		case itemInstallMonitorGo:
			desc = "Install or redeploy the cron-based CS2 auto-update monitor."
		case itemServersStatusViewport:
			desc = "View running CS2 tmux sessions and server status in a scrollable view."
		case itemLogsViewport:
			desc = "Pick a server number and view its logs in a scrollable viewport."
		case itemStartAllGo:
			desc = "Start every configured CS2 server via tmux."
		case itemStopAllGo:
			desc = "Stop every running CS2 server via tmux."
		case itemRestartAllGo:
			desc = "Restart all CS2 servers via tmux."
		case itemUpdateGameGo:
			desc = "Run SteamCMD to update the master CS2 install and sync updated game files to all servers."
		case itemDeployPluginsGo:
			desc = "Download the latest plugin bundle, then sync plugins/configs to all servers."
		case itemMatchzyDBViewport:
			desc = "Verify and (if needed) repair the MatchZy MySQL database in a scrollable view."
		case itemPublicIPGo:
			desc = "Resolve and show the server's public IP on a dedicated screen."
		case itemForceUpdateNow:
			desc = "Bypass the cache and check GitHub for a newer CSM version."
		case itemExtractThumbnailsGo:
			desc = "Run the VPK/thumbnails pipeline and write PNGs into map_thumbnails/."
		case itemCleanupAllGo:
			desc = "Wipe all servers and the dedicated CS2 user; use only when you want a full reset."
		case itemAttachHelp:
			desc = "Attach your terminal to a server's tmux session via the CLI:  csm attach <server>."
		case itemDebugHelp:
			desc = "Run a server in foreground debug mode via the CLI:  csm debug <server>."
		}
		if strings.TrimSpace(desc) != "" {
			fmt.Fprintln(&b, subtleStyle.Render(desc))
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

	return mainStyle.Render("\n" + b.String() + "\n")
}

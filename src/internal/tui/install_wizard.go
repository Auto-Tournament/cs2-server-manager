package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
)

// applyWizardNumericFields parses the string fields coming from the wizard
// into the concrete numeric fields on the config.
func (w *installWizard) applyWizardNumericFields() {
	if n, err := strconv.Atoi(strings.TrimSpace(w.numServersStr)); err == nil && n > 0 {
		w.cfg.numServers = n
	}
	if p, err := strconv.Atoi(strings.TrimSpace(w.basePortStr)); err == nil && p > 0 {
		w.cfg.basePort = p
	}
	if p, err := strconv.Atoi(strings.TrimSpace(w.tvPortStr)); err == nil && p > 0 {
		w.cfg.tvPort = p
	}
}

// Wizard field indices for the one-page install wizard view.
const (
	wizardFieldDBMode = iota
	wizardFieldNumServers
	wizardFieldBasePort
	wizardFieldTVPort
	wizardFieldCS2User
	wizardFieldMetamod
	wizardFieldFreshInstall
	wizardFieldUpdateMaster
	wizardFieldUpdatePlugins
	wizardFieldRCONPassword
	wizardFieldStartInstall
	wizardFieldCancel
	wizardFieldCount
)

// wizardWindowSize is the default number of wizard rows shown at once when we
// don't know the terminal height yet. Once we receive a WindowSizeMsg, the
// install wizard will compute a dynamic window size based on available rows.
const wizardWindowSize = 10

// wizardWindowSizeFor computes how many wizard rows to show based on the
// current terminal height. We account for header, spacing and the bottom
// description so the window only scrolls when it actually needs to.
func wizardWindowSizeFor(height int) int {
	if height <= 0 {
		return wizardWindowSize
	}

	// Rough layout budget:
	// - 4–5 lines for header + spacing
	// - 2–3 lines for description / error at the bottom
	// - Each wizard row uses ~2 lines (label + blank)
	rowsForItems := (height - 8) / 2
	if rowsForItems < 4 {
		rowsForItems = 4
	}
	if rowsForItems > wizardFieldCount {
		rowsForItems = wizardFieldCount
	}
	return rowsForItems
}

// validateAll validates the wizard fields before starting the install.
func (w *installWizard) validateAll() error {
	// Number of servers
	if strings.TrimSpace(w.numServersStr) == "" {
		return fmt.Errorf("number of servers is required")
	}
	if n, err := strconv.Atoi(strings.TrimSpace(w.numServersStr)); err != nil || n <= 0 {
		return fmt.Errorf("enter a positive integer for number of servers")
	}

	// Base ports
	if strings.TrimSpace(w.basePortStr) == "" {
		return fmt.Errorf("base game port is required")
	}
	if p, err := strconv.Atoi(strings.TrimSpace(w.basePortStr)); err != nil || p <= 0 {
		return fmt.Errorf("enter a valid base game port")
	}

	if strings.TrimSpace(w.tvPortStr) == "" {
		return fmt.Errorf("base GOTV port is required")
	}
	if p, err := strconv.Atoi(strings.TrimSpace(w.tvPortStr)); err != nil || p <= 0 {
		return fmt.Errorf("enter a valid base GOTV port")
	}

	// CS2 user
	name := strings.TrimSpace(w.cfg.cs2User)
	if name == "" {
		return fmt.Errorf("CS2 user is required")
	}
	current := os.Getenv("USER")
	sudoUser := os.Getenv("SUDO_USER")
	if name == "root" || name == current || name == sudoUser {
		if current == "" {
			current = "your login"
		}
		return fmt.Errorf("please choose a dedicated service user (e.g. \"cs2\"), not your own login user (%q)", current)
	}

	return nil
}

func (m model) viewInstallWizard() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("Install / redeploy servers")) +
		"\n" +
		headerBorderStyle.Render("Configure your servers, then choose Start install.")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	// Ensure numeric string fields have sensible defaults for display.
	if strings.TrimSpace(m.wizard.numServersStr) == "" && m.wizard.cfg.numServers > 0 {
		m.wizard.numServersStr = fmt.Sprintf("%d", m.wizard.cfg.numServers)
	}
	if strings.TrimSpace(m.wizard.basePortStr) == "" && m.wizard.cfg.basePort > 0 {
		m.wizard.basePortStr = fmt.Sprintf("%d", m.wizard.cfg.basePort)
	}
	if strings.TrimSpace(m.wizard.tvPortStr) == "" && m.wizard.cfg.tvPort > 0 {
		m.wizard.tvPortStr = fmt.Sprintf("%d", m.wizard.cfg.tvPort)
	}

	// Compute which rows should be visible in the current window so the wizard
	// feels scrollable on smaller terminals.
	start := m.wizard.windowStart
	if start < 0 {
		start = 0
	}
	// Derive window size dynamically from the terminal height when available.
	windowSize := wizardWindowSizeFor(m.height)
	end := start + windowSize
	if end > wizardFieldCount {
		end = wizardFieldCount
	}

	visible := func(index int) bool {
		return index >= start && index < end
	}

	// Helper to render a single row with optional selection highlighting.
	renderRow := func(index int, label, value string) {
		if !visible(index) {
			return
		}
		selected := index == m.wizard.cursor
		style := menuItemStyle
		if selected {
			style = menuSelectedStyle
		}
		line := fmt.Sprintf("%-20s %s", label, value)
		fmt.Fprintln(&b, style.Render(line))
		fmt.Fprintln(&b)
	}

	// DB mode row.
	dbLabel := "Docker-managed MySQL (recommended)"
	if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
		dbLabel = "External MySQL (no Docker provisioning)"
	}
	renderRow(wizardFieldDBMode, "MatchZy DB:", dbLabel)

	// Numeric / text rows.
	numServersVal := m.wizard.numServersStr
	if m.wizard.cursor == wizardFieldNumServers && m.wizard.editing {
		numServersVal = m.wizard.input.View()
	}
	renderRow(wizardFieldNumServers, "Number of servers:", numServersVal)

	basePortVal := m.wizard.basePortStr
	if m.wizard.cursor == wizardFieldBasePort && m.wizard.editing {
		basePortVal = m.wizard.input.View()
	}
	renderRow(wizardFieldBasePort, "Base game port:", basePortVal)

	tvPortVal := m.wizard.tvPortStr
	if m.wizard.cursor == wizardFieldTVPort && m.wizard.editing {
		tvPortVal = m.wizard.input.View()
	}
	renderRow(wizardFieldTVPort, "Base GOTV port:", tvPortVal)

	cs2UserVal := m.wizard.cfg.cs2User
	if m.wizard.cursor == wizardFieldCS2User && m.wizard.editing {
		cs2UserVal = m.wizard.input.View()
	}
	renderRow(wizardFieldCS2User, "CS2 user:", cs2UserVal)

	// Boolean rows.
	boolLabel := func(v bool) string {
		if v {
			return "[x] Yes"
		}
		return "[ ] No"
	}
	renderRow(wizardFieldMetamod, "Enable Metamod:", boolLabel(m.wizard.cfg.enableMetamod))
	renderRow(wizardFieldFreshInstall, "Fresh install:", boolLabel(m.wizard.cfg.freshInstall))
	renderRow(wizardFieldUpdateMaster, "Update master:", boolLabel(m.wizard.cfg.updateMaster))
	renderRow(wizardFieldUpdatePlugins, "Update plugins:", boolLabel(m.wizard.cfg.updatePlugins))

	// RCON password row (do not echo anything special; keep it simple).
	rconVal := m.wizard.cfg.rconPassword
	if m.wizard.cursor == wizardFieldRCONPassword && m.wizard.editing {
		rconVal = m.wizard.input.View()
	}
	renderRow(wizardFieldRCONPassword, "RCON password:", rconVal)

	// Action rows: Start install / Cancel.
	startLabel := "Start install"
	cancelLabel := "Cancel"
	renderRow(wizardFieldStartInstall, "", startLabel)
	renderRow(wizardFieldCancel, "", cancelLabel)

	// Contextual description at the bottom for the currently selected field.
	var desc string
	switch m.wizard.cursor {
	case wizardFieldDBMode:
		desc = "MatchZy DB: choose Docker-managed MySQL (recommended) or an existing external MySQL server."
	case wizardFieldNumServers:
		desc = "Number of servers: how many CS2 game servers to create on this machine."
	case wizardFieldBasePort:
		desc = "Base game port: first game port to use; additional servers use consecutive ports."
	case wizardFieldTVPort:
		desc = "Base GOTV port: first GOTV port to use; additional servers use consecutive ports."
	case wizardFieldCS2User:
		desc = "CS2 user: Linux user account that owns the CS2 files and runs the servers."
	case wizardFieldMetamod:
		desc = "Enable Metamod: install Metamod so you can run SourceMod and other plugins."
	case wizardFieldFreshInstall:
		desc = "Fresh install: delete any existing CS2 server directories before installing."
	case wizardFieldUpdateMaster:
		desc = "Update master: run SteamCMD to update the master CS2 install before deploying servers."
	case wizardFieldUpdatePlugins:
		desc = "Update plugins: download the latest plugins before installing or redeploying servers."
	case wizardFieldRCONPassword:
		desc = "RCON password: password applied to all servers (you can change per-server later)."
	case wizardFieldStartInstall:
		desc = "Start install: run the full install with the settings above."
	case wizardFieldCancel:
		desc = "Cancel: return to the main menu without installing."
	}

	if strings.TrimSpace(desc) != "" {
		fmt.Fprintln(&b, subtleStyle.Render(desc))
		fmt.Fprintln(&b)
	}

	// Optional inline error at the bottom of the wizard.
	if strings.TrimSpace(m.wizard.errMsg) != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	}

	return b.String()
}

// updateInstallWizard handles navigation and editing for the one-page wizard.
func (m model) updateInstallWizard(msg tea.Msg) (model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		if m.wizard.cursor > 0 {
			m.wizard.cursor--
			m.wizard.editing = false
			m.wizard.errMsg = ""
			// Keep cursor within the visible window when scrolling up.
			windowSize := wizardWindowSizeFor(m.height)
			if m.wizard.cursor < m.wizard.windowStart {
				m.wizard.windowStart = m.wizard.cursor
			} else if m.wizard.cursor >= m.wizard.windowStart+windowSize {
				m.wizard.windowStart = m.wizard.cursor - windowSize + 1
			}
		}
		return m, nil
	case "down", "j":
		if m.wizard.cursor < wizardFieldCount-1 {
			m.wizard.cursor++
			m.wizard.editing = false
			m.wizard.errMsg = ""
			// Keep cursor within the visible window when scrolling down.
			windowSize := wizardWindowSizeFor(m.height)
			if m.wizard.cursor < m.wizard.windowStart {
				m.wizard.windowStart = m.wizard.cursor
			} else if m.wizard.cursor >= m.wizard.windowStart+windowSize {
				m.wizard.windowStart = m.wizard.cursor - windowSize + 1
			}
		}
		return m, nil
	case "tab":
		m.wizard.cursor = (m.wizard.cursor + 1) % wizardFieldCount
		m.wizard.editing = false
		m.wizard.errMsg = ""
		windowSize := wizardWindowSizeFor(m.height)
		if m.wizard.cursor < m.wizard.windowStart {
			m.wizard.windowStart = m.wizard.cursor
		} else if m.wizard.cursor >= m.wizard.windowStart+windowSize {
			m.wizard.windowStart = m.wizard.cursor - windowSize + 1
		}
		return m, nil
	case "shift+tab":
		m.wizard.cursor--
		if m.wizard.cursor < 0 {
			m.wizard.cursor = wizardFieldCount - 1
		}
		m.wizard.editing = false
		m.wizard.errMsg = ""
		windowSize := wizardWindowSizeFor(m.height)
		if m.wizard.cursor < m.wizard.windowStart {
			m.wizard.windowStart = m.wizard.cursor
		} else if m.wizard.cursor >= m.wizard.windowStart+windowSize {
			m.wizard.windowStart = m.wizard.cursor - windowSize + 1
		}
		return m, nil
	case "esc":
		if m.wizard.editing {
			// Cancel the current edit, keep previous value.
			m.wizard.editing = false
			m.wizard.errMsg = ""
			return m, nil
		}
		// Esc from the wizard view behaves like cancel.
		m.wizard.active = false
		m.view = viewMain
		m.status = "Select an action and press Enter to run it."
		return m, nil
	case "left":
		// Arrow-left: decrement numeric fields and toggle simple options when not
		// in edit mode.
		if !m.wizard.editing {
			switch m.wizard.cursor {
			case wizardFieldNumServers:
				if n, err := strconv.Atoi(strings.TrimSpace(m.wizard.numServersStr)); err == nil && n > 1 {
					m.wizard.numServersStr = fmt.Sprintf("%d", n-1)
					m.wizard.errMsg = ""
				}
			case wizardFieldBasePort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.basePortStr)); err == nil && p > 1 {
					m.wizard.basePortStr = fmt.Sprintf("%d", p-1)
					m.wizard.errMsg = ""
				}
			case wizardFieldTVPort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.tvPortStr)); err == nil && p > 1 {
					m.wizard.tvPortStr = fmt.Sprintf("%d", p-1)
					m.wizard.errMsg = ""
				}
			case wizardFieldDBMode:
				// Left/right both toggle DB mode between docker and external.
				if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
					m.wizard.cfg.dbMode = "docker"
				} else {
					m.wizard.cfg.dbMode = "external"
				}
				m.wizard.errMsg = ""
			case wizardFieldMetamod:
				m.wizard.cfg.enableMetamod = !m.wizard.cfg.enableMetamod
				m.wizard.errMsg = ""
			case wizardFieldFreshInstall:
				m.wizard.cfg.freshInstall = !m.wizard.cfg.freshInstall
				m.wizard.errMsg = ""
			case wizardFieldUpdateMaster:
				m.wizard.cfg.updateMaster = !m.wizard.cfg.updateMaster
				m.wizard.errMsg = ""
			case wizardFieldUpdatePlugins:
				m.wizard.cfg.updatePlugins = !m.wizard.cfg.updatePlugins
				m.wizard.errMsg = ""
			}
		}
		return m, nil
	case "right":
		// Arrow-right: increment numeric fields and toggle simple options when
		// not in edit mode.
		if !m.wizard.editing {
			switch m.wizard.cursor {
			case wizardFieldNumServers:
				if n, err := strconv.Atoi(strings.TrimSpace(m.wizard.numServersStr)); err == nil {
					m.wizard.numServersStr = fmt.Sprintf("%d", n+1)
					m.wizard.errMsg = ""
				}
			case wizardFieldBasePort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.basePortStr)); err == nil {
					m.wizard.basePortStr = fmt.Sprintf("%d", p+1)
					m.wizard.errMsg = ""
				}
			case wizardFieldTVPort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.tvPortStr)); err == nil {
					m.wizard.tvPortStr = fmt.Sprintf("%d", p+1)
					m.wizard.errMsg = ""
				}
			case wizardFieldDBMode:
				if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
					m.wizard.cfg.dbMode = "docker"
				} else {
					m.wizard.cfg.dbMode = "external"
				}
				m.wizard.errMsg = ""
			case wizardFieldMetamod:
				m.wizard.cfg.enableMetamod = !m.wizard.cfg.enableMetamod
				m.wizard.errMsg = ""
			case wizardFieldFreshInstall:
				m.wizard.cfg.freshInstall = !m.wizard.cfg.freshInstall
				m.wizard.errMsg = ""
			case wizardFieldUpdateMaster:
				m.wizard.cfg.updateMaster = !m.wizard.cfg.updateMaster
				m.wizard.errMsg = ""
			case wizardFieldUpdatePlugins:
				m.wizard.cfg.updatePlugins = !m.wizard.cfg.updatePlugins
				m.wizard.errMsg = ""
			}
		}
		return m, nil
	}

	// When editing a text field, route keys into the shared text input.
	if m.wizard.editing {
		switch key.String() {
		case "enter":
			// Commit current input into the appropriate field.
			val := strings.TrimSpace(m.wizard.input.Value())
			switch m.wizard.cursor {
			case wizardFieldNumServers:
				m.wizard.numServersStr = val
			case wizardFieldBasePort:
				m.wizard.basePortStr = val
			case wizardFieldTVPort:
				m.wizard.tvPortStr = val
			case wizardFieldCS2User:
				m.wizard.cfg.cs2User = val
			case wizardFieldRCONPassword:
				m.wizard.cfg.rconPassword = val
			}
			m.wizard.editing = false
			m.wizard.errMsg = ""
			return m, nil
		case "ctrl+c":
			// Let the outer handler deal with quit confirmation.
			return m, nil
		default:
			var cmd tea.Cmd
			m.wizard.input, cmd = m.wizard.input.Update(key)
			return m, cmd
		}
	}

	// Not currently editing: handle toggles and actions.
	switch key.String() {
	case "enter", " ":
		switch m.wizard.cursor {
		case wizardFieldDBMode:
			if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
				m.wizard.cfg.dbMode = "docker"
			} else {
				m.wizard.cfg.dbMode = "external"
			}
			return m, nil
		case wizardFieldMetamod:
			m.wizard.cfg.enableMetamod = !m.wizard.cfg.enableMetamod
			return m, nil
		case wizardFieldFreshInstall:
			m.wizard.cfg.freshInstall = !m.wizard.cfg.freshInstall
			return m, nil
		case wizardFieldUpdateMaster:
			m.wizard.cfg.updateMaster = !m.wizard.cfg.updateMaster
			return m, nil
		case wizardFieldUpdatePlugins:
			m.wizard.cfg.updatePlugins = !m.wizard.cfg.updatePlugins
			return m, nil
		case wizardFieldNumServers, wizardFieldBasePort, wizardFieldTVPort, wizardFieldCS2User, wizardFieldRCONPassword:
			// Begin editing the selected text/numeric field.
			m.wizard.editing = true
			m.wizard.errMsg = ""
			var initial string
			switch m.wizard.cursor {
			case wizardFieldNumServers:
				initial = m.wizard.numServersStr
			case wizardFieldBasePort:
				initial = m.wizard.basePortStr
			case wizardFieldTVPort:
				initial = m.wizard.tvPortStr
			case wizardFieldCS2User:
				initial = m.wizard.cfg.cs2User
			case wizardFieldRCONPassword:
				initial = m.wizard.cfg.rconPassword
			}
			m.wizard.input.SetValue(initial)
			m.wizard.input.CursorEnd()
			return m, nil
		case wizardFieldStartInstall:
			// Validate before starting the multi-step install.
			if err := m.wizard.validateAll(); err != nil {
				m.wizard.errMsg = err.Error()
				return m, nil
			}
			// Parse numeric fields into cfg.
			m.wizard.applyWizardNumericFields()

			m.wizard.active = false
			m.view = viewMain
			m.running = true
			m.status = "Step 1/4: Preparing plugin update..."
			m.lastOutput = ""

			cfg := m.wizard.cfg
			return m, tea.Batch(runInstallStep(cfg, installStepPlugins), m.spin.Tick)
		case wizardFieldCancel:
			m.wizard.active = false
			m.view = viewMain
			m.status = "Select an action and press Enter to run it."
			return m, nil
		}
	}

	return m, nil
}

// runInstallStep performs a single phase of the install wizard. Each call
// returns an installStepMsg so the TUI can update status/output and decide
// which step to run next.
func runInstallStep(cfg installConfig, step installStep) tea.Cmd {
	return func() tea.Msg {
		var logs []string

		switch step {
		case installStepPlugins:
			if cfg.updatePlugins {
				logs = append(logs, "[1/4] Downloading latest plugins...")

				// Stream plugin update progress by mirroring logs into a temp
				// file that a background goroutine tails.
				logPath := filepath.Join(os.TempDir(), "csm-plugins.log")
				_ = os.Remove(logPath)

				done := make(chan struct{})
				defer close(done)

				go tailInstallLog(logPath, done)

				_ = os.Setenv("CSM_PLUGINS_LOG", logPath)
				defer os.Unsetenv("CSM_PLUGINS_LOG")

				if out, err := csm.UpdatePlugins(); err != nil {
					if out != "" {
						logs = append(logs, out)
					}
					logs = append(logs, fmt.Sprintf("Plugin download failed: %v", err))
					return installStepMsg{
						step: installStepPlugins,
						out:  strings.Join(logs, "\n"),
						err:  err,
					}
				} else if out != "" {
					logs = append(logs, out)
				}
				logs = append(logs, "[1/4] Plugin update finished.")
			} else {
				logs = append(logs, "[1/4] Skipping plugin download (user disabled update plugins).")
			}
			return installStepMsg{
				step: installStepPlugins,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}

		case installStepBootstrap:
			logs = append(logs, "[2/4] Setting up CS2 servers (this may take several minutes)...")

			// Derive MatchZy Docker behaviour from dbMode.
			cfg.matchzySkipDocker = strings.EqualFold(cfg.dbMode, "external")
			bcfg := csm.BootstrapConfig{
				CS2User:           cfg.cs2User,
				NumServers:        cfg.numServers,
				BaseGamePort:      cfg.basePort,
				BaseTVPort:        cfg.tvPort,
				EnableMetamod:     cfg.enableMetamod,
				FreshInstall:      cfg.freshInstall,
				UpdateMaster:      cfg.updateMaster,
				RCONPassword:      cfg.rconPassword,
				MatchzySkipDocker: cfg.matchzySkipDocker,
			}

			// Stream bootstrap progress by mirroring logs into a temp file that
			// a background goroutine tails, sending installLogTickMsg updates.
			logPath := filepath.Join(os.TempDir(), "csm-bootstrap.log")
			_ = os.Remove(logPath)

			// Signal goroutine when we're done (success or failure).
			done := make(chan struct{})
			defer close(done)

			// Start log tailer in the background.
			go tailInstallLog(logPath, done)

			// Configure Bootstrap to mirror logs into the temp file.
			_ = os.Setenv("CSM_BOOTSTRAP_LOG", logPath)
			defer os.Unsetenv("CSM_BOOTSTRAP_LOG")

			// Use a cancellable context so steamcmd and the rest of bootstrap
			// are terminated if the user quits the TUI mid-install.
			ctx, cancel := context.WithCancel(context.Background())
			SetInstallCancel(cancel)
			defer CancelInstall()

			if out, err := csm.BootstrapWithContext(ctx, bcfg); err != nil {
				if out != "" {
					logs = append(logs, out)
				}
				logs = append(logs, fmt.Sprintf("Bootstrap failed: %v", err))
				return installStepMsg{
					step: installStepBootstrap,
					out:  strings.Join(logs, "\n"),
					err:  err,
				}
			} else if out != "" {
				logs = append(logs, out)
			}
			logs = append(logs, "[2/4] CS2 servers setup finished.")
			return installStepMsg{
				step: installStepBootstrap,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}

		case installStepMonitor:
			logs = append(logs, "[3/4] Configuring auto-update monitor (cron job)...")
			if out, err := csm.InstallAutoUpdateCron(""); err != nil {
				if out != "" {
					logs = append(logs, out)
				}
				logs = append(logs, fmt.Sprintf("Auto-update monitor setup failed: %v", err))
				return installStepMsg{
					step: installStepMonitor,
					out:  strings.Join(logs, "\n"),
					err:  err,
				}
			} else if out != "" {
				logs = append(logs, out)
			}
			logs = append(logs, "[3/4] Auto-update monitor configured.")
			return installStepMsg{
				step: installStepMonitor,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}

		case installStepStartServers:
			logs = append(logs, "[4/4] Starting all servers...")
			manager, err := csm.NewTmuxManager()
			if err != nil {
				logs = append(logs, fmt.Sprintf("Failed to initialize tmux manager: %v", err))
				return installStepMsg{
					step: installStepStartServers,
					out:  strings.Join(logs, "\n"),
					err:  err,
				}
			}
			if err := manager.StartAll(); err != nil {
				logs = append(logs, fmt.Sprintf("Failed to start servers: %v", err))
				return installStepMsg{
					step: installStepStartServers,
					out:  strings.Join(logs, "\n"),
					err:  err,
				}
			}
			logs = append(logs, "[4/4] All servers started via tmux.")
			return installStepMsg{
				step: installStepStartServers,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}
		}

		// Should not happen; treat as no-op.
		return installStepMsg{
			step: step,
			out:  "",
			err:  nil,
		}
	}
}

// tailInstallLog periodically reads a log file and sends the last few lines
// back into the TUI as installLogTickMsg values so users can see live progress
// while long-running steps run.
func tailInstallLog(path string, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(data))
			if text == "" {
				continue
			}
			lines := strings.Split(text, "\n")
			if len(lines) > 4 {
				lines = lines[len(lines)-4:]
			}
			send(installLogTickMsg{lines: strings.Join(lines, "\n")})
		}
	}
}

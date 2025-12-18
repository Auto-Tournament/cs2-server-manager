package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	if p, err := strconv.Atoi(strings.TrimSpace(w.dbPortStr)); err == nil && p > 0 {
		w.cfg.externalDBPort = p
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
	wizardFieldInstallMonitor
	wizardFieldRCONPassword
	wizardFieldDBExternalHost
	wizardFieldDBExternalPort
	wizardFieldDBExternalName
	wizardFieldDBExternalUser
	wizardFieldDBExternalPassword
	wizardFieldStartInstall
	wizardFieldCancel
	wizardFieldCount
)

// isFieldActive reports whether the given wizard field index should be focusable
// in the current configuration. External DB fields are only active when the
// MatchZy DB mode is set to "external"; in all other cases they are skipped
// during navigation so the cursor never lands on invisible rows.
func (w *installWizard) isFieldActive(index int) bool {
	if index >= wizardFieldDBExternalHost && index <= wizardFieldDBExternalPassword {
		return strings.EqualFold(w.cfg.dbMode, "external")
	}
	return index >= 0 && index < wizardFieldCount
}

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

// estimateDiskSpace returns total and free space (in GB) for the filesystem
// that will hold /home/<cs2User>. If cs2User is empty or the check fails, it
// falls back to "/" and may return ok=false on error.
func estimateDiskSpace(cs2User string) (totalGB, freeGB float64, ok bool) {
	path := "/"
	if strings.TrimSpace(cs2User) != "" {
		path = filepath.Join("/home", cs2User)
	}

	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}

	totalBytes := float64(st.Blocks) * float64(st.Bsize)
	freeBytes := float64(st.Bavail) * float64(st.Bsize)

	const gb = 1024 * 1024 * 1024
	return totalBytes / gb, freeBytes / gb, true
}

// existingInstallLayout inspects /home/<cs2User> to detect whether a master
// install already exists and how many server-* directories are present. It is
// used by the wizard's disk estimate so we can approximate how much of the
// "required" space is already occupied by an existing install and focus on
// *additional* space needed for the requested layout.
func existingInstallLayout(cs2User string) (hasMaster bool, numServers int) {
	user := strings.TrimSpace(cs2User)
	if user == "" {
		user = csm.DefaultCS2User
	}

	home := filepath.Join("/home", user)
	if fi, err := os.Stat(filepath.Join(home, "master-install")); err == nil && fi.IsDir() {
		hasMaster = true
	}

	entries, err := os.ReadDir(home)
	if err != nil {
		return hasMaster, 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "server-") {
			continue
		}
		count++
	}
	return hasMaster, count
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

	// RCON password
	if strings.TrimSpace(w.cfg.rconPassword) == "" {
		return fmt.Errorf("RCON password is required")
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
	renderRow(wizardFieldInstallMonitor, "Install auto-update:", boolLabel(m.wizard.cfg.installMonitor))

	// RCON password row (do not echo anything special; keep it simple).
	rconVal := m.wizard.cfg.rconPassword
	if m.wizard.cursor == wizardFieldRCONPassword && m.wizard.editing {
		rconVal = m.wizard.input.View()
	}
	renderRow(wizardFieldRCONPassword, "RCON password:", rconVal)

	// External DB configuration (used when MatchZy DB is set to external).
	if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
		dbHostVal := m.wizard.cfg.externalDBHost
		if m.wizard.cursor == wizardFieldDBExternalHost && m.wizard.editing {
			dbHostVal = m.wizard.input.View()
		}
		renderRow(wizardFieldDBExternalHost, "DB host (external):", dbHostVal)

		dbPortVal := m.wizard.dbPortStr
		if strings.TrimSpace(dbPortVal) == "" && m.wizard.cfg.externalDBPort > 0 {
			dbPortVal = fmt.Sprintf("%d", m.wizard.cfg.externalDBPort)
		}
		if m.wizard.cursor == wizardFieldDBExternalPort && m.wizard.editing {
			dbPortVal = m.wizard.input.View()
		}
		renderRow(wizardFieldDBExternalPort, "DB port (external):", dbPortVal)

		dbNameVal := m.wizard.cfg.externalDBName
		if m.wizard.cursor == wizardFieldDBExternalName && m.wizard.editing {
			dbNameVal = m.wizard.input.View()
		}
		renderRow(wizardFieldDBExternalName, "DB name (external):", dbNameVal)

		dbUserVal := m.wizard.cfg.externalDBUser
		if m.wizard.cursor == wizardFieldDBExternalUser && m.wizard.editing {
			dbUserVal = m.wizard.input.View()
		}
		renderRow(wizardFieldDBExternalUser, "DB user (external):", dbUserVal)

		dbPassVal := m.wizard.cfg.externalDBPassword
		if m.wizard.cursor == wizardFieldDBExternalPassword && m.wizard.editing {
			dbPassVal = m.wizard.input.View()
		}
		renderRow(wizardFieldDBExternalPassword, "DB password (external):", dbPassVal)
	}

	// Action rows: Start install / Cancel.
	startLabel := "Start install"
	cancelLabel := "Cancel"
	renderRow(wizardFieldStartInstall, "", startLabel)
	renderRow(wizardFieldCancel, "", cancelLabel)

	// Contextual description at the bottom for the currently selected field.
	var desc string
	switch m.wizard.cursor {
	case wizardFieldDBMode:
		desc = "Choose Docker-managed MySQL (recommended) or an existing external MySQL server."
	case wizardFieldNumServers:
		desc = "How many CS2 game servers to create on this machine."
	case wizardFieldBasePort:
		desc = "First game port to use; additional servers use consecutive ports."
	case wizardFieldTVPort:
		desc = "First GOTV port to use; additional servers use consecutive ports."
	case wizardFieldCS2User:
		desc = "Dedicated account for CS2 Server Manager. Danger zone cleanup deletes this user and its home; don't use it for anything else."
	case wizardFieldMetamod:
		desc = "Install Metamod so you can run SourceMod and other plugins."
	case wizardFieldFreshInstall:
		if m.wizard.cfg.freshInstall {
			desc = "Perform a full fresh install: delete existing master install, MatchZy DB container/volume, and all server-* directories before recreating everything."
		} else {
			desc = "Reuse the existing master install, MatchZy DB, and servers; only update what is needed."
		}
	case wizardFieldUpdateMaster:
		desc = "Run SteamCMD to update the master CS2 install before deploying servers."
	case wizardFieldUpdatePlugins:
		desc = "Download the latest plugins before installing or redeploying servers."
	case wizardFieldInstallMonitor:
		desc = "Install a cron-based auto-update monitor that keeps servers up to date when the AutoUpdater plugin shuts them down."
	case wizardFieldRCONPassword:
		desc = "Password applied to all servers (you can change per-server later)."
	case wizardFieldDBExternalHost:
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			desc = "External MySQL host for MatchZy (used when MatchZy DB is set to external)."
		}
	case wizardFieldDBExternalPort:
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			desc = "External MySQL port for MatchZy (typically 3306)."
		}
	case wizardFieldDBExternalName:
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			desc = "External MySQL database name for MatchZy (e.g. \"matchzy\")."
		}
	case wizardFieldDBExternalUser:
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			desc = "External MySQL username for MatchZy."
		}
	case wizardFieldDBExternalPassword:
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			desc = "External MySQL password for MatchZy."
		}
	case wizardFieldStartInstall:
		desc = "Run the full install with the settings above."
	case wizardFieldCancel:
		desc = "Return to the main menu without installing."
	}

	if strings.TrimSpace(desc) != "" {
		fmt.Fprintln(&b, subtleStyle.Render(desc))
	}

	// Rough disk space estimate based on a master install and N servers.
	const masterGB = csm.DefaultMasterDiskGB
	const perServerGB = csm.DefaultPerServerDiskGB

	numServers := m.wizard.cfg.numServers
	if n, err := strconv.Atoi(strings.TrimSpace(m.wizard.numServersStr)); err == nil && n > 0 {
		numServers = n
	}
	if numServers <= 0 {
		numServers = 1
	}

	totalRequiredGB := masterGB + perServerGB*float64(numServers)

	// Estimate how much of this footprint is already present on disk so we can
	// show a more realistic "additional space needed" number instead of
	// double-counting an existing install.
	hasMaster, existingServers := existingInstallLayout(m.wizard.cfg.cs2User)

	var alreadyGB float64
	if hasMaster && !m.wizard.cfg.freshInstall {
		alreadyGB += masterGB
	}
	if existingServers > 0 && !m.wizard.cfg.freshInstall {
		// Only subtract up to the number of servers we plan to have; if we're
		// shrinking, we won't keep more than numServers servers after the run.
		if existingServers > numServers {
			existingServers = numServers
		}
		alreadyGB += perServerGB * float64(existingServers)
	}

	if m.wizard.cfg.freshInstall && (hasMaster || existingServers > 0) {
		// For a full fresh install we will delete the existing master and
		// server-* directories before recreating them, so the on-disk footprint
		// stays roughly the same. Model this as "already present" to avoid
		// double-counting.
		alreadyGB = totalRequiredGB
	}

	additionalGB := totalRequiredGB - alreadyGB
	if additionalGB < 0 {
		additionalGB = 0
	}

	// Try to estimate disk space on the filesystem that will hold /home/<cs2User>.
	baseLine := fmt.Sprintf("Estimated total footprint for this layout: ~%.1f GB (master + %d server(s)).", totalRequiredGB, numServers)
	additionalLine := fmt.Sprintf("Estimated additional space needed: ~%.1f GB.", additionalGB)

	_, freeGB, ok := estimateDiskSpace(m.wizard.cfg.cs2User)
	if ok {
		freeLine := fmt.Sprintf("Currently available: ~%.1f GB free.", freeGB)
		afterGB := freeGB - additionalGB
		if afterGB >= 0 {
			afterLine := fmt.Sprintf("Approximate free space after install: ~%.1f GB.", afterGB)
			fmt.Fprintln(&b, subtleStyle.Render(freeLine))
			fmt.Fprintln(&b, subtleStyle.Render(baseLine))
			fmt.Fprintln(&b, subtleStyle.Render(additionalLine))
			fmt.Fprintln(&b, subtleStyle.Render(afterLine))
		} else {
			needed := -afterGB
			warnLine := fmt.Sprintf("Warning: estimated additional space is short by ~%.1f GB.", needed)
			fmt.Fprintln(&b, subtleStyle.Render(freeLine))
			fmt.Fprintln(&b, subtleStyle.Render(baseLine))
			fmt.Fprintln(&b, warningStyle.Render(additionalLine))
			fmt.Fprintln(&b, warningStyle.Render(warnLine))
		}
	} else {
		fmt.Fprintln(&b, subtleStyle.Render(baseLine))
		fmt.Fprintln(&b, subtleStyle.Render(additionalLine))
	}
	fmt.Fprintln(&b)

	// Optional inline error at the bottom of the wizard.
	if strings.TrimSpace(m.wizard.errMsg) != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	}

	out := b.String()

	// Ensure the rendered wizard view always occupies at least the current
	// terminal height so that when fields are shown/hidden (e.g. toggling DB
	// mode) we don't leave stale lines from the previous frame on screen.
	if m.height > 0 {
		lineCount := strings.Count(out, "\n")
		if lineCount < m.height {
			out += strings.Repeat("\n", m.height-lineCount)
		}
	}

	return out
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
			// Move to the previous *active* field, skipping any inactive ones
			// (such as external DB fields when DB mode is not set to external).
			for {
				m.wizard.cursor--
				if m.wizard.cursor <= 0 || m.wizard.isFieldActive(m.wizard.cursor) {
					break
				}
			}
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
			// Move to the next *active* field, skipping any inactive ones.
			for {
				m.wizard.cursor++
				if m.wizard.cursor >= wizardFieldCount-1 || m.wizard.isFieldActive(m.wizard.cursor) {
					break
				}
			}
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
		// Advance to the next active field, wrapping around as needed.
		m.wizard.cursor = (m.wizard.cursor + 1) % wizardFieldCount
		for !m.wizard.isFieldActive(m.wizard.cursor) {
			m.wizard.cursor = (m.wizard.cursor + 1) % wizardFieldCount
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
	case "shift+tab":
		// Move to the previous active field, wrapping around as needed.
		m.wizard.cursor--
		if m.wizard.cursor < 0 {
			m.wizard.cursor = wizardFieldCount - 1
		}
		for !m.wizard.isFieldActive(m.wizard.cursor) {
			m.wizard.cursor--
			if m.wizard.cursor < 0 {
				m.wizard.cursor = wizardFieldCount - 1
			}
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
			case wizardFieldDBExternalPort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.dbPortStr)); err == nil && p > 1 {
					m.wizard.dbPortStr = fmt.Sprintf("%d", p-1)
					m.wizard.errMsg = ""
				}
			case wizardFieldDBMode:
				// Left/right both toggle DB mode between docker and external.
				if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
					m.wizard.cfg.dbMode = "docker"
				} else {
					m.wizard.cfg.dbMode = "external"
				}
				// After toggling, keep focus on the DB mode row and reset the
				// window so the visible rows match the new configuration.
				m.wizard.cursor = wizardFieldDBMode
				m.wizard.windowStart = 0
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
			case wizardFieldInstallMonitor:
				m.wizard.cfg.installMonitor = !m.wizard.cfg.installMonitor
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
			case wizardFieldDBExternalPort:
				if p, err := strconv.Atoi(strings.TrimSpace(m.wizard.dbPortStr)); err == nil {
					m.wizard.dbPortStr = fmt.Sprintf("%d", p+1)
					m.wizard.errMsg = ""
				}
			case wizardFieldDBMode:
				if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
					m.wizard.cfg.dbMode = "docker"
				} else {
					m.wizard.cfg.dbMode = "external"
				}
				m.wizard.cursor = wizardFieldDBMode
				m.wizard.windowStart = 0
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
			case wizardFieldInstallMonitor:
				m.wizard.cfg.installMonitor = !m.wizard.cfg.installMonitor
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
			case wizardFieldRCONPassword:
				m.wizard.cfg.rconPassword = val
			case wizardFieldDBExternalHost:
				m.wizard.cfg.externalDBHost = val
			case wizardFieldDBExternalPort:
				m.wizard.dbPortStr = val
			case wizardFieldDBExternalName:
				m.wizard.cfg.externalDBName = val
			case wizardFieldDBExternalUser:
				m.wizard.cfg.externalDBUser = val
			case wizardFieldDBExternalPassword:
				m.wizard.cfg.externalDBPassword = val
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
			// After toggling via Enter/space, keep focus on the DB mode row and
			// reset the window so the newly visible rows line up cleanly.
			m.wizard.cursor = wizardFieldDBMode
			m.wizard.windowStart = 0
			m.wizard.errMsg = ""
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
		case wizardFieldInstallMonitor:
			m.wizard.cfg.installMonitor = !m.wizard.cfg.installMonitor
			return m, nil
		case wizardFieldNumServers, wizardFieldBasePort, wizardFieldTVPort, wizardFieldRCONPassword,
			wizardFieldDBExternalHost, wizardFieldDBExternalPort, wizardFieldDBExternalName,
			wizardFieldDBExternalUser, wizardFieldDBExternalPassword:
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
			case wizardFieldRCONPassword:
				initial = m.wizard.cfg.rconPassword
			case wizardFieldDBExternalHost:
				initial = m.wizard.cfg.externalDBHost
			case wizardFieldDBExternalPort:
				initial = m.wizard.dbPortStr
			case wizardFieldDBExternalName:
				initial = m.wizard.cfg.externalDBName
			case wizardFieldDBExternalUser:
				initial = m.wizard.cfg.externalDBUser
			case wizardFieldDBExternalPassword:
				initial = m.wizard.cfg.externalDBPassword
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

			// Low disk space confirmation: reuse the same rough estimate used in
			// the wizard view. If we estimate that the install will exceed the
			// available space, show a warning and require the user to press
			// Start install again to continue anyway.
			const masterGB = csm.DefaultMasterDiskGB
			const perServerGB = csm.DefaultPerServerDiskGB

			numServers := m.wizard.cfg.numServers
			if n, err := strconv.Atoi(strings.TrimSpace(m.wizard.numServersStr)); err == nil && n > 0 {
				numServers = n
			}
			if numServers <= 0 {
				numServers = 1
			}
			totalRequiredGB := masterGB + perServerGB*float64(numServers)

			hasMaster, existingServers := existingInstallLayout(m.wizard.cfg.cs2User)

			var alreadyGB float64
			if hasMaster && !m.wizard.cfg.freshInstall {
				alreadyGB += masterGB
			}
			if existingServers > 0 && !m.wizard.cfg.freshInstall {
				if existingServers > numServers {
					existingServers = numServers
				}
				alreadyGB += perServerGB * float64(existingServers)
			}

			if m.wizard.cfg.freshInstall && (hasMaster || existingServers > 0) {
				alreadyGB = totalRequiredGB
			}

			additionalGB := totalRequiredGB - alreadyGB
			if additionalGB < 0 {
				additionalGB = 0
			}

			_, freeGB, ok := estimateDiskSpace(m.wizard.cfg.cs2User)
			if ok && freeGB < additionalGB && !m.wizard.lowDiskConfirmed {
				m.wizard.errMsg = fmt.Sprintf(
					"Estimated additional space needed is ~%.1f GB but only ~%.1f GB is free.\nPress Start install again if you are sure you want to continue anyway.",
					additionalGB, freeGB,
				)
				m.wizard.lowDiskConfirmed = true
				return m, nil
			}

			// Parse numeric fields into cfg.
			m.wizard.applyWizardNumericFields()

			// Initialize live timing for step 1.
			m.currentInstallStep = installStepPlugins
			m.installStepStart = time.Now()
			m.installStatusBase = "Step 1/4: Preparing plugin update..."
			m.installExpected = "~1–5 minutes"

			m.wizard.active = false
			m.view = viewMain
			m.running = true
			m.status = m.installStatusBase
			m.lastOutput = ""

			cfg := m.wizard.cfg
			return m, tea.Batch(runInstallStep(cfg, installStepPlugins), m.spin.Tick, tea.Tick(time.Second, func(time.Time) tea.Msg {
				return installElapsedMsg{}
			}))
		case wizardFieldCancel:
			m.wizard.active = false
			m.view = viewMain
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
		start := time.Now()
		var logs []string

		switch step {
		case installStepPlugins:
			if cfg.updatePlugins {
				logs = append(logs, "[1/4] Downloading latest plugins...")

				out, err := withPluginsLogTail(func() (string, error) {
					return csm.UpdatePlugins()
				})
				if err != nil {
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
			dur := time.Since(start).Round(time.Second)
			logs = append(logs, fmt.Sprintf("[i] Step 1/4 (plugins) took %s.", dur))
			appendInstallLog(cfg, installStepPlugins, strings.Join(logs, "\n"))
			return installStepMsg{
				step: installStepPlugins,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}

		case installStepBootstrap:
			if cfg.freshInstall {
				logs = append(logs,
					"[2/4] Performing fresh CS2 install (this may take several minutes)...",
					"  • Run full cleanup (same as Danger zone: remove CS2 user, home, MatchZy DB container/volume)",
					"  • Recreate CS2 user",
					"  • Reinstall master via SteamCMD",
					"  • Provision a clean MatchZy database (Docker mode)",
					"  • Recreate all servers from the new master",
				)

				// Run the same cleanup flow as the Danger Zone action so a
				// fresh install truly wipes the CS2 user, home directory, and
				// MatchZy resources before we reinstall.
				cleanupCfg := csm.CleanupConfig{
					CS2User: cfg.cs2User,
					// MatchzyContainer/Volume default inside CleanupAll when empty.
				}
				if out, err := csm.CleanupAll(cleanupCfg); err != nil {
					if out != "" {
						logs = append(logs, out)
					}
					logs = append(logs, fmt.Sprintf("Cleanup failed before fresh install: %v", err))
					return installStepMsg{
						step: installStepBootstrap,
						out:  strings.Join(logs, "\n"),
						err:  err,
					}
				}

				// After a full cleanup we no longer need Bootstrap's internal
				// FRESH_INSTALL semantics; it's effectively a clean install.
				cfg.freshInstall = false
			} else {
				logs = append(logs, "[2/4] Setting up CS2 servers (this may take several minutes)...")
			}

			// Derive MatchZy Docker behaviour from dbMode.
			cfg.matchzySkipDocker = strings.EqualFold(cfg.dbMode, "external")
			bcfg := csm.BootstrapConfig{
				CS2User:            cfg.cs2User,
				NumServers:         cfg.numServers,
				BaseGamePort:       cfg.basePort,
				BaseTVPort:         cfg.tvPort,
				EnableMetamod:      cfg.enableMetamod,
				FreshInstall:       cfg.freshInstall,
				UpdateMaster:       cfg.updateMaster,
				RCONPassword:       cfg.rconPassword,
				MatchzySkipDocker:  cfg.matchzySkipDocker,
				DBMode:             cfg.dbMode,
				ExternalDBHost:     cfg.externalDBHost,
				ExternalDBPort:     cfg.externalDBPort,
				ExternalDBName:     cfg.externalDBName,
				ExternalDBUser:     cfg.externalDBUser,
				ExternalDBPassword: cfg.externalDBPassword,
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
			dur := time.Since(start).Round(time.Second)
			logs = append(logs, fmt.Sprintf("[i] Step 2/4 (bootstrap) took %s.", dur))
			appendInstallLog(cfg, installStepBootstrap, strings.Join(logs, "\n"))
			return installStepMsg{
				step: installStepBootstrap,
				out:  strings.Join(logs, "\n"),
				err:  nil,
			}

		case installStepMonitor:
			if cfg.installMonitor {
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
			} else {
				logs = append(logs, "[3/4] Skipping auto-update monitor setup (user disabled it in the wizard).")
			}
			dur := time.Since(start).Round(time.Second)
			logs = append(logs, fmt.Sprintf("[i] Step 3/4 (auto-update monitor) took %s.", dur))
			appendInstallLog(cfg, installStepMonitor, strings.Join(logs, "\n"))
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
			dur := time.Since(start).Round(time.Second)
			logs = append(logs, fmt.Sprintf("[i] Step 4/4 (start servers) took %s.", dur))
			appendInstallLog(cfg, installStepStartServers, strings.Join(logs, "\n"))
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
			if len(data) == 0 {
				continue
			}

			// Normalise carriage returns so tools that render progress on a
			// single line (like rsync --info=PROGRESS2) still produce a useful
			// multi-line tail in the TUI.
			text := strings.ReplaceAll(string(data), "\r\n", "\n")
			text = strings.ReplaceAll(text, "\r", "\n")
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}

			lines := strings.Split(text, "\n")
			// Show a more generous slice of the log so long-running steamcmd
			// output and rsync progress are visible during the wizard. We
			// choose a fixed tail of 20 lines here; the final step summary
			// view already adapts to the full terminal height.
			if len(lines) > 20 {
				lines = lines[len(lines)-20:]
			}
			send(installLogTickMsg{lines: strings.Join(lines, "\n")})
		}
	}
}

// appendInstallLog writes a copy of each wizard step's logs to a persistent
// install log on disk so failures can be investigated after the TUI exits.
// The log is written under the shared CSM log directory (or CSM_INSTALL_LOG if
// explicitly set).
func appendInstallLog(cfg installConfig, step installStep, content string) {
	override := os.Getenv("CSM_INSTALL_LOG")

	timestamp := time.Now().Format(time.RFC3339)
	var b strings.Builder
	fmt.Fprintf(&b, "==== CSM install wizard step ====\n")
	fmt.Fprintf(&b, "Time : %s\n", timestamp)
	fmt.Fprintf(&b, "Step : %d\n", step)
	fmt.Fprintf(&b, "User : %s\n", cfg.cs2User)
	fmt.Fprintf(&b, "Servers: %d (base ports: game %d, GOTV %d)\n", cfg.numServers, cfg.basePort, cfg.tvPort)
	fmt.Fprintln(&b, "---- Output ----")
	fmt.Fprintln(&b, content)
	fmt.Fprintln(&b, "===============================")

	if override != "" {
		// Backwards-compatible explicit path override.
		_ = os.MkdirAll(filepath.Dir(override), 0o755)
		_ = os.WriteFile(override, []byte(b.String()), 0o644)
		return
	}

	csm.AppendLog("install.log", b.String())
}

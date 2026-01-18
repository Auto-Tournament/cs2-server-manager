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

	"github.com/charmbracelet/bubbles/textinput"
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

// Wizard field indices for the multi-step install wizard view.
const (
	wizardFieldDBMode = iota
	wizardFieldNumServers
	wizardFieldBasePort
	wizardFieldTVPort
	wizardFieldCS2User
	wizardFieldHostnamePrefix
	wizardFieldRCONPassword
	wizardFieldMaxPlayers
	wizardFieldGSLT
	wizardFieldMetamod
	wizardFieldFreshInstall
	wizardFieldUpdateMaster
	wizardFieldUpdatePlugins
	wizardFieldInstallMonitor
	wizardFieldDBExternalHost
	wizardFieldDBExternalPort
	wizardFieldDBExternalName
	wizardFieldDBExternalUser
	wizardFieldDBExternalPassword
	wizardFieldNext
	wizardFieldPrevious
	wizardFieldStartInstall
	wizardFieldCancel
	wizardFieldCount
)

// wizardPage defines which fields appear on each page
type wizardPage []int

// getWizardPages returns the pages based on DB mode (external DB fields shown conditionally)
func getWizardPages(dbMode string) []wizardPage {
	pages := []wizardPage{
		// Page 0: Basic setup (4 items)
		{
			wizardFieldDBMode, wizardFieldNumServers, wizardFieldBasePort, wizardFieldTVPort,
		},
		// Page 1: Server identity (4 items)
		{
			wizardFieldCS2User, wizardFieldHostnamePrefix, wizardFieldRCONPassword, wizardFieldMaxPlayers,
		},
		// Page 2: Tokens and core options (4 items)
		{
			wizardFieldGSLT, wizardFieldMetamod, wizardFieldFreshInstall, wizardFieldUpdateMaster,
		},
		// Page 3: Update options (4 items)
		{
			wizardFieldUpdatePlugins, wizardFieldInstallMonitor,
		},
	}
	
	// Add external DB page if using external mode
	if strings.EqualFold(dbMode, "external") {
		externalPage := wizardPage{
			wizardFieldDBExternalHost,
			wizardFieldDBExternalPort,
			wizardFieldDBExternalName,
			wizardFieldDBExternalUser,
			wizardFieldDBExternalPassword,
		}
		// Insert before final page (which has Start install)
		pages = append(pages[:len(pages)-1], externalPage, pages[len(pages)-1])
	}
	
	return pages
}

// estimateDiskSpace returns total and free space (in GB) for the filesystem
// that will hold /home/<cs2User>. If cs2User is empty or the check fails, it
// falls back to "/" and may return ok=false on error.
func estimateDiskSpace(cs2User string) (totalGB, freeGB float64, ok bool) {
	path := "/"
	if strings.TrimSpace(cs2User) != "" {
		path = filepath.Join("/home", cs2User)
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}

	blockSize := float64(stat.Bsize)
	totalGB = (float64(stat.Blocks) * blockSize) / (1024 * 1024 * 1024)
	freeGB = (float64(stat.Bavail) * blockSize) / (1024 * 1024 * 1024)
	return totalGB, freeGB, true
}

// existingInstallLayout returns whether a master-install directory exists and
// how many server-* directories are present. This is used to estimate the
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

	header := headerBorderStyle.Render(titleStyle.Render("Install or redeploy servers")) +
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

	// Get pages for current DB mode
	pages := getWizardPages(m.wizard.cfg.dbMode)
	if m.wizard.currentPage < 0 {
		m.wizard.currentPage = 0
	}
	if m.wizard.currentPage >= len(pages) {
		m.wizard.currentPage = len(pages) - 1
	}
	currentPage := pages[m.wizard.currentPage]
	isLastPage := m.wizard.currentPage == len(pages)-1

	// Build list of fields to show: navigation buttons first, then page fields
	var visibleFields []int
	
	// Add navigation buttons at the top (Next first, then Previous)
	if !isLastPage {
		visibleFields = append(visibleFields, wizardFieldNext)
	} else {
		visibleFields = append(visibleFields, wizardFieldStartInstall)
	}
	if m.wizard.currentPage > 0 {
		visibleFields = append(visibleFields, wizardFieldPrevious)
	}
	visibleFields = append(visibleFields, wizardFieldCancel)
	
	// Add page fields after navigation
	visibleFields = append(visibleFields, currentPage...)

	// Ensure cursor is within bounds
	if m.wizard.cursor < 0 {
		m.wizard.cursor = 0
	}
	if m.wizard.cursor >= len(visibleFields) {
		m.wizard.cursor = len(visibleFields) - 1
	}

	// Helper to render a single row with optional selection highlighting.
	renderRow := func(fieldIdx int, label, value string) {
		selected := false
		if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == fieldIdx {
			selected = true
		}
		style := menuItemStyle
		if selected {
			style = menuSelectedStyle
		}
		line := fmt.Sprintf("%-20s %s", label, value)
		fmt.Fprintln(&b, style.Render(line))
		fmt.Fprintln(&b)
	}

	// Render all visible fields
	for _, fieldIdx := range visibleFields {
		switch fieldIdx {
		case wizardFieldDBMode:
			dbLabel := "Docker-managed MySQL (recommended)"
			if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
				dbLabel = "External MySQL (no Docker provisioning)"
			}
			renderRow(wizardFieldDBMode, "MatchZy DB:", dbLabel)

		case wizardFieldNumServers:
			numServersVal := m.wizard.numServersStr
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldNumServers && m.wizard.editing {
				numServersVal = m.wizard.input.View()
			}
			renderRow(wizardFieldNumServers, "Number of servers:", numServersVal)

		case wizardFieldBasePort:
			basePortVal := m.wizard.basePortStr
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldBasePort && m.wizard.editing {
				basePortVal = m.wizard.input.View()
			}
			renderRow(wizardFieldBasePort, "Base game port:", basePortVal)

		case wizardFieldTVPort:
			tvPortVal := m.wizard.tvPortStr
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldTVPort && m.wizard.editing {
				tvPortVal = m.wizard.input.View()
			}
			renderRow(wizardFieldTVPort, "Base GOTV port:", tvPortVal)

		case wizardFieldCS2User:
			cs2UserVal := m.wizard.cfg.cs2User
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldCS2User && m.wizard.editing {
				cs2UserVal = m.wizard.input.View()
			}
			renderRow(wizardFieldCS2User, "CS2 user:", cs2UserVal)

		case wizardFieldHostnamePrefix:
			hostnameVal := m.wizard.cfg.hostnamePrefix
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldHostnamePrefix && m.wizard.editing {
				hostnameVal = m.wizard.input.View()
			}
			renderRow(wizardFieldHostnamePrefix, "Server name prefix:", hostnameVal)

		case wizardFieldRCONPassword:
			rconVal := m.wizard.cfg.rconPassword
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldRCONPassword && m.wizard.editing {
				rconVal = m.wizard.input.View()
			}
			renderRow(wizardFieldRCONPassword, "RCON password:", rconVal)

		case wizardFieldMaxPlayers:
			maxPlayersStr := ""
			if m.wizard.cfg.maxPlayers > 0 {
				maxPlayersStr = fmt.Sprintf("%d", m.wizard.cfg.maxPlayers)
			}
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldMaxPlayers && m.wizard.editing {
				maxPlayersStr = m.wizard.input.View()
			}
			renderRow(wizardFieldMaxPlayers, "Max players:", maxPlayersStr)

		case wizardFieldGSLT:
			gsltVal := m.wizard.cfg.gslt
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldGSLT && m.wizard.editing {
				gsltVal = m.wizard.input.View()
			}
			renderRow(wizardFieldGSLT, "GSLT token (optional):", gsltVal)

		case wizardFieldMetamod:
			boolLabel := func(v bool) string {
				if v {
					return "[x] Yes"
				}
				return "[ ] No"
			}
			renderRow(wizardFieldMetamod, "Enable Metamod:", boolLabel(m.wizard.cfg.enableMetamod))

		case wizardFieldFreshInstall:
			boolLabel := func(v bool) string {
				if v {
					return "[x] Yes"
				}
				return "[ ] No"
			}
			renderRow(wizardFieldFreshInstall, "Fresh install:", boolLabel(m.wizard.cfg.freshInstall))

		case wizardFieldUpdateMaster:
			boolLabel := func(v bool) string {
				if v {
					return "[x] Yes"
				}
				return "[ ] No"
			}
			renderRow(wizardFieldUpdateMaster, "Update master:", boolLabel(m.wizard.cfg.updateMaster))

		case wizardFieldUpdatePlugins:
			boolLabel := func(v bool) string {
				if v {
					return "[x] Yes"
				}
				return "[ ] No"
			}
			renderRow(wizardFieldUpdatePlugins, "Update plugins:", boolLabel(m.wizard.cfg.updatePlugins))

		case wizardFieldInstallMonitor:
			boolLabel := func(v bool) string {
				if v {
					return "[x] Yes"
				}
				return "[ ] No"
			}
			renderRow(wizardFieldInstallMonitor, "Install auto-update:", boolLabel(m.wizard.cfg.installMonitor))

		case wizardFieldDBExternalHost:
			dbHostVal := m.wizard.cfg.externalDBHost
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldDBExternalHost && m.wizard.editing {
				dbHostVal = m.wizard.input.View()
			}
			renderRow(wizardFieldDBExternalHost, "DB host (external):", dbHostVal)

		case wizardFieldDBExternalPort:
			dbPortVal := m.wizard.dbPortStr
			if strings.TrimSpace(dbPortVal) == "" && m.wizard.cfg.externalDBPort > 0 {
				dbPortVal = fmt.Sprintf("%d", m.wizard.cfg.externalDBPort)
			}
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldDBExternalPort && m.wizard.editing {
				dbPortVal = m.wizard.input.View()
			}
			renderRow(wizardFieldDBExternalPort, "DB port (external):", dbPortVal)

		case wizardFieldDBExternalName:
			dbNameVal := m.wizard.cfg.externalDBName
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldDBExternalName && m.wizard.editing {
				dbNameVal = m.wizard.input.View()
			}
			renderRow(wizardFieldDBExternalName, "DB name (external):", dbNameVal)

		case wizardFieldDBExternalUser:
			dbUserVal := m.wizard.cfg.externalDBUser
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldDBExternalUser && m.wizard.editing {
				dbUserVal = m.wizard.input.View()
			}
			renderRow(wizardFieldDBExternalUser, "DB user (external):", dbUserVal)

		case wizardFieldDBExternalPassword:
			dbPassVal := m.wizard.cfg.externalDBPassword
			if m.wizard.cursor < len(visibleFields) && visibleFields[m.wizard.cursor] == wizardFieldDBExternalPassword && m.wizard.editing {
				dbPassVal = m.wizard.input.View()
			}
			renderRow(wizardFieldDBExternalPassword, "DB password (external):", dbPassVal)

		case wizardFieldNext:
			renderRow(wizardFieldNext, "", "Next →")

		case wizardFieldPrevious:
			renderRow(wizardFieldPrevious, "", "← Previous")

		case wizardFieldStartInstall:
			renderRow(wizardFieldStartInstall, "", "Start install")

		case wizardFieldCancel:
			renderRow(wizardFieldCancel, "", "Cancel")
		}
	}

	// Page indicator
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, subtleStyle.Render(fmt.Sprintf("Page %d of %d", m.wizard.currentPage+1, len(pages))))

	// Contextual description at the bottom for the currently selected field.
	var desc string
	if m.wizard.cursor < len(visibleFields) {
		selectedField := visibleFields[m.wizard.cursor]
		switch selectedField {
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
		case wizardFieldHostnamePrefix:
			desc = "Base name for your servers, e.g. \"My CS2 Server\" (CSM will append server numbers automatically)."
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
		case wizardFieldMaxPlayers:
			desc = "Maximum number of players (0 or empty = use CS2 default, typically 10)."
		case wizardFieldGSLT:
			desc = "Steam Game Server Login Token (GSLT) for server authentication. Optional but recommended for public servers."
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
		case wizardFieldNext:
			desc = "Continue to the next page."
		case wizardFieldPrevious:
			desc = "Go back to the previous page."
		case wizardFieldStartInstall:
			desc = "Run the full install with the settings above."
		case wizardFieldCancel:
			desc = "Return to the main menu without installing."
		}
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
	diskLine := fmt.Sprintf("Estimated additional space needed: ~%.1f GB.", additionalGB)

	_, freeGB, ok := estimateDiskSpace(m.wizard.cfg.cs2User)
	if ok {
		afterGB := freeGB - additionalGB
		if afterGB >= 0 {
			diskLine += fmt.Sprintf(" (will leave ~%.1f GB free)", afterGB)
		} else {
			needed := -afterGB
			diskLine2 := fmt.Sprintf("Warning: estimated additional space is short by ~%.1f GB (currently ~%.1f GB free).", needed, freeGB)
			fmt.Fprintln(&b, subtleStyle.Render(baseLine))
			fmt.Fprintln(&b, warningStyle.Render(diskLine))
			fmt.Fprintln(&b, warningStyle.Render(diskLine2))
		}
	} else {
		fmt.Fprintln(&b, subtleStyle.Render(baseLine))
		fmt.Fprintln(&b, subtleStyle.Render(diskLine))
	}
	fmt.Fprintln(&b)

	// Optional inline error at the bottom of the wizard.
	if strings.TrimSpace(m.wizard.errMsg) != "" {
		fmt.Fprintln(&b, statusBarStyle.Render("Error: "+m.wizard.errMsg))
	}

	return b.String()
}

// updateInstallWizard handles navigation and editing for the multi-step wizard.
func (m model) updateInstallWizard(msg tea.Msg) (model, tea.Cmd) {
	// If editing, handle input first (for both KeyMsg and non-KeyMsg)
	if m.wizard.editing {
		key, ok := msg.(tea.KeyMsg)
		// Handle Enter/Esc to exit editing mode
		if ok {
			if key.String() == "enter" || key.String() == "esc" {
				// Will be handled below in the enter/esc cases
			} else {
				// All other keys go to the input field
				var cmd tea.Cmd
				m.wizard.input, cmd = m.wizard.input.Update(msg)
				return m, cmd
			}
		} else {
			// Non-KeyMsg input (like text input events)
			var cmd tea.Cmd
			m.wizard.input, cmd = m.wizard.input.Update(msg)
			return m, cmd
		}
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Get current page fields
	pages := getWizardPages(m.wizard.cfg.dbMode)
	if m.wizard.currentPage < 0 {
		m.wizard.currentPage = 0
	}
	if m.wizard.currentPage >= len(pages) {
		m.wizard.currentPage = len(pages) - 1
	}
	currentPage := pages[m.wizard.currentPage]
	isLastPage := m.wizard.currentPage == len(pages)-1

	var visibleFields []int
	
	// Add navigation buttons at the top (Next first, then Previous - same order as viewInstallWizard)
	if !isLastPage {
		visibleFields = append(visibleFields, wizardFieldNext)
	} else {
		visibleFields = append(visibleFields, wizardFieldStartInstall)
	}
	if m.wizard.currentPage > 0 {
		visibleFields = append(visibleFields, wizardFieldPrevious)
	}
	visibleFields = append(visibleFields, wizardFieldCancel)
	
	// Add page fields after navigation
	visibleFields = append(visibleFields, currentPage...)

	// Ensure cursor is within bounds
	if m.wizard.cursor < 0 {
		m.wizard.cursor = 0
	}
	if m.wizard.cursor >= len(visibleFields) {
		m.wizard.cursor = len(visibleFields) - 1
	}

	currentField := visibleFields[m.wizard.cursor]

	switch key.String() {
	case "up", "k":
		if m.wizard.cursor > 0 {
			m.wizard.cursor--
			m.wizard.editing = false
			m.wizard.errMsg = ""
		}
		return m, nil
	case "down", "j":
		if m.wizard.cursor < len(visibleFields)-1 {
			m.wizard.cursor++
			m.wizard.editing = false
			m.wizard.errMsg = ""
		}
		return m, nil
	case "tab":
		m.wizard.cursor = (m.wizard.cursor + 1) % len(visibleFields)
		m.wizard.editing = false
		m.wizard.errMsg = ""
		return m, nil
	case "shift+tab":
		m.wizard.cursor--
		if m.wizard.cursor < 0 {
			m.wizard.cursor = len(visibleFields) - 1
		}
		m.wizard.editing = false
		m.wizard.errMsg = ""
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
			switch currentField {
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
				// Recalculate pages if DB mode changed
				pages = getWizardPages(m.wizard.cfg.dbMode)
				if m.wizard.currentPage >= len(pages) {
					m.wizard.currentPage = len(pages) - 1
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
			switch currentField {
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
				// Recalculate pages if DB mode changed
				pages = getWizardPages(m.wizard.cfg.dbMode)
				if m.wizard.currentPage >= len(pages) {
					m.wizard.currentPage = len(pages) - 1
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
			case wizardFieldInstallMonitor:
				m.wizard.cfg.installMonitor = !m.wizard.cfg.installMonitor
				m.wizard.errMsg = ""
			}
		}
		return m, nil
	case "enter":
		if m.wizard.editing {
			// Commit the current edit.
			value := strings.TrimSpace(m.wizard.input.Value())
			
			switch currentField {
			case wizardFieldNumServers:
				m.wizard.numServersStr = value
			case wizardFieldBasePort:
				m.wizard.basePortStr = value
			case wizardFieldTVPort:
				m.wizard.tvPortStr = value
			case wizardFieldHostnamePrefix:
				m.wizard.cfg.hostnamePrefix = value
			case wizardFieldRCONPassword:
				m.wizard.cfg.rconPassword = value
			case wizardFieldMaxPlayers:
				m.wizard.cfg.maxPlayers = 0
				if n, err := strconv.Atoi(value); err == nil && n > 0 {
					m.wizard.cfg.maxPlayers = n
				}
			case wizardFieldGSLT:
				m.wizard.cfg.gslt = value
			case wizardFieldDBExternalHost:
				m.wizard.cfg.externalDBHost = value
			case wizardFieldDBExternalPort:
				m.wizard.dbPortStr = value
			case wizardFieldDBExternalName:
				m.wizard.cfg.externalDBName = value
			case wizardFieldDBExternalUser:
				m.wizard.cfg.externalDBUser = value
			case wizardFieldDBExternalPassword:
				m.wizard.cfg.externalDBPassword = value
			case wizardFieldCS2User:
				m.wizard.cfg.cs2User = value
			}

			m.wizard.editing = false
			m.wizard.errMsg = ""
			return m, nil
		}

		// Not editing: handle button presses and field activation.
		switch currentField {
		case wizardFieldNext:
			// Move to next page
			if m.wizard.currentPage < len(pages)-1 {
				m.wizard.currentPage++
				m.wizard.cursor = 0
			}
			return m, nil
		case wizardFieldPrevious:
			// Move to previous page
			if m.wizard.currentPage > 0 {
				m.wizard.currentPage--
				m.wizard.cursor = 0
			}
			return m, nil
		case wizardFieldDBMode, wizardFieldMetamod, wizardFieldFreshInstall,
			wizardFieldUpdateMaster, wizardFieldUpdatePlugins, wizardFieldInstallMonitor:
			// Toggle boolean fields or DB mode on Enter
			switch currentField {
			case wizardFieldDBMode:
				if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
					m.wizard.cfg.dbMode = "docker"
				} else {
					m.wizard.cfg.dbMode = "external"
				}
				// Recalculate pages
				pages = getWizardPages(m.wizard.cfg.dbMode)
				if m.wizard.currentPage >= len(pages) {
					m.wizard.currentPage = len(pages) - 1
				}
			case wizardFieldMetamod:
				m.wizard.cfg.enableMetamod = !m.wizard.cfg.enableMetamod
			case wizardFieldFreshInstall:
				m.wizard.cfg.freshInstall = !m.wizard.cfg.freshInstall
			case wizardFieldUpdateMaster:
				m.wizard.cfg.updateMaster = !m.wizard.cfg.updateMaster
			case wizardFieldUpdatePlugins:
				m.wizard.cfg.updatePlugins = !m.wizard.cfg.updatePlugins
			case wizardFieldInstallMonitor:
				m.wizard.cfg.installMonitor = !m.wizard.cfg.installMonitor
			}
			m.wizard.errMsg = ""
			return m, nil
		case wizardFieldNumServers, wizardFieldBasePort, wizardFieldTVPort,
			wizardFieldHostnamePrefix, wizardFieldRCONPassword, wizardFieldMaxPlayers,
			wizardFieldGSLT, wizardFieldDBExternalHost, wizardFieldDBExternalPort,
			wizardFieldDBExternalName, wizardFieldDBExternalUser, wizardFieldDBExternalPassword,
			wizardFieldCS2User:
			// Enter on a text/numeric field: start editing.
			m.wizard.editing = true
			m.wizard.errMsg = ""

			// Populate input with current value
			var currentValue string
			switch currentField {
			case wizardFieldNumServers:
				currentValue = m.wizard.numServersStr
			case wizardFieldBasePort:
				currentValue = m.wizard.basePortStr
			case wizardFieldTVPort:
				currentValue = m.wizard.tvPortStr
			case wizardFieldHostnamePrefix:
				currentValue = m.wizard.cfg.hostnamePrefix
			case wizardFieldRCONPassword:
				currentValue = m.wizard.cfg.rconPassword
			case wizardFieldMaxPlayers:
				if m.wizard.cfg.maxPlayers > 0 {
					currentValue = fmt.Sprintf("%d", m.wizard.cfg.maxPlayers)
				}
			case wizardFieldGSLT:
				currentValue = m.wizard.cfg.gslt
			case wizardFieldDBExternalHost:
				currentValue = m.wizard.cfg.externalDBHost
			case wizardFieldDBExternalPort:
				currentValue = m.wizard.dbPortStr
			case wizardFieldDBExternalName:
				currentValue = m.wizard.cfg.externalDBName
			case wizardFieldDBExternalUser:
				currentValue = m.wizard.cfg.externalDBUser
			case wizardFieldDBExternalPassword:
				currentValue = m.wizard.cfg.externalDBPassword
			case wizardFieldCS2User:
				currentValue = m.wizard.cfg.cs2User
			}

			m.wizard.input.SetValue(currentValue)
			m.wizard.input.Focus()
			if currentField == wizardFieldDBExternalPassword {
				m.wizard.input.EchoMode = textinput.EchoPassword
			} else {
				m.wizard.input.EchoMode = textinput.EchoNormal
			}
			m.wizard.input.CharLimit = 0
			if currentField == wizardFieldMaxPlayers || currentField == wizardFieldNumServers ||
				currentField == wizardFieldBasePort || currentField == wizardFieldTVPort ||
				currentField == wizardFieldDBExternalPort {
				m.wizard.input.Prompt = "> "
			} else {
				m.wizard.input.Prompt = "> "
			}

			return m, textinput.Blink
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

		log := func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			logs = append(logs, msg)
		}

		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer func() {
			cancel()
			CancelInstall()
		}()

		var err error
		switch step {
		case installStepPlugins:
			if cfg.updatePlugins {
				// UpdatePlugins doesn't have context support yet, but we still
				// check context cancellation before/after to allow quick abort
				// during install wizard.
				select {
				case <-ctx.Done():
					err = ctx.Err()
					log("Plugin update cancelled: %v", err)
				default:
					_, err = csm.UpdatePlugins()
					if err != nil {
						log("Plugin update failed: %v", err)
					} else {
						log("Plugin update completed successfully.")
					}
				}
			} else {
				log("Plugin update skipped (update plugins disabled).")
			}

		case installStepBootstrap:
			// Stream bootstrap progress by mirroring logs into a temp file that a
			// background goroutine tails, similar to other long-running operations.
			logPath := filepath.Join(os.TempDir(), "csm-bootstrap.log")
			_ = os.Remove(logPath)

			done := make(chan struct{})
			go tailInstallLog(logPath, done)
			defer close(done)

			_ = os.Setenv("CSM_BOOTSTRAP_LOG", logPath)
			defer os.Unsetenv("CSM_BOOTSTRAP_LOG")

			bootstrapCfg := csm.BootstrapConfig{
				NumServers:     cfg.numServers,
				BaseGamePort:   cfg.basePort,
				BaseTVPort:     cfg.tvPort,
				CS2User:        cfg.cs2User,
				HostnamePrefix: cfg.hostnamePrefix,
				EnableMetamod:  cfg.enableMetamod,
				FreshInstall:   cfg.freshInstall,
				UpdateMaster:   cfg.updateMaster,
				RCONPassword:   cfg.rconPassword,
				MaxPlayers:     cfg.maxPlayers,
				GSLT:           cfg.gslt,
			}
			_, err = csm.BootstrapWithContext(ctx, bootstrapCfg)
			if err != nil {
				log("Bootstrap failed: %v", err)
			} else {
				log("Bootstrap completed successfully.")
			}

		case installStepMonitor:
			if cfg.installMonitor {
				_, err = csm.InstallAutoUpdateCronWithContext(ctx, "")
				if err != nil {
					log("Monitor installation failed: %v", err)
				} else {
					log("Monitor installation completed successfully.")
				}
			} else {
				log("Monitor installation skipped (install monitor disabled).")
			}

		case installStepStartServers:
			mgr, mgrErr := csm.NewTmuxManager()
			if mgrErr != nil {
				err = fmt.Errorf("failed to create tmux manager: %w", mgrErr)
			} else {
				startErr := mgr.StartAll()
				if startErr != nil {
					err = fmt.Errorf("failed to start servers: %w", startErr)
				} else {
					log("All servers started successfully.")
				}
			}
		}

		elapsed := time.Since(start)
		log("Step completed in %v.", elapsed)

		output := strings.Join(logs, "\n")
		return installStepMsg{step: step, out: output, err: err}
	}
}

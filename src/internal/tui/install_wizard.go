package tui

import (
	"fmt"
	"strconv"
	"strings"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
	huh "github.com/charmbracelet/huh"
	tea "github.com/charmbracelet/bubbletea"
)

// buildInstallForm constructs the huh.Form used for the install wizard,
// binding its fields directly to the wizard config and helper strings.
func buildInstallForm(w *installWizard) *huh.Form {
	// Ensure sensible defaults.
	if w.cfg.dbMode == "" {
		w.cfg.dbMode = "docker"
	}
	if w.cfg.cs2User == "" {
		w.cfg.cs2User = "cs2"
	}
	if w.cfg.rconPassword == "" {
		w.cfg.rconPassword = "ntlan2025"
	}
	if w.cfg.numServers <= 0 {
		w.cfg.numServers = 3
	}
	if w.cfg.basePort == 0 {
		w.cfg.basePort = 27015
	}
	if w.cfg.tvPort == 0 {
		w.cfg.tvPort = 27020
	}

	if w.numServersStr == "" {
		w.numServersStr = fmt.Sprintf("%d", w.cfg.numServers)
	}
	if w.basePortStr == "" {
		w.basePortStr = fmt.Sprintf("%d", w.cfg.basePort)
	}
	if w.tvPortStr == "" {
		w.tvPortStr = fmt.Sprintf("%d", w.cfg.tvPort)
	}

	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Key("db_mode").
				Title("MatchZy database").
				Options(
					huh.NewOption("Docker-managed MySQL (recommended)", "docker"),
					huh.NewOption("External MySQL (advanced, no Docker provisioning)", "external"),
				).
				Value(&w.cfg.dbMode),

			huh.NewInput().
				Key("num_servers").
				Title("Number of servers").
				Value(&w.numServersStr).
				Validate(func(v string) error {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("number of servers is required")
					}
					n, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || n <= 0 {
						return fmt.Errorf("enter a positive integer")
					}
					return nil
				}),

			huh.NewInput().
				Key("base_game_port").
				Title("Base game port").
				Value(&w.basePortStr).
				Validate(func(v string) error {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("base game port is required")
					}
					p, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || p <= 0 {
						return fmt.Errorf("enter a valid port number")
					}
					return nil
				}),

			huh.NewInput().
				Key("base_tv_port").
				Title("Base GOTV port").
				Value(&w.tvPortStr).
				Validate(func(v string) error {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("base GOTV port is required")
					}
					p, err := strconv.Atoi(strings.TrimSpace(v))
					if err != nil || p <= 0 {
						return fmt.Errorf("enter a valid port number")
					}
					return nil
				}),

			huh.NewInput().
				Key("cs2_user").
				Title("Linux user that owns the servers").
				Value(&w.cfg.cs2User),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Key("metamod").
				Title("Enable Metamod framework?").
				Affirmative("Yes").
				Negative("No").
				Value(&w.cfg.enableMetamod),

			huh.NewConfirm().
				Key("fresh_install").
				Title("Fresh install (delete existing servers)?").
				Affirmative("Yes").
				Negative("No").
				Value(&w.cfg.freshInstall),

			huh.NewConfirm().
				Key("update_master").
				Title("Update master CS2 install via SteamCMD?").
				Affirmative("Yes").
				Negative("No").
				Value(&w.cfg.updateMaster),

			huh.NewConfirm().
				Key("update_plugins").
				Title("Download latest plugins before install?").
				Affirmative("Yes").
				Negative("No").
				Value(&w.cfg.updatePlugins),
		),
		huh.NewGroup(
			huh.NewInput().
				Key("rcon_password").
				Title("RCON password for all servers").
				Value(&w.cfg.rconPassword),
		),
	)
}

// applyWizardNumericFields parses the string fields coming from the form into
// the concrete numeric fields on the config.
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

func (m model) viewInstallWizard() string {
	var b strings.Builder

	header := headerBorderStyle.Render(titleStyle.Render("CS2 Server Manager - Install Wizard")) +
		"\n" +
		headerBorderStyle.Render("Configure your servers using the form below")

	fmt.Fprintln(&b, header)
	fmt.Fprintln(&b)

	if m.wizard.reviewing {
		// Final summary/confirmation page before we actually start installing.
		dbMode := "Docker-managed MySQL"
		if strings.EqualFold(m.wizard.cfg.dbMode, "external") {
			dbMode = "External MySQL (no Docker provisioning)"
		}

		fmt.Fprintln(&b, menuTitleStyle.Render("Review install settings"))
		fmt.Fprintln(&b)

		lines := []string{
			fmt.Sprintf("MatchZy DB     : %s", dbMode),
			fmt.Sprintf("Servers        : %d", m.wizard.cfg.numServers),
			fmt.Sprintf("Base ports     : game %d, GOTV %d", m.wizard.cfg.basePort, m.wizard.cfg.tvPort),
			fmt.Sprintf("CS2 user       : %s", m.wizard.cfg.cs2User),
			fmt.Sprintf("Metamod        : %v", m.wizard.cfg.enableMetamod),
			fmt.Sprintf("Fresh install  : %v", m.wizard.cfg.freshInstall),
			fmt.Sprintf("Update master  : %v", m.wizard.cfg.updateMaster),
			fmt.Sprintf("RCON password  : %s", m.wizard.cfg.rconPassword),
			fmt.Sprintf("Update plugins : %v", m.wizard.cfg.updatePlugins),
		}
		for _, l := range lines {
			fmt.Fprintln(&b, menuItemStyle.Render(l))
		}
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, statusBarStyle.Render("Press Enter to start install, Esc to cancel, q to quit."))
		return b.String()
	}

	if m.wizard.form != nil {
		fmt.Fprintln(&b, m.wizard.form.View())
	} else {
		fmt.Fprintln(&b, "Install wizard is not initialized.")
	}

	return b.String()
}

// updateInstallWizard routes messages into the huh.Form and, once the form
// is completed, kicks off the actual install via runInstallFromWizard.
func (m model) updateInstallWizard(msg tea.Msg) (model, tea.Cmd) {
	if m.wizard.form == nil {
		return m, nil
	}

	// If we're on the final review/confirmation page, handle keys here.
	if m.wizard.reviewing {
		if key, ok := msg.(tea.KeyMsg); ok {
			switch key.String() {
			case "enter", "y":
				// Start install.
				m.wizard.reviewing = false
				m.view = viewMain
				m.wizard.active = false
				m.running = true
				m.status = "Installing servers..."
				m.lastOutput = ""

				cfg := m.wizard.cfg
				return m, tea.Batch(runInstallFromWizard(cfg), m.spin.Tick)
			case "esc":
				// Cancel and go back to main menu without installing.
				m.wizard.reviewing = false
				m.wizard.active = false
				m.view = viewMain
				m.status = "Select an action and press Enter to run it."
				return m, nil
			case "ctrl+c", "q":
				return m, tea.Quit
			}
		}
		return m, nil
	}

	form, cmd := m.wizard.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.wizard.form = f
	}

	// When the form is completed we parse numeric fields and switch to a
	// summary review page; the actual install only starts after explicit
	// confirmation.
	if m.wizard.form.State == huh.StateCompleted {
		m.wizard.applyWizardNumericFields()
		m.wizard.reviewing = true
		m.status = "Review settings. Press Enter to start install, Esc to cancel."
		return m, cmd
	}

	return m, cmd
}

// runInstallFromWizard orchestrates the install using existing scripts and the
// configuration gathered from the wizard.
func runInstallFromWizard(cfg installConfig) tea.Cmd {
	return func() tea.Msg {
		var logs []string

		// 1) Optionally download plugins via Go implementation
		if cfg.updatePlugins {
			logs = append(logs, "Downloading latest plugins...")
			if out, err := csm.UpdatePlugins(); err != nil {
				logs = append(logs, out)
				logs = append(logs, fmt.Sprintf("Plugin download failed: %v", err))
				return commandFinishedMsg{
					item:   menuItem{title: "Install wizard"},
					output: strings.Join(logs, "\n"),
					err:    err,
				}
			} else if out != "" {
				logs = append(logs, out)
			}
		}

		// 2) Bootstrap CS2 servers via Go implementation
		logs = append(logs, "Setting up CS2 servers...")
		// Derive MatchZy Docker behaviour from dbMode.
		cfg.matchzySkipDocker = strings.EqualFold(cfg.dbMode, "external")

		bcfg := csm.BootstrapConfig{
			CS2User:          cfg.cs2User,
			NumServers:       cfg.numServers,
			BaseGamePort:     cfg.basePort,
			BaseTVPort:       cfg.tvPort,
			EnableMetamod:    cfg.enableMetamod,
			FreshInstall:     cfg.freshInstall,
			UpdateMaster:     cfg.updateMaster,
			RCONPassword:     cfg.rconPassword,
			MatchzySkipDocker: cfg.matchzySkipDocker,
		}
		if out, err := csm.Bootstrap(bcfg); err != nil {
			logs = append(logs, out)
			logs = append(logs, fmt.Sprintf("Bootstrap failed: %v", err))
			return commandFinishedMsg{
				item:   menuItem{title: "Install wizard"},
				output: strings.Join(logs, "\n"),
				err:    err,
			}
		} else if out != "" {
			logs = append(logs, out)
		}

		// 3) Setup auto-update monitor cronjob via Go implementation
		logs = append(logs, "Configuring auto-update monitor (cron job)...")
		if out, err := csm.InstallAutoUpdateCron(""); err != nil {
			logs = append(logs, out)
			logs = append(logs, fmt.Sprintf("Auto-update monitor setup failed: %v", err))
			// Non-fatal for install, but we do propagate the error.
			return commandFinishedMsg{
				item:   menuItem{title: "Install wizard"},
				output: strings.Join(logs, "\n"),
				err:    err,
			}
		} else if out != "" {
			logs = append(logs, out)
		}

		// 4) Start all servers using the Go tmux manager
		logs = append(logs, "Starting all servers...")
		manager, err := csm.NewTmuxManager()
		if err != nil {
			logs = append(logs, fmt.Sprintf("Failed to initialize tmux manager: %v", err))
			return commandFinishedMsg{
				item:   menuItem{title: "Install wizard"},
				output: strings.Join(logs, "\n"),
				err:    err,
			}
		}
		if err := manager.StartAll(); err != nil {
			logs = append(logs, fmt.Sprintf("Failed to start servers: %v", err))
			return commandFinishedMsg{
				item:   menuItem{title: "Install wizard"},
				output: strings.Join(logs, "\n"),
				err:    err,
			}
		}
		logs = append(logs, "All servers started via tmux.")

		return commandFinishedMsg{
			item:   menuItem{title: "Install wizard"},
			output: strings.Join(logs, "\n"),
			err:    err,
		}
	}
}


package tui

import (
	"fmt"
	"os"
	"os/exec"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
	tea "github.com/charmbracelet/bubbletea"
)

// runCommand returns a Bubble Tea command that runs the underlying CLI
// action and captures its combined output (truncated in the UI).
func runCommand(item menuItem) tea.Cmd {
	return func() tea.Msg {
		if len(item.command) == 0 {
			return commandFinishedMsg{
				item:   item,
				output: "no command configured",
				err:    fmt.Errorf("no command configured"),
			}
		}

		name := item.command[0]
		args := item.command[1:]

		cmd := exec.Command(name, args...)
		cmd.Stdout = nil
		cmd.Stderr = nil

		out, err := cmd.CombinedOutput()
		return commandFinishedMsg{
			item:   item,
			output: string(out),
			err:    err,
		}
	}
}

// runUpdateGameGo runs the Go-based game updater and returns its logs.
func runUpdateGameGo() tea.Cmd {
	return func() tea.Msg {
		out, err := csm.UpdateGame()
		return commandFinishedMsg{
			item: menuItem{
				title: "Update CS2 game files",
				kind:  itemUpdateGameGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runDeployPluginsGo runs the Go-based plugin deployment across all servers.
func runDeployPluginsGo() tea.Cmd {
	return func() tea.Msg {
		out, err := csm.DeployPluginsToServers()
		return commandFinishedMsg{
			item: menuItem{
				title: "Deploy plugins to all servers",
				kind:  itemDeployPluginsGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runInstallMonitorGo configures the cron-based auto-update monitor using
// the Go implementation instead of shell scripts.
func runInstallMonitorGo() tea.Cmd {
	return func() tea.Msg {
		title := "Install/redeploy auto-update monitor (sudo)"

		// This action must be run as root because it modifies root's crontab
		// and writes to /var/log. Rather than trying to handle sudo prompts
		// inside the TUI, we guide the user to rerun CSM with sudo.
		if os.Geteuid() != 0 {
			out := "The auto-update monitor must be installed as root.\n\n" +
				"Please restart CSM with sudo and run this action again:\n\n" +
				"  sudo ./csm\n\n" +
				"Or run the CLI command directly from your shell:\n\n" +
				"  sudo ./csm install-monitor-cron\n"

			return commandFinishedMsg{
				item: menuItem{
					title: title,
					kind:  itemInstallMonitorGo,
				},
				output: out,
				err:    nil,
			}
		}

		out, err := csm.InstallAutoUpdateCron("")
		return commandFinishedMsg{
			item: menuItem{
				title: title,
				kind:  itemInstallMonitorGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runStartAllServers starts all servers via the Go tmux manager.
func runStartAllServers() tea.Cmd {
	return func() tea.Msg {
		mgr, err := csm.NewTmuxManager()
		if err != nil {
			return commandFinishedMsg{
				item:   menuItem{title: "Start all servers"},
				output: "",
				err:    err,
			}
		}
		err = mgr.StartAll()
		return commandFinishedMsg{
			item:   menuItem{title: "Start all servers"},
			output: "",
			err:    err,
		}
	}
}

// runStopAllServers stops all servers via the Go tmux manager.
func runStopAllServers() tea.Cmd {
	return func() tea.Msg {
		mgr, err := csm.NewTmuxManager()
		if err != nil {
			return commandFinishedMsg{
				item:   menuItem{title: "Stop all servers"},
				output: "",
				err:    err,
			}
		}
		err = mgr.StopAll()
		return commandFinishedMsg{
			item:   menuItem{title: "Stop all servers"},
			output: "",
			err:    err,
		}
	}
}

// runRestartAllServers restarts all servers via the Go tmux manager.
func runRestartAllServers() tea.Cmd {
	return func() tea.Msg {
		mgr, err := csm.NewTmuxManager()
		if err != nil {
			return commandFinishedMsg{
				item:   menuItem{title: "Restart all servers"},
				output: "",
				err:    err,
			}
		}
		err = mgr.RestartAll()
		return commandFinishedMsg{
			item:   menuItem{title: "Restart all servers"},
			output: "",
			err:    err,
		}
	}
}

// runPublicIP resolves and prints the public IP using the Go implementation.
func runPublicIP() tea.Cmd {
	return func() tea.Msg {
		out, err := csm.PublicIP()
		return commandFinishedMsg{
			item: menuItem{
				title: "Show public IP",
				kind:  itemPublicIPGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runInstallDepsGo installs core system dependencies (tmux, steamcmd, rsync,
// jq, etc.) using the Go-native helper. This must be run as root.
func runInstallDepsGo() tea.Cmd {
	return func() tea.Msg {
		title := "Install system dependencies (sudo)"

		if os.Geteuid() != 0 {
			out := "System dependency installation must be run as root.\n\n" +
				"Please restart CSM with sudo and run this action again:\n\n" +
				"  sudo ./csm\n\n" +
				"Or run the CLI command directly from your shell:\n\n" +
				"  sudo ./csm install-deps\n"

			return commandFinishedMsg{
				item: menuItem{
					title: title,
					kind:  itemInstallDepsGo,
				},
				output: out,
				err:    nil,
			}
		}

		out, err := csm.InstallDependencies()
		return commandFinishedMsg{
			item: menuItem{
				title: title,
				kind:  itemInstallDepsGo,
			},
			output: out,
			err:    err,
		}
	}
}



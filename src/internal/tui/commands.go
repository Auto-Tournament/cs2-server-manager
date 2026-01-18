package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
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
		// Wire a cancellable context so the user can press C to abort a long
		// update-game run without quitting the TUI. The CancelInstall helper
		// is also used by the multi-step install wizard.
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := withUpdateGameLogTail(func() (string, error) {
			return csm.UpdateGameWithContext(ctx)
		})
		return commandFinishedMsg{
			item: menuItem{
				title: "Update CS2 after Valve update",
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
		// Wire a cancellable context so the user can press C to abort a long
		// plugin update/deploy run without quitting the TUI.
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := withPluginsLogTail(func() (string, error) {
			return csm.UpdateAndDeployPluginsWithContext(ctx)
		})
		return commandFinishedMsg{
			item: menuItem{
				title: "Update plugins on all servers",
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
		title := "Install or redeploy auto-update monitor (cron)"

		// This action must be run as root because it modifies root's crontab
		// and writes to /var/log. Rather than trying to handle sudo prompts
		// inside the TUI, we guide the user to rerun CSM with sudo.
		if os.Geteuid() != 0 {
			out := "The auto-update monitor must be installed as root.\n\n" +
				"Please restart CSM with sudo and run this action again:\n\n" +
				"  sudo csm\n\n" +
				"Or run the CLI command directly from your shell:\n\n" +
				"  sudo csm install-monitor-cron\n"

			return commandFinishedMsg{
				item: menuItem{
					title: title,
					kind:  itemInstallMonitorGo,
				},
				output: out,
				err:    nil,
			}
		}

		// Wire a cancellable context so the user can press C to abort if the
		// cron modification hangs for any reason.
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := csm.InstallAutoUpdateCronWithContext(ctx, "")
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
				item:   menuItem{title: "Start all servers", kind: itemStartAllGo},
				output: "",
				err:    err,
			}
		}
		err = mgr.StartAll()
		out := ""
		if err == nil {
			out = fmt.Sprintf("Started %d server(s) via tmux.\n\nUse the Servers dashboard or `csm attach <n>` to inspect them.", mgr.NumServers)
		}
		return commandFinishedMsg{
			item:   menuItem{title: "Start all servers", kind: itemStartAllGo},
			output: out,
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
				item:   menuItem{title: "Stop all servers", kind: itemStopAllGo},
				output: "",
				err:    err,
			}
		}
		err = mgr.StopAll()
		out := ""
		if err == nil {
			out = fmt.Sprintf("Stopped %d server(s) via tmux.", mgr.NumServers)
		}
		return commandFinishedMsg{
			item:   menuItem{title: "Stop all servers", kind: itemStopAllGo},
			output: out,
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
				item:   menuItem{title: "Restart all servers", kind: itemRestartAllGo},
				output: "",
				err:    err,
			}
		}
		err = mgr.RestartAll()
		out := ""
		if err == nil {
			out = fmt.Sprintf("Restarted %d server(s) via tmux.", mgr.NumServers)
		}
		return commandFinishedMsg{
			item:   menuItem{title: "Restart all servers", kind: itemRestartAllGo},
			output: out,
			err:    err,
		}
	}
}

// runMatchzyDBDetail runs the MatchZy DB verification/repair flow via the
// Go-native csm.VerifyMatchzyDB helper and shows the output on a simple
// action-result page instead of a scrollable viewport. The content is
// typically short enough that scrolling is unnecessary.
func runMatchzyDBDetail() tea.Cmd {
	return func() tea.Msg {
		out, err := csm.VerifyMatchzyDB()
		return commandFinishedMsg{
			item: menuItem{
				title: "MatchZy DB: verify/repair",
				kind:  itemMatchzyDBViewport,
			},
			output: out,
			err:    err,
		}
	}
}

// runAddServersGo scales up by creating N additional server instances based on
// the existing layout for the detected CS2 user.
func runAddServersGo(n int) tea.Cmd {
	return func() tea.Msg {
		// Stream scale-up progress by mirroring logs into a temp file that a
		// background goroutine tails, similar to the install wizard.
		logPath := filepath.Join(os.TempDir(), "csm-scale.log")
		_ = os.Remove(logPath)

		done := make(chan struct{})
		go tailInstallLog(logPath, done)
		defer close(done)

		_ = os.Setenv("CSM_SCALE_LOG", logPath)
		defer os.Unsetenv("CSM_SCALE_LOG")

		// Wire a cancellable context so the user can press C to cancel the
		// scale-up and roll back any newly created servers from this run.
		ctx, cancel := context.WithCancel(context.Background())
		SetScaleCancel(cancel)
		defer CancelScale()

		out, err := csm.AddServersWithContext(ctx, n)
		return commandFinishedMsg{
			item: menuItem{
				title: "Add servers",
				kind:  itemAddServerGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runUpdateServerConfigsGoWithConfig updates server configurations with specific values.
// This is called from the config editor after the user edits values.
func runUpdateServerConfigsGoWithConfig(cfg csm.UpdateServerConfigsConfig) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := csm.UpdateServerConfigsWithContext(ctx, cfg)
		return commandFinishedMsg{
			item: menuItem{
				title: "Update server configs",
				kind:  itemUpdateServerConfigs,
			},
			output: out,
			err:    err,
		}
	}
}

// runRemoveServersGo scales down by stopping and deleting the highest-numbered
// N server-* directories so NewTmuxManager will subsequently report fewer
// servers.
func runRemoveServersGo(n int) tea.Cmd {
	return func() tea.Msg {
		// Stream scale-down progress by mirroring logs into a temp file that a
		// background goroutine tails, similar to the install wizard and
		// AddServers flow.
		logPath := filepath.Join(os.TempDir(), "csm-scale.log")
		_ = os.Remove(logPath)

		done := make(chan struct{})
		go tailInstallLog(logPath, done)
		defer close(done)

		_ = os.Setenv("CSM_SCALE_LOG", logPath)
		defer os.Unsetenv("CSM_SCALE_LOG")

		// Wire a cancellable context so the user can press C to cancel the
		// scale-down and stop removing additional servers. Servers already
		// removed are not restored.
		ctx, cancel := context.WithCancel(context.Background())
		SetScaleCancel(cancel)
		defer CancelScale()

		out, err := csm.RemoveServersWithContext(ctx, n)
		return commandFinishedMsg{
			item: menuItem{
				title: "Remove servers",
				kind:  itemRemoveServerGo,
			},
			output: out,
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
		title := "Install system dependencies"

		if os.Geteuid() != 0 {
			out := "System dependency installation must be run as root.\n\n" +
				"Please restart CSM with sudo and run this action again:\n\n" +
				"  sudo csm\n\n" +
				"Or run the CLI command directly from your shell:\n\n" +
				"  sudo csm install-deps\n"

			return commandFinishedMsg{
				item: menuItem{
					title: title,
					kind:  itemInstallDepsGo,
				},
				output: out,
				err:    nil,
			}
		}

		// Stream dependency installation progress by mirroring logs into a
		// temp file that a background goroutine tails.
		logPath := filepath.Join(os.TempDir(), "csm-deps.log")
		_ = os.Remove(logPath)

		done := make(chan struct{})
		go tailInstallLog(logPath, done)
		defer close(done)

		_ = os.Setenv("CSM_DEPS_LOG", logPath)
		defer os.Unsetenv("CSM_DEPS_LOG")

		// Wire a cancellable context so the user can press C to abort long
		// apt-get runs without quitting the TUI.
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := csm.InstallDependenciesWithContext(ctx)
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

// runExtractThumbnailsGo runs the Go-based map thumbnail extraction pipeline.
// It mirrors the old VPK + thumbnail scripts and writes PNGs into
// map_thumbnails/ under the current working directory. While running, it
// streams progress into a temp log that the TUI tails so users can see live
// steps (found files, conversions, etc.).
func runExtractThumbnailsGo() tea.Cmd {
	return func() tea.Msg {
		// Stream thumbnail extraction progress by mirroring logs into a temp
		// file that a background goroutine tails.
		logPath := filepath.Join(os.TempDir(), "csm-thumbnails.log")
		_ = os.Remove(logPath)

		done := make(chan struct{})
		go tailInstallLog(logPath, done)
		defer close(done)

		_ = os.Setenv("CSM_THUMBS_LOG", logPath)
		defer os.Unsetenv("CSM_THUMBS_LOG")

		// Wire a cancellable context so the user can press C to abort a long
		// thumbnail extraction/conversion run without quitting the TUI.
		ctx, cancel := context.WithCancel(context.Background())
		SetInstallCancel(cancel)
		defer CancelInstall()

		out, err := csm.ExtractMapThumbnailsWithContext(ctx)
		return commandFinishedMsg{
			item: menuItem{
				title: "Extract map thumbnails",
				kind:  itemExtractThumbnailsGo,
			},
			output: out,
			err:    err,
		}
	}
}

// runCleanupAllGo runs the dangerous "cleanup all" operation that removes all
// CS2 servers, data, and the CS2 user. This should only be exposed behind a
// clear confirmation step in the TUI.
func runCleanupAllGo() tea.Cmd {
	return func() tea.Msg {
		cfg := csm.CleanupConfig{
			CS2User:          os.Getenv("CS2_USER"),
			MatchzyContainer: os.Getenv("MATCHZY_DB_CONTAINER"),
			MatchzyVolume:    os.Getenv("MATCHZY_DB_VOLUME"),
		}
		out, err := csm.CleanupAll(cfg)
		return commandFinishedMsg{
			item: menuItem{
				title: "Danger zone: wipe all servers and CS2 user",
				kind:  itemCleanupAllGo,
			},
			output: out,
			err:    err,
		}
	}
}

// withPluginsLogTail wraps a long-running plugin update/deploy operation so
// that logs are mirrored into a temp file (CSM_PLUGINS_LOG) which the TUI
// tails to show live progress. Both the install wizard and the standalone
// "Update plugins on all servers" action reuse this helper to avoid
// duplicating the env+tailing wiring.
func withPluginsLogTail(run func() (string, error)) (string, error) {
	return withEnvLogTail("CSM_PLUGINS_LOG", "csm-plugins.log", run)
}

// withUpdateGameLogTail wraps the standalone "Update CS2 after Valve update"
// action so that its logs are mirrored into a temp file (CSM_UPDATE_GAME_LOG)
// which the TUI tails to show live SteamCMD + rsync progress.
func withUpdateGameLogTail(run func() (string, error)) (string, error) {
	return withEnvLogTail("CSM_UPDATE_GAME_LOG", "csm-update-game.log", run)
}

// withEnvLogTail is a small helper that wires up a temp log file and env var
// for any long-running operation that wants to stream progress via
// tailInstallLog. It is used by both plugin and update-game flows to avoid
// duplicating the env+tailing wiring.
func withEnvLogTail(envKey, tempName string, run func() (string, error)) (string, error) {
	logPath := filepath.Join(os.TempDir(), tempName)
	_ = os.Remove(logPath)

	done := make(chan struct{})
	go tailInstallLog(logPath, done)
	defer close(done)

	_ = os.Setenv(envKey, logPath)
	defer os.Unsetenv(envKey)

	return run()
}

// tailInstallLog runs in a background goroutine and periodically reads the log
// file at logPath, sending installLogTickMsg messages to the TUI program.
// It stops when the done channel is closed.
func tailInstallLog(logPath string, done <-chan struct{}) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var lastPos int64 = 0
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			f, err := os.Open(logPath)
			if err != nil {
				continue // File might not exist yet
			}

			// Seek to last known position
			if lastPos > 0 {
				f.Seek(lastPos, 0)
			}

			// Read new content
			data, err := io.ReadAll(f)
			if err == nil && len(data) > 0 {
				send(installLogTickMsg{lines: string(data)})
				lastPos, _ = f.Seek(0, io.SeekCurrent)
			}
			f.Close()
		}
	}
}

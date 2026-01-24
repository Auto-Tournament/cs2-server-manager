package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"

	csm "github.com/sivert-io/cs2-server-manager/src/internal/csm"
	tui "github.com/sivert-io/cs2-server-manager/src/internal/tui"
)

func main() {
	// Global flags and CLI subcommands. We parse flags first so that
	// "csm -d" and "csm -h" work, then interpret any remaining args as
	// subcommands. If no recognised subcommand is given, we fall back
	// to the TUI.
	fs := flag.NewFlagSet("csm", flag.ExitOnError)
	var daemonMode bool
	var showHelp bool
	fs.BoolVar(&daemonMode, "d", false, "run without TUI renderer (daemon mode)")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showHelp, "help", false, "show help")
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()

	if showHelp && len(args) == 0 {
		printUsage()
		return
	}

	if len(args) > 0 {
		switch args[0] {
		case "help":
			printUsage()
			return
		case "install-deps":
			out, err := csm.InstallDependencies()
			csm.LogAction("cli", "install-deps", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "dependency installation failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "bootstrap":
			cfg := csm.BootstrapConfig{
				// Prefer the same dedicated service user as the TUI install
				// wizard so CLI bootstrap and interactive installs remain in
				// sync by default.
				CS2User:        getenvDefault("CS2_USER", csm.DefaultCS2User),
				NumServers:     intFromEnv("NUM_SERVERS", csm.DefaultNumServers),
				BaseGamePort:   intFromEnv("BASE_GAME_PORT", csm.DefaultBaseGamePort),
				BaseTVPort:     intFromEnv("BASE_TV_PORT", csm.DefaultBaseTVPort),
				HostnamePrefix: getenvDefault("HOSTNAME_PREFIX", "CS2 Server"),
				EnableMetamod:  intFromEnv("ENABLE_METAMOD", 1) != 0,
				FreshInstall:   intFromEnv("FRESH_INSTALL", 0) != 0,
				UpdateMaster:   intFromEnv("UPDATE_MASTER", 1) != 0,
				RCONPassword:   getenvDefault("RCON_PASSWORD", csm.DefaultRCONPassword),

				MatchzySkipDocker: intFromEnv("MATCHZY_SKIP_DOCKER", 0) != 0,
				GameFilesDir:      getenvDefault("GAME_FILES_DIR", ""),
				OverridesDir:      getenvDefault("OVERRIDES_DIR", ""),
			}
			out, err := csm.Bootstrap(cfg)
			csm.LogAction("cli", "bootstrap", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "bootstrap failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "cleanup-all":
			cfg := csm.CleanupConfig{
				CS2User:          getenvDefault("CS2_USER", csm.DefaultCS2User),
				MatchzyContainer: getenvDefault("MATCHZY_DB_CONTAINER", csm.DefaultMatchzyContainerName),
				MatchzyVolume:    getenvDefault("MATCHZY_DB_VOLUME", csm.DefaultMatchzyVolumeName),
			}
			out, err := csm.CleanupAll(cfg)
			csm.LogAction("cli", "cleanup-all", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "cleanup failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "extract-map-data":
			out, err := csm.ExtractMapThumbnails()
			csm.LogAction("cli", "extract-map-data", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "map extraction failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "public-ip":
			ip, err := csm.PublicIP()
			if ip != "" || err != nil {
				csm.LogAction("cli", "public-ip", ip, err)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to resolve public IP: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(ip)
			return
		case "status":
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux status failed: %v\n", err)
				os.Exit(1)
			}
			out, err := mgr.Status()
			csm.LogAction("cli", "status", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux status failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "start":
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux start failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (sudo csm).")
				os.Exit(1)
			}
			target := "all"
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
					os.Exit(1)
				}
				if server > mgr.NumServers {
					fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
					os.Exit(1)
				}
				err = mgr.Start(server)
				target = fmt.Sprintf("server-%d", server)
			} else {
				err = mgr.StartAll()
			}
			csm.LogAction("cli", "start "+target, "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux start failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "stop":
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux stop failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (sudo csm).")
				os.Exit(1)
			}
			target := "all"
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
					os.Exit(1)
				}
				if server > mgr.NumServers {
					fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
					os.Exit(1)
				}
				err = mgr.Stop(server)
				target = fmt.Sprintf("server-%d", server)
			} else {
				err = mgr.StopAll()
			}
			csm.LogAction("cli", "stop "+target, "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux stop failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "restart":
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux restart failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (sudo csm).")
				os.Exit(1)
			}
			target := "all"
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
					os.Exit(1)
				}
				if server > mgr.NumServers {
					fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
					os.Exit(1)
				}
				err = mgr.Restart(server)
				target = fmt.Sprintf("server-%d", server)
			} else {
				err = mgr.RestartAll()
			}
			csm.LogAction("cli", "restart "+target, "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux restart failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "reinstall":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm reinstall <server>")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "This will completely rebuild the specified server from the master installation.")
				fmt.Fprintln(os.Stderr, "Use this if a server's game files are corrupted or incomplete.")
				os.Exit(1)
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to detect servers: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (sudo csm).")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
				os.Exit(1)
			}
			if server > mgr.NumServers {
				fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
				os.Exit(1)
			}
			out, err := csm.ReinstallServerInstance(server)
			csm.LogAction("cli", fmt.Sprintf("reinstall server-%d", server), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "reinstall server-%d failed: %v\n", server, err)
				os.Exit(1)
			}
			fmt.Printf("\nServer %d has been reinstalled successfully!\n", server)
			return
		case "update-config":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm update-config <server>")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Regenerates server.cfg and autoexec.cfg without reinstalling game files.")
				fmt.Fprintln(os.Stderr, "Much faster than reinstall when you just need to fix config issues.")
				os.Exit(1)
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to detect servers: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (sudo csm).")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
				os.Exit(1)
			}
			if server > mgr.NumServers {
				fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
				os.Exit(1)
			}
			out, err := csm.UpdateServerConfig(server)
			csm.LogAction("cli", fmt.Sprintf("update-config server-%d", server), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "update-config server-%d failed: %v\n", server, err)
				os.Exit(1)
			}
			fmt.Printf("\nServer %d config updated successfully!\n", server)
			return
		case "unban":
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "usage: csm unban <server> <ip>")
				fmt.Fprintln(os.Stderr, "       csm unban 0 <ip>  (unban from all servers)")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Removes an IP address from a server's banned_ip.cfg file.")
				fmt.Fprintln(os.Stderr, "Use this when an IP was incorrectly banned for RCON hacking attempts.")
				fmt.Fprintln(os.Stderr, "Use server number 0 to unban from all servers.")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Example: sudo csm unban 1 172.19.0.3")
				fmt.Fprintln(os.Stderr, "         sudo csm unban 0 172.19.0.3  (unban from all servers)")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server < 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be 0 for all servers, or a positive integer)\n", args[1])
				os.Exit(1)
			}
			ip := args[2]
			out, err := csm.UnbanIP(server, ip)
			csm.LogAction("cli", fmt.Sprintf("unban %d %s", server, ip), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "unban failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "unban-all":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm unban-all <server>")
				fmt.Fprintln(os.Stderr, "       csm unban-all 0  (clear all bans from all servers)")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Removes all IP addresses from a server's banned_ip.cfg file.")
				fmt.Fprintln(os.Stderr, "Use this to clear all IPs that were banned for RCON hacking attempts.")
				fmt.Fprintln(os.Stderr, "Use server number 0 to clear bans from all servers.")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Example: sudo csm unban-all 1")
				fmt.Fprintln(os.Stderr, "         sudo csm unban-all 0  (clear all bans from all servers)")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server < 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be 0 for all servers, or a positive integer)\n", args[1])
				os.Exit(1)
			}
			out, err := csm.UnbanAllIPs(server)
			csm.LogAction("cli", fmt.Sprintf("unban-all %d", server), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "unban-all failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "list-bans":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm list-bans <server>")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Lists all banned IP addresses for the specified server.")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", args[1])
				os.Exit(1)
			}
			ips, err := csm.ListBannedIPs(server)
			csm.LogAction("cli", fmt.Sprintf("list-bans %d", server), "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "list-bans failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(ips)
			return
		case "logs":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm logs <server> [lines]")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
				os.Exit(1)
			}
			lines := 0
			if len(args) > 2 {
				n, nerr := strconv.Atoi(args[2])
				if nerr != nil {
					fmt.Fprintf(os.Stderr, "invalid line count %q\n", args[2])
					os.Exit(1)
				}
				lines = n
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux logs failed: %v\n", err)
				os.Exit(1)
			}
			out, err := mgr.Logs(server, lines)
			csm.LogAction("cli", fmt.Sprintf("logs server-%d", server), out, err)
			if out != "" {
				fmt.Print(out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Println()
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux logs failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "logs-file":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm logs-file <server>")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
				os.Exit(1)
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux logs-file failed: %v\n", err)
				os.Exit(1)
			}
			path := mgr.ServerLogPath(server)
			csm.LogAction("cli", fmt.Sprintf("logs-file server-%d", server), path, nil)
			fmt.Println(path)
			return
		case "attach":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm attach <server>")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
				os.Exit(1)
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux attach failed: %v\n", err)
				os.Exit(1)
			}
			attachErr := mgr.Attach(server)
			csm.LogAction("cli", fmt.Sprintf("attach server-%d", server), "", attachErr)
			if attachErr != nil {
				fmt.Fprintf(os.Stderr, "tmux attach failed: %v\n", attachErr)
				os.Exit(1)
			}
			return
		case "list-sessions":
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux list failed: %v\n", err)
				os.Exit(1)
			}
			out, err := mgr.ListSessions()
			csm.LogAction("cli", "list-sessions", out, err)
			if out != "" {
				fmt.Print(out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Println()
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux list failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "debug":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm debug <server>")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
				os.Exit(1)
			}
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "debug failed: %v\n", err)
				os.Exit(1)
			}
			debugErr := mgr.Debug(server)
			csm.LogAction("cli", fmt.Sprintf("debug server-%d", server), "", debugErr)
			if debugErr != nil {
				fmt.Fprintf(os.Stderr, "debug failed: %v\n", debugErr)
				os.Exit(1)
			}
			return
		case "update-game":
			out, err := csm.UpdateGame()
			csm.LogAction("cli", "update-game", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "game update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-server":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm update-server <server>")
				os.Exit(1)
			}
			sn, serr := strconv.Atoi(args[1])
			if serr != nil || sn <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
				os.Exit(1)
			}
			out, err := csm.UpdateServerWithContext(context.Background(), sn)
			csm.LogAction("cli", fmt.Sprintf("update-server-%d", sn), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "update-server %d failed: %v\n", sn, err)
				os.Exit(1)
			}
			return
		case "update-plugins":
			// For CLI convenience, perform both the download and deploy steps.
			if out, err := csm.UpdatePlugins(); out != "" || err != nil {
				csm.LogAction("cli", "update-plugins-download", out, err)
				if out != "" {
					fmt.Print(out)
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "plugin download failed: %v\n", err)
					os.Exit(1)
				}
			}
			if out, err := csm.DeployPluginsToServers(); out != "" || err != nil {
				csm.LogAction("cli", "update-plugins-deploy", out, err)
				if out != "" {
					fmt.Print(out)
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "plugin deployment failed: %v\n", err)
					os.Exit(1)
				}
			}
			return
		case "monitor":
			err := csm.RunAutoUpdateMonitor()
			csm.LogAction("cli", "monitor", "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "auto-update monitor failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "install-monitor-cron":
			interval := ""
			if len(args) > 1 {
				interval = args[1]
			}
			out, err := csm.InstallAutoUpdateCron(interval)
			csm.LogAction("cli", "install-monitor-cron", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to install auto-update cronjob: %v\n", err)
				os.Exit(1)
			}
			return
		case "remove-monitor-cron":
			out, err := csm.RemoveAutoUpdateCron()
			csm.LogAction("cli", "remove-monitor-cron", out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to remove auto-update cronjob: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "Unrecognized command: %q\n\n", args[0])
			printUsage()
			os.Exit(1)
		}
	}

	// No subcommand matched: run the TUI. For safety and to simplify behaviour,
	// the interactive TUI requires sudo so that all install/update/cleanup
	// flows can run without surprising permission errors.
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "CSM TUI must be run with sudo so it can manage users, tmux, game files and cron jobs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Please restart CSM with:")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  sudo csm")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "You can still run non-TUI commands without sudo where appropriate, e.g.:")
		fmt.Fprintln(os.Stderr, "  csm status")
		fmt.Fprintln(os.Stderr, "  csm logs <server>")
		fmt.Fprintln(os.Stderr, "  csm attach <server>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "For a full list of commands and which require sudo, run:")
		fmt.Fprintln(os.Stderr, "  csm -h")
		os.Exit(1)
	}

	// No subcommand matched and we are root: run the TUI. If we're in daemon
	// mode or stdout is not a TTY, disable the renderer. Otherwise, use
	// full-screen TUI.
	//
	// Always enable Bubble Tea file logging so we can debug TUI behaviour even
	// when stdout is occupied. Logs go to CSM_LOG_DIR (default: current
	// directory) as csm.log, shared with other CSM log helpers.
	logDir := getenvDefault("CSM_LOG_DIR", ".")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "failed to create CSM_LOG_DIR:", err)
	} else {
		logPath := filepath.Join(logDir, "csm.log")
		if f, err := tea.LogToFile(logPath, "debug"); err != nil {
			fmt.Fprintln(os.Stderr, "failed to enable debug logging:", err)
		} else {
			defer f.Close()
		}
	}

	var opts []tea.ProgramOption
	if daemonMode || !isatty.IsTerminal(os.Stdout.Fd()) {
		opts = append(opts, tea.WithoutRenderer())
	} else {
		opts = append(opts, tea.WithAltScreen())
	}

	p := tea.NewProgram(tui.New(), opts...)
	tui.SetProgram(p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// Small helpers for CLI env parsing to keep csm package independent of os.

func getenvDefault(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func intFromEnv(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func printUsage() {
	// Use a small amount of ANSI color in CLI help to make sections easier to
	// scan in a terminal without affecting non-interactive usage.
	const bold = "\x1b[1m"
	const reset = "\x1b[0m"
	const cyan = "\x1b[36m"
	const yellow = "\x1b[33m"

	fmt.Printf("%sCSM - CS2 Server Manager%s\n", bold, reset)
	fmt.Println()
	fmt.Printf("%sUsage:%s\n", cyan, reset)
	fmt.Println("  csm [flags]")
	fmt.Println("  csm <command> [args]")
	fmt.Println()
	fmt.Printf("%sFlags:%s\n", cyan, reset)
	fmt.Println("  -d           run without TUI renderer (daemon mode)")
	fmt.Println("  -h, --help   show this help message")
	fmt.Println()
	fmt.Printf("%sCommands (no sudo required):%s\n", cyan, reset)
	fmt.Println("  public-ip              Print public IP address")
	fmt.Println("  status                 Show tmux server status")
	fmt.Println("  start|stop|restart     Control servers via tmux")
	fmt.Println("  logs                   Tail server logs (scrolling)")
	fmt.Println("  logs-file              Show the raw log file path for a server")
	fmt.Println("  attach                 Attach to a server tmux session")
	fmt.Println("  list-sessions          List tmux sessions")
	fmt.Println("  debug                  Run a server in foreground debug mode")
	fmt.Println("  extract-map-data       Extract map thumbnails from VPKs (PNG + WEBP, plus 1280px WEBP thumbnails)")
	fmt.Println("  list-bans <server>     List banned IP addresses for a server")
	fmt.Println()
	fmt.Printf("%sCommands (require sudo for typical setups):%s\n", yellow, reset)
	fmt.Println("  bootstrap              Install/redeploy servers (non-interactive)")
	fmt.Println("  cleanup-all            Remove all servers and related resources")
	fmt.Println("  reinstall <server>     Rebuild a single server from master (fixes corrupted files)")
	fmt.Println("  update-config <server> Regenerate server configs without reinstalling")
	fmt.Println("  unban <server> <ip>    Remove IP from banned RCON requests (use 0 for all servers)")
	fmt.Println("  unban-all <server>     Clear all IPs banned for RCON attempts (use 0 for all servers)")
	fmt.Println("  update-game            Update CS2 game files after a Valve update")
	fmt.Println("  update-plugins         Update plugins and deploy to servers")
	fmt.Println("  monitor                Run auto-update monitor loop")
	fmt.Println("  install-monitor-cron   Install auto-update monitor cronjob")
	fmt.Println("  remove-monitor-cron    Remove auto-update monitor cronjob")
	fmt.Println("  install-deps           Install system dependencies")
	fmt.Println()
	fmt.Println("If no command is given, the interactive TUI is started.")
}

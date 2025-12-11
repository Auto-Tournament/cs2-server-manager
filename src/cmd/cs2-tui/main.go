package main

import (
	"flag"
	"fmt"
	"io"
	"log"
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
	// Ensure we run from the repository root so relative paths resolve
	// correctly, even when the binary is invoked from elsewhere.
	if exePath, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exePath); dir != "" {
			// If we are in src/cmd/cs2-tui (or similar), move one level up.
			if strings.HasSuffix(dir, string(filepath.Join("src", "cmd", "cs2-tui"))) {
				_ = os.Chdir(filepath.Dir(filepath.Dir(dir)))
			}
		}
	}

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
            		case "install-deps":
            			out, err := csm.InstallDependencies()
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
				CS2User:          getenvDefault("CS2_USER", "cs2"),
				NumServers:       intFromEnv("NUM_SERVERS", 3),
				BaseGamePort:     intFromEnv("BASE_GAME_PORT", 27015),
				BaseTVPort:       intFromEnv("BASE_TV_PORT", 27020),
				EnableMetamod:    intFromEnv("ENABLE_METAMOD", 1) != 0,
				FreshInstall:     intFromEnv("FRESH_INSTALL", 0) != 0,
				UpdateMaster:     intFromEnv("UPDATE_MASTER", 1) != 0,
				RCONPassword:     getenvDefault("RCON_PASSWORD", "ntlan2025"),
				MatchzySkipDocker: intFromEnv("MATCHZY_SKIP_DOCKER", 0) != 0,
				GameFilesDir:     getenvDefault("GAME_FILES_DIR", ""),
				OverridesDir:     getenvDefault("OVERRIDES_DIR", ""),
			}
			out, err := csm.Bootstrap(cfg)
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
				CS2User:          getenvDefault("CS2_USER", "cs2"),
				MatchzyContainer: getenvDefault("MATCHZY_DB_CONTAINER", "matchzy-mysql"),
				MatchzyVolume:    getenvDefault("MATCHZY_DB_VOLUME", "matchzy-mysql-data"),
			}
			out, err := csm.CleanupAll(cfg)
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
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil {
					fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
					os.Exit(1)
				}
				err = mgr.Start(server)
			} else {
				err = mgr.StartAll()
			}
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
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil {
					fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
					os.Exit(1)
				}
				err = mgr.Stop(server)
			} else {
				err = mgr.StopAll()
			}
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
			if len(args) > 1 {
				server, serr := strconv.Atoi(args[1])
				if serr != nil {
					fmt.Fprintf(os.Stderr, "invalid server number %q\n", args[1])
					os.Exit(1)
				}
				err = mgr.Restart(server)
			} else {
				err = mgr.RestartAll()
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux restart failed: %v\n", err)
				os.Exit(1)
			}
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
			if err := mgr.Attach(server); err != nil {
				fmt.Fprintf(os.Stderr, "tmux attach failed: %v\n", err)
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
			if err := mgr.Debug(server); err != nil {
				fmt.Fprintf(os.Stderr, "debug failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-game":
			out, err := csm.UpdateGame()
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "game update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-plugins":
			// For CLI convenience, perform both the download and deploy steps.
			if out, err := csm.UpdatePlugins(); out != "" || err != nil {
				if out != "" {
					fmt.Print(out)
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "plugin download failed: %v\n", err)
					os.Exit(1)
				}
			}
			if out, err := csm.DeployPluginsToServers(); out != "" || err != nil {
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
			if err := csm.RunAutoUpdateMonitor(); err != nil {
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
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to install auto-update cronjob: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// No subcommand matched: run the TUI. If we're in daemon mode or stdout is
	// not a TTY, disable the renderer. Otherwise, use full-screen TUI and
	// silence log output to avoid mixing logs into the UI.
	var opts []tea.ProgramOption
	if daemonMode || !isatty.IsTerminal(os.Stdout.Fd()) {
		opts = append(opts, tea.WithoutRenderer())
	} else {
		opts = append(opts, tea.WithAltScreen())
		log.SetOutput(io.Discard)
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
	fmt.Println("CSM - CS2 Server Manager")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  csm [flags]")
	fmt.Println("  csm <command> [args]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -d           run without TUI renderer (daemon mode)")
	fmt.Println("  -h, --help   show this help message")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  bootstrap              Install/redeploy servers (non-interactive)")
	fmt.Println("  cleanup-all            Remove all servers and related resources")
	fmt.Println("  extract-map-data       Extract map thumbnails from VPKs")
	fmt.Println("  public-ip              Print public IP address")
	fmt.Println("  status                 Show tmux server status")
	fmt.Println("  start|stop|restart     Control servers via tmux")
	fmt.Println("  logs                   Tail server logs")
	fmt.Println("  attach                 Attach to a server tmux session")
	fmt.Println("  list-sessions          List tmux sessions")
	fmt.Println("  debug                  Attach in debug mode")
	fmt.Println("  update-game            Update CS2 game files")
	fmt.Println("  update-plugins         Update plugins and deploy to servers")
	fmt.Println("  monitor                Run auto-update monitor")
	fmt.Println("  install-monitor-cron   Install auto-update monitor cronjob")
	fmt.Println("  install-deps           Install system dependencies (sudo)")
	fmt.Println()
	fmt.Println("If no command is given, the interactive TUI is started.")
}


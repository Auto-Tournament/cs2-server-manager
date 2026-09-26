package main

import (
	"bufio"
	"context"
	"errors"
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
	var copyMode string
	fs.BoolVar(&daemonMode, "d", false, "run without TUI renderer (daemon mode)")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.StringVar(&copyMode, "copy-mode", "", "copy mode for master→server replication (auto|reflink|rsync|legacy)")
	_ = fs.Parse(os.Args[1:])
	args := fs.Args()

	// Global copy-mode override: export as env so internal helpers can read it.
	if strings.TrimSpace(copyMode) != "" {
		_ = os.Setenv("CSM_COPY_MODE", strings.TrimSpace(copyMode))
	}

	if showHelp && len(args) == 0 {
		printUsage()
		return
	}

	if len(args) > 0 {
		switch args[0] {
		case "help":
			printUsage()
			return
		case "setup-host":
			hfs := flag.NewFlagSet("setup-host", flag.ExitOnError)
			skipDeps := hfs.Bool("skip-deps", false, "don't apt-get install dependencies (use on a host that already runs servers)")
			skipLinger := hfs.Bool("skip-linger", false, "don't run loginctl enable-linger")
			_ = hfs.Parse(args[1:])
			var buf strings.Builder
			err := csm.SetupHost(context.Background(), &buf, csm.SetupHostOptions{
				CS2User:    getenvDefault("CS2_USER", csm.DefaultCS2User),
				SkipDeps:   *skipDeps,
				SkipLinger: *skipLinger,
			})
			csm.LogAction("cli", "setup-host", buf.String(), err)
			fmt.Print(buf.String())
			if err != nil {
				fmt.Fprintf(os.Stderr, "setup-host failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "ci":
			cmd, err := csm.ParseCIArgs(args[1:])
			if errors.Is(err, flag.ErrHelp) {
				fmt.Print(csm.CIUsage)
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "csm ci: %v\n\n%s", err, csm.CIUsage)
				os.Exit(2)
			}
			// Output streams to the terminal (with the token redacted); the
			// action log only records the result.
			err = csm.RunCI(context.Background(), os.Stdout, cmd)
			csm.LogAction("cli", "ci "+cmd.Action, "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "csm ci %s failed: %v\n", cmd.Action, err)
				os.Exit(1)
			}
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
				// Empty keeps the password already in cs2-config's
				// server.cfg (falls back to the default on a first install).
				RCONPassword: getenvDefault("RCON_PASSWORD", ""),

				MatchzySkipDocker: intFromEnv("MATCHZY_SKIP_DOCKER", 0) != 0,
				// MATCHZY_DB_ENGINE=sqlite gives each server its own SQLite
				// database; mysql (or unset) keeps the shared MySQL database.
				DBEngine:     getenvDefault("MATCHZY_DB_ENGINE", ""),
				GameFilesDir: getenvDefault("GAME_FILES_DIR", ""),
				OverridesDir: getenvDefault("OVERRIDES_DIR", ""),
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
			mfs := flag.NewFlagSet("extract-map-data", flag.ExitOnError)
			publish := mfs.Bool("publish", false, "commit map_thumbnails/ into a cs2-server-manager checkout on a new branch")
			repoDir := mfs.String("repo", "", "path to the cs2-server-manager git checkout used by --publish")
			_ = mfs.Parse(args[1:])
			if *publish && strings.TrimSpace(*repoDir) == "" {
				fmt.Fprintln(os.Stderr, "--publish needs --repo <dir> (a git checkout of cs2-server-manager)")
				os.Exit(2)
			}
			// Progress streams to stdout as it happens; out is only kept
			// for the action log.
			out, res, err := csm.ExtractMapData(context.Background(), csm.MapDataOptions{Progress: os.Stdout})
			csm.LogAction("cli", "extract-map-data", out, err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "map extraction failed: %v\n", err)
				os.Exit(1)
			}
			if *publish {
				fmt.Println()
				if err := csm.PublishMapThumbnails(context.Background(), res.ThumbsDir, *repoDir, res.PatchVersion, os.Stdout); err != nil {
					fmt.Fprintf(os.Stderr, "publish failed: %v\n", err)
					os.Exit(1)
				}
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
			if err := runStatusCommand(args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "status failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "start":
			startFS := flag.NewFlagSet("start", flag.ExitOnError)
			var startAlternate bool
			var startBinary bool
			startFS.BoolVar(&startAlternate, "alternate", false, "use alternate launcher (game/csm.sh) instead of Valve's cs2.sh")
			startFS.BoolVar(&startBinary, "binary", false, "run the cs2 binary directly (not recommended; troubleshooting only)")
			_ = startFS.Parse(args[1:])
			startArgs := startFS.Args()

			if err := csm.ApplyLaunchModeFlags(startAlternate, startBinary); err != nil {
				fmt.Fprintf(os.Stderr, "start: %v\n", err)
				os.Exit(1)
			}

			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux start failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (csm, as the CS2 user or root).")
				os.Exit(1)
			}
			target := "all"
			if len(startArgs) > 0 {
				server, serr := strconv.Atoi(startArgs[0])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", startArgs[0])
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
			stopForce, stopArgs := extractForce(args[1:])
			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux stop failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (csm, as the CS2 user or root).")
				os.Exit(1)
			}
			target := "all"
			if len(stopArgs) > 0 {
				server, serr := strconv.Atoi(stopArgs[0])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", stopArgs[0])
					os.Exit(1)
				}
				if server > mgr.NumServers {
					fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
					os.Exit(1)
				}
				gateOrExit(mgr, fmt.Sprintf("stop server-%d", server), []int{server}, stopForce)
				err = mgr.Stop(server)
				target = fmt.Sprintf("server-%d", server)
			} else {
				gateOrExit(mgr, "stop", nil, stopForce)
				err = mgr.StopAll()
			}
			csm.LogAction("cli", "stop "+target, "", err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux stop failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "restart":
			restartFS := flag.NewFlagSet("restart", flag.ExitOnError)
			var restartAlternate bool
			var restartBinary bool
			restartFS.BoolVar(&restartAlternate, "alternate", false, "use alternate launcher (game/csm.sh) instead of Valve's cs2.sh")
			restartFS.BoolVar(&restartBinary, "binary", false, "run the cs2 binary directly (not recommended; troubleshooting only)")
			restartForce, restartRaw := extractForce(args[1:])
			_ = restartFS.Parse(restartRaw)
			restartArgs := restartFS.Args()

			if err := csm.ApplyLaunchModeFlags(restartAlternate, restartBinary); err != nil {
				fmt.Fprintf(os.Stderr, "restart: %v\n", err)
				os.Exit(1)
			}

			mgr, err := csm.NewTmuxManager()
			if err != nil {
				fmt.Fprintf(os.Stderr, "tmux restart failed: %v\n", err)
				os.Exit(1)
			}
			if mgr.NumServers == 0 {
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (csm, as the CS2 user or root).")
				os.Exit(1)
			}
			target := "all"
			if len(restartArgs) > 0 {
				server, serr := strconv.Atoi(restartArgs[0])
				if serr != nil || server <= 0 {
					fmt.Fprintf(os.Stderr, "invalid server number %q (must be a positive integer)\n", restartArgs[0])
					os.Exit(1)
				}
				if server > mgr.NumServers {
					fmt.Fprintf(os.Stderr, "server-%d does not exist (only %d server(s) installed)\n", server, mgr.NumServers)
					os.Exit(1)
				}
				gateOrExit(mgr, fmt.Sprintf("restart server-%d", server), []int{server}, restartForce)
				err = mgr.Restart(server)
				target = fmt.Sprintf("server-%d", server)
			} else {
				gateOrExit(mgr, "restart", nil, restartForce)
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
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (csm, as the CS2 user or root).")
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
				fmt.Fprintln(os.Stderr, "No servers found. Run the install wizard first (csm, as the CS2 user or root).")
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
				fmt.Fprintln(os.Stderr, "Example: csm unban 1 172.19.0.3")
				fmt.Fprintln(os.Stderr, "         csm unban 0 172.19.0.3  (unban from all servers)")
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
				fmt.Fprintln(os.Stderr, "Example: csm unban-all 1")
				fmt.Fprintln(os.Stderr, "         csm unban-all 0  (clear all bans from all servers)")
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
		case "doctor":
			// Doctor runs as root or as the CS2 user (user mode). A few fixes
			// (e.g. the /usr/bin/steamcmd wrapper) still need root.
			if !csm.CanManageServers() {
				fmt.Fprintln(os.Stderr, "csm doctor must be run as root or as the CS2 user so it can apply fixes.")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Run it as the CS2 user (after a one-time `sudo csm setup-host`):")
				fmt.Fprintf(os.Stderr, "  sudo -iu %s csm doctor\n", getenvDefault("CS2_USER", csm.DefaultCS2User))
				fmt.Fprintln(os.Stderr, "or as root:")
				fmt.Fprintln(os.Stderr, "  sudo csm doctor")
				os.Exit(1)
			}

			dfs := flag.NewFlagSet("doctor", flag.ExitOnError)
			assumeYes := dfs.Bool("yes", false, "apply all available fixes without prompting")
			server := dfs.Int("server", 0, "server number to check (0 = all)")
			_ = dfs.Parse(args[1:])

			ctx := context.Background()
			meta, checks, scanErr := csm.DoctorScan(ctx, csm.DoctorOptions{Server: *server})
			report := csm.FormatDoctorReport(meta, checks)
			fmt.Print(report)

			var fullOut strings.Builder
			fullOut.WriteString(report)
			if scanErr != nil {
				fullOut.WriteString("\n[!] Doctor scan error: ")
				fullOut.WriteString(scanErr.Error())
				fullOut.WriteString("\n")
			}

			isInteractive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
			appliedAny := false

			for _, chk := range checks {
				if chk.Status == csm.DoctorOK || chk.Fix == nil {
					continue
				}

				if !*assumeYes {
					// Without a TTY we don't prompt; require --yes for non-interactive fixing.
					if !isInteractive {
						continue
					}
					if !promptYesNo(fmt.Sprintf("Fix now: %s? [y/N] ", chk.Title)) {
						continue
					}
				}

				fmt.Println()
				fmt.Printf("[*] Applying fix: %s\n", chk.Title)
				out, err := chk.Fix(ctx)
				if strings.TrimSpace(out) != "" {
					fmt.Print(out)
					if !strings.HasSuffix(out, "\n") {
						fmt.Println()
					}
				}

				fullOut.WriteString("\n--- fix: ")
				fullOut.WriteString(chk.ID)
				fullOut.WriteString(" ---\n")
				fullOut.WriteString(out)

				if err != nil {
					fmt.Fprintf(os.Stderr, "Fix failed: %v\n", err)
					fullOut.WriteString("\nFix failed: ")
					fullOut.WriteString(err.Error())
					fullOut.WriteString("\n")
					csm.LogAction("cli", "doctor", fullOut.String(), err)
					os.Exit(1)
				}
				appliedAny = true
			}

			if appliedAny {
				fmt.Println()
				fmt.Println("[*] Re-scanning after fixes...")
				meta2, checks2, scanErr2 := csm.DoctorScan(ctx, csm.DoctorOptions{Server: *server})
				report2 := csm.FormatDoctorReport(meta2, checks2)
				fmt.Print(report2)
				fullOut.WriteString("\n--- rescan ---\n")
				fullOut.WriteString(report2)
				if scanErr2 != nil {
					fullOut.WriteString("\nRescan error: ")
					fullOut.WriteString(scanErr2.Error())
					fullOut.WriteString("\n")
				}
			} else if !*assumeYes && !isInteractive {
				fmt.Fprintln(os.Stderr, "\n[i] Non-interactive session: no fixes applied. Re-run with `--yes` to auto-fix.")
			}

			csm.LogAction("cli", "doctor", fullOut.String(), scanErr)
			if scanErr != nil {
				fmt.Fprintf(os.Stderr, "doctor scan error: %v\n", scanErr)
				os.Exit(1)
			}
			return
		case "fix-libv8":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: csm fix-libv8 <server|0>")
				fmt.Fprintln(os.Stderr, "")
				fmt.Fprintln(os.Stderr, "Repairs missing libv8.so by validating the master install (SteamCMD app 730) and re-syncing game files to server(s).")
				fmt.Fprintln(os.Stderr, "Use 0 to repair all discovered servers.")
				os.Exit(1)
			}
			server, serr := strconv.Atoi(args[1])
			if serr != nil || server < 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q (must be 0 for all servers, or a positive integer)\n", args[1])
				os.Exit(1)
			}
			out, err := csm.FixLibV8WithContext(context.Background(), server)
			csm.LogAction("cli", fmt.Sprintf("fix-libv8 %d", server), out, err)
			if out != "" {
				fmt.Print(out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Println()
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "fix-libv8 failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "dedupe-vpk":
			opts := csm.DedupeVPKOptions{}
			for _, a := range args[1:] {
				switch a {
				case "--verify":
					opts.Verify = true
				case "--dry-run":
					opts.DryRun = true
				case "--undo":
					opts.Undo = true
				case "--allow-running":
					opts.AllowRunning = true
				case "-h", "--help":
					printDedupeVPKUsage(os.Stdout)
					return
				default:
					n, nerr := strconv.Atoi(a)
					if nerr != nil || n < 0 {
						fmt.Fprintf(os.Stderr, "unknown argument %q\n\n", a)
						printDedupeVPKUsage(os.Stderr)
						os.Exit(1)
					}
					opts.Server = n
				}
			}
			out, err := csm.DedupeVPKs(context.Background(), os.Stdout, opts)
			csm.LogAction("cli", "dedupe-vpk "+strings.Join(args[1:], " "), out, err)
			if err != nil {
				fmt.Fprintf(os.Stderr, "dedupe-vpk failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "logdir":
			// Show command logs directory and recent logs
			logDir := csm.ResolveRoot()
			if d := os.Getenv("CSM_LOG_DIR"); d != "" {
				logDir = d
			} else {
				logDir = filepath.Join(logDir, "logs")
			}

			if _, err := os.Stat(logDir); os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Logs directory does not exist yet: %s\n\nRun some commands first to generate logs.\n", logDir)
				os.Exit(1)
			}

			logs, err := csm.ListRecentLogs(10)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to list logs: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("CS2 Server Manager - Command Logs\n==================================\n\nLog directory: %s\n\n", logDir)

			if len(logs) == 0 {
				fmt.Println("No command logs found yet.\n\nRun some commands and their logs will appear here.")
			} else {
				fmt.Printf("Recent commands (10 most recent):\n\n")
				for i, logName := range logs {
					fmt.Printf("  %d. %s\n", i+1, logName)
				}
				fmt.Printf("\nTo view a log:\n  cat %s/<filename>\n\nTo navigate to logs directory:\n  cd %s\n\nTo follow live logs:\n  tail -f %s/csm.log\n", logDir, logDir, logDir)
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
			debugFS := flag.NewFlagSet("debug", flag.ExitOnError)
			var debugAlternate bool
			var debugBinary bool
			debugFS.BoolVar(&debugAlternate, "alternate", false, "use alternate launcher (game/csm.sh) instead of Valve's cs2.sh")
			debugFS.BoolVar(&debugBinary, "binary", false, "run the cs2 binary directly (not recommended; troubleshooting only)")
			_ = debugFS.Parse(args[1:])
			debugArgs := debugFS.Args()

			if len(debugArgs) < 1 {
				fmt.Fprintln(os.Stderr, "usage: csm debug [--alternate|--binary] <server>")
				os.Exit(1)
			}
			if err := csm.ApplyLaunchModeFlags(debugAlternate, debugBinary); err != nil {
				fmt.Fprintf(os.Stderr, "debug: %v\n", err)
				os.Exit(1)
			}

			server, serr := strconv.Atoi(debugArgs[0])
			if serr != nil {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", debugArgs[0])
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
		case "self-update":
			if err := tui.SelfUpdateCLI(os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "self-update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-game":
			gameForce, _ := extractForce(args[1:])
			if mgr, merr := csm.NewTmuxManager(); merr == nil {
				gateOrExit(mgr, "update-game", nil, gameForce)
			}
			// Stream the log (SteamCMD and rsync progress) as it happens; it
			// is not printed again at the end.
			live := strings.TrimSpace(os.Getenv("CSM_UPDATE_GAME_LOG")) == ""
			if live {
				csm.SetLiveOutput(os.Stdout)
			}
			out, err := csm.UpdateGame()
			csm.SetLiveOutput(nil)
			csm.LogAction("cli", "update-game", out, err)
			if !live && out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "game update failed: %v\n", err)
				os.Exit(1)
			}
			return
		case "update-server":
			serverForce, serverArgs := extractForce(args[1:])
			if len(serverArgs) < 1 {
				fmt.Fprintln(os.Stderr, "usage: csm update-server [--force] <server>")
				os.Exit(1)
			}
			sn, serr := strconv.Atoi(serverArgs[0])
			if serr != nil || sn <= 0 {
				fmt.Fprintf(os.Stderr, "invalid server number %q\n", serverArgs[0])
				os.Exit(1)
			}
			if mgr, merr := csm.NewTmuxManager(); merr == nil {
				gateOrExit(mgr, fmt.Sprintf("update-server server-%d", sn), []int{sn}, serverForce)
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
			pluginsForce, _ := extractForce(args[1:])
			if mgr, merr := csm.NewTmuxManager(); merr == nil {
				gateOrExit(mgr, "update-plugins", nil, pluginsForce)
			}
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
		case "updates":
			out, err := runUpdatesCommand(args[1:])
			// `updates platform <url> <token>` carries a secret: the action
			// log and the printed output must not keep it.
			csm.LogAction("cli", "updates "+strings.Join(redactUpdatesArgs(args[1:]), " "), out, err)
			if out != "" {
				fmt.Print(out)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "updates: %v\n", err)
				printUpdatesUsage(os.Stderr)
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

	// No subcommand matched: run the TUI. It manages tmux, game files and
	// cron jobs, so it runs as the CS2 user (user mode, after a one-time
	// `sudo csm setup-host`) or as root. Anyone else would hit permission
	// errors halfway through a flow.
	if !csm.CanManageServers() {
		cs2User := getenvDefault("CS2_USER", csm.DefaultCS2User)
		fmt.Fprintf(os.Stderr, "CSM TUI must be run as the CS2 user (%s) or as root so it can manage tmux, game files and cron jobs.\n", cs2User)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "One-time host setup (installs dependencies, creates the user, hands it csm's files):")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  sudo csm setup-host")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Then run csm as that user, no sudo:")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "  sudo -iu %s\n", cs2User)
		fmt.Fprintln(os.Stderr, "  csm")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "You can still run non-TUI commands without sudo where appropriate, e.g.:")
		fmt.Fprintln(os.Stderr, "  csm status")
		fmt.Fprintln(os.Stderr, "  csm logs <server>")
		fmt.Fprintln(os.Stderr, "  csm attach <server>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "For a full list of commands and which need root, run:")
		fmt.Fprintln(os.Stderr, "  csm -h")
		os.Exit(1)
	}

	// No subcommand matched and we are root or the CS2 user: run the TUI. If we're in daemon
	// mode or stdout is not a TTY, disable the renderer. Otherwise, use
	// full-screen TUI.
	//
	// Always enable Bubble Tea file logging so we can debug TUI behaviour even
	// when stdout is occupied. Logs go to CSM_LOG_DIR (default: current
	// directory) as csm.log, shared with other CSM log helpers.
	rootForLogs := getenvDefault("CSM_ROOT", ".")
	logDir := getenvDefault("CSM_LOG_DIR", filepath.Join(rootForLogs, "logs"))
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "failed to create CSM_LOG_DIR:", err)
	} else {
		logPath := filepath.Join(logDir, "csm.log")
		if f, err := tea.LogToFile(logPath, "debug"); err != nil {
			fmt.Fprintln(os.Stderr, "failed to enable debug logging:", err)
		} else {
			// Bubble Tea log files default to 0600; make them readable so users can
			// quickly inspect why the TUI exited (common during setup).
			_ = os.Chmod(logPath, 0o644)
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
	fmt.Println("  --copy-mode  master→server replication mode (auto|reflink|rsync|legacy)")
	fmt.Println()
	fmt.Printf("%sUser mode:%s after a one-time `sudo csm setup-host`, run csm as the CS2 user\n", cyan, reset)
	fmt.Printf("(%s by default) without sudo. Running everything as root still works.\n", csm.DefaultCS2User)
	fmt.Println()
	fmt.Printf("%sCommands (CS2 user or root):%s\n", cyan, reset)
	fmt.Println("  public-ip              Print public IP address")
	fmt.Println("  status                 Fleet table: process, map, phase, score, players, Ready Up (--watch, --json)")
	fmt.Println("  start|stop|restart     Control servers via tmux")
	fmt.Println("  logs                   Tail server logs (scrolling)")
	fmt.Println("  logs-file              Show the raw log file path for a server")
	fmt.Println("  fix-libv8              Validate+sync to fix missing libv8.so")
	fmt.Println("  attach                 Attach to a server tmux session")
	fmt.Println("  list-sessions          List tmux sessions")
	fmt.Println("  debug                  Run a server in foreground debug mode")
	fmt.Println("  extract-map-data       Extract map thumbnails (PNG + WEBP) and maps.json (map list + Active Duty) into map_thumbnails/")
	fmt.Println("                         --publish --repo <dir>: commit them on a new branch in a cs2-server-manager checkout")
	fmt.Println("  list-bans <server>     List banned IP addresses for a server")
	fmt.Println()
	fmt.Println("Start options:")
	fmt.Println("  csm start [--alternate|--binary] [server]")
	fmt.Println("  csm restart [--alternate|--binary] [server]")
	fmt.Println("  csm debug [--alternate|--binary] <server>")
	fmt.Println()
	fmt.Println("Live matches:")
	fmt.Println("  stop, restart, update-game, update-server and update-plugins refuse to touch a")
	fmt.Println("  server whose Ready Up reports update_safe=false (a match is live). Add --force")
	fmt.Println("  to go ahead anyway; forced runs are logged. Servers without Ready Up are not held.")
	fmt.Println()
	fmt.Printf("%sMore commands (CS2 user or root):%s\n", cyan, reset)
	fmt.Println("  bootstrap              Install/redeploy servers (non-interactive; the Docker MySQL step needs root)")
	fmt.Println("  doctor                 Diagnose and offer fixes for common issues")
	fmt.Println("  reinstall <server>     Rebuild a single server from master (fixes corrupted files)")
	fmt.Println("  update-config <server> Regenerate server configs without reinstalling")
	fmt.Println("  unban <server> <ip>    Remove IP from banned RCON requests (use 0 for all servers)")
	fmt.Println("  unban-all <server>     Clear all IPs banned for RCON attempts (use 0 for all servers)")
	fmt.Println("  update-game            Update CS2 game files after a Valve update")
	fmt.Println("  update-plugins         Update plugins and deploy to servers")
	fmt.Println("  self-update            Update csm itself to the latest release")
	fmt.Println("  dedupe-vpk [server]    Hardlink server VPKs to master-install to save disk (--dry-run, --verify, --undo)")
	fmt.Println("  monitor                Update servers with a pending CS2 update once idle (cron runs this)")
	fmt.Println("  updates hold on|off|auto  Pause/resume automatic updates; auto lets Auto Tournament decide")
	fmt.Println("  updates status         Show hold state, platform and idle grace period")
	fmt.Println("  updates grace <min>    Minutes a server must be idle before it is auto-updated")
	fmt.Println("  updates platform       Point csm at an Auto Tournament instance (<url> <token>, or off)")
	fmt.Println("  updates check          Ask the platform now whether updates are held")
	fmt.Println("  install-monitor-cron   Install auto-update monitor cronjob (in the crontab of the user running it)")
	fmt.Println("  remove-monitor-cron    Remove auto-update monitor cronjob")
	fmt.Println("  ci setup|status|update|remove  CI test host for Ready Up's live-server check: a separate CS2")
	fmt.Println("                         install (not a numbered server) + a GitHub Actions runner (label")
	fmt.Println("                         readyup-live) as a systemd --user service. CS2 user only. `csm ci -h`")
	fmt.Println()
	fmt.Printf("%sCommands that need root (sudo):%s\n", yellow, reset)
	fmt.Println("  setup-host             One-time host setup for user mode: deps, CS2 user, linger, file ownership,")
	fmt.Println("                         monitor cron in the user's crontab (--skip-deps, --skip-linger). Never touches servers")
	fmt.Println("  install-deps           Install system dependencies")
	fmt.Println("  cleanup-all            Remove all servers and related resources")
	fmt.Println()
	fmt.Println("If no command is given, the interactive TUI is started.")
}

// redactUpdatesArgs hides the platform token before the command line is
// written to the action log.
func redactUpdatesArgs(args []string) []string {
	out := append([]string(nil), args...)
	if len(out) >= 3 && strings.EqualFold(out[0], "platform") {
		out[2] = "***"
	}
	return out
}

// runUpdatesCommand handles `csm updates hold on|off|auto`, `csm updates
// status`, `csm updates grace <minutes>`, `csm updates platform ...` and
// `csm updates check`.
func runUpdatesCommand(args []string) (string, error) {
	status := func(st csm.AutoUpdateSettings) string {
		var out strings.Builder
		mode := st.Mode()
		switch mode {
		case csm.HoldModeOn:
			out.WriteString("Automatic updates hold: ON (manual; the monitor only reports available updates)\n")
		case csm.HoldModeOff:
			out.WriteString("Automatic updates hold: off (manual; the platform is not consulted)\n")
		default:
			out.WriteString("Automatic updates hold: auto (Auto Tournament decides)\n")
		}
		if st.HoldChangedAt != "" {
			fmt.Fprintf(&out, "Hold last changed:      %s\n", st.HoldChangedAt)
		}
		platform := st.Platform.Resolved()
		if platform.Configured() {
			fmt.Fprintf(&out, "Platform:               %s\n", platform.BaseURL)
			if csm.PlainHTTPToRemoteHost(platform.BaseURL) {
				out.WriteString("                        warning: plain http to a remote host sends the token unencrypted\n")
			}
		} else {
			out.WriteString("Platform:               not configured (csm updates platform <url> <token>)\n")
		}
		fmt.Fprintf(&out, "Idle grace period:      %s\n", st.IdleGrace())
		if mode == csm.HoldModeAuto && platform.Configured() {
			out.WriteString("\nRun `csm updates check` to ask the platform now.\n")
		}
		return out.String()
	}
	if len(args) == 0 || args[0] == "status" {
		st, err := csm.LoadAutoUpdateSettings()
		if err != nil {
			return "", err
		}
		return status(st), nil
	}
	switch args[0] {
	case "hold":
		if len(args) != 2 {
			return "", fmt.Errorf("usage: csm updates hold on|off|auto")
		}
		mode, err := csm.ParseHoldMode(args[1])
		if err != nil {
			return "", err
		}
		st, err := csm.SetUpdateHoldMode(mode)
		if err != nil {
			return "", err
		}
		return status(st), nil
	case "grace":
		if len(args) != 2 {
			return "", fmt.Errorf("usage: csm updates grace <minutes>")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("grace takes a whole number of minutes, not %q", args[1])
		}
		st, err := csm.SetIdleGraceMinutes(n)
		if err != nil {
			return "", err
		}
		return status(st), nil
	case "platform":
		switch len(args) {
		case 2:
			if !strings.EqualFold(args[1], "off") && !strings.EqualFold(args[1], "none") {
				return "", fmt.Errorf("usage: csm updates platform <url> <token> | off")
			}
			st, err := csm.SetPlatform("", "")
			if err != nil {
				return "", err
			}
			return "Platform cleared; the automatic hold is off until one is set again.\n\n" + status(st), nil
		case 3:
			st, err := csm.SetPlatform(args[1], args[2])
			if err != nil {
				return "", err
			}
			return status(st), nil
		}
		return "", fmt.Errorf("usage: csm updates platform <url> <token> | off")
	case "check":
		st, err := csm.LoadAutoUpdateSettings()
		if err != nil {
			return "", err
		}
		platform := st.Platform.Resolved()
		if !platform.Configured() {
			return "", fmt.Errorf("no platform is configured (csm updates platform <url> <token>)")
		}
		answer, err := csm.FetchPlatformHold(context.Background(), platform)
		if err != nil {
			// A failed check is exactly what the monitor would see, so say
			// what it would then do.
			return "", fmt.Errorf("%w\n\nThe monitor holds updates while this fails", err)
		}
		hold := "off"
		if answer.Hold {
			hold = "ON"
		}
		out := fmt.Sprintf("Platform:  %s\nHold:      %s\nReason:    %s\n", platform.BaseURL, hold, answer.Reason)
		if answer.TournamentStatus != "" {
			out += fmt.Sprintf("Tournament: %s\n", answer.TournamentStatus)
		}
		if st.Mode() != csm.HoldModeAuto {
			out += fmt.Sprintf("\nNote: the hold is set to %q, so this answer is not used.\n", st.Mode())
		}
		return out, nil
	}
	return "", fmt.Errorf("unknown subcommand %q", args[0])
}

func printUpdatesUsage(w *os.File) {
	fmt.Fprintln(w, "usage: csm updates hold on|off|auto | status | grace <minutes> | platform <url> <token> | check")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  hold auto     ask Auto Tournament (the default); it holds while a tournament runs")
	fmt.Fprintln(w, "  hold on       the monitor never restarts servers; it only logs that an update is available")
	fmt.Fprintln(w, "  hold off      the monitor updates servers once idle, without asking the platform")
	fmt.Fprintln(w, "  status        show the current settings")
	fmt.Fprintln(w, "  grace <min>   how long a server must stay idle before it is updated (default 10)")
	fmt.Fprintln(w, "  platform      point csm at an Auto Tournament instance; `platform off` clears it")
	fmt.Fprintln(w, "                the token is the platform's SERVER_TOKEN; CSM_PLATFORM_URL and")
	fmt.Fprintln(w, "                CSM_PLATFORM_TOKEN override the stored values")
	fmt.Fprintln(w, "  check         ask the platform now and print its answer")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "In `auto`, a platform that cannot be reached holds updates: csm does not restart")
	fmt.Fprintln(w, "a server while it cannot tell whether a tournament is running.")
	fmt.Fprintln(w, "`csm update-game` and `csm update-server` always run, hold or not.")
}

func printDedupeVPKUsage(w *os.File) {
	fmt.Fprintln(w, "usage: csm dedupe-vpk [--dry-run] [--verify] [--undo] [--allow-running] [server]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Replaces each server's *.vpk files with hardlinks to the identical files in")
	fmt.Fprintln(w, "master-install (same size and mtime). Everything else stays a per-server copy.")
	fmt.Fprintln(w, "Safe to run twice; prints disk usage before and after.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "  server           only process server-N (default: all servers)")
	fmt.Fprintln(w, "  --dry-run        report what would change and the estimated savings")
	fmt.Fprintln(w, "  --verify         also byte-compare each file before linking (slow)")
	fmt.Fprintln(w, "  --undo           turn hardlinked VPKs back into independent copies")
	fmt.Fprintln(w, "  --allow-running  do not refuse when target servers are running")
}

func promptYesNo(question string) bool {
	fmt.Fprint(os.Stdout, question)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

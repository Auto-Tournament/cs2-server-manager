package csm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fixServerOwnership ensures all files in the cs2servermanager home directory
// are owned by the correct user. This is called at the end of all major
// operations (bootstrap, reinstall, add server, update plugins) to prevent
// ownership issues that can break server functionality.
func fixServerOwnership(user string) error {
	homeDir := filepath.Join("/home", user)
	
	// Check if home directory exists
	if _, err := os.Stat(homeDir); os.IsNotExist(err) {
		// Nothing to fix if home doesn't exist yet
		return nil
	}
	
	// Run chown recursively on the entire home directory
	cmd := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", user, user), homeDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to fix ownership of %s: %w", homeDir, err)
	}
	
	return nil
}

// BootstrapConfig mirrors the high-level options used by the original
// bootstrap_cs2.sh script.
type BootstrapConfig struct {
	CS2User           string
	NumServers        int
	BaseGamePort      int
	BaseTVPort        int
	HostnamePrefix    string
	EnableMetamod     bool
	FreshInstall      bool
	UpdateMaster      bool
	RCONPassword      string
	MaxPlayers        int // 0 means use default
	GSLT              string // Game Server Login Token (optional)
	MatchzySkipDocker bool
	GameFilesDir      string // typically <root>/game_files
	OverridesDir      string // typically <root>/overrides

	// Optional MatchZy DB wiring from the install wizard. When DBMode is set
	// to "docker" or "external", setupMatchZyDatabaseGo will treat
	// database.json as wizard-managed and overwrite it using these values
	// before proceeding. When DBMode is empty, the legacy behaviour of reading
	// overrides/database.json as-is is preserved for CLI and VerifyMatchzyDB.
	DBMode             string
	ExternalDBHost     string
	ExternalDBPort     int
	ExternalDBName     string
	ExternalDBUser     string
	ExternalDBPassword string
}

// Bootstrap installs or redeploys the CS2 servers, performing roughly the
// same steps as scripts/bootstrap_cs2.sh. It returns a human-readable log.
func Bootstrap(cfg BootstrapConfig) (string, error) {
	return BootstrapWithContext(context.Background(), cfg)
}

// BootstrapWithContext is like Bootstrap but allows callers to provide a
// context that can be cancelled to terminate long-running operations such as
// steamcmd. The plain Bootstrap function uses a background context.
func BootstrapWithContext(ctx context.Context, cfg BootstrapConfig) (string, error) {
	var buf bytes.Buffer
	var logFile *os.File

	// When invoked from the TUI install wizard, CSM_BOOTSTRAP_LOG is set to a
	// temp path that the UI tails in real time. Mirror all bootstrap log lines
	// into that file so the user can see progress across the entire step, not
	// just steamcmd output.
	if logPath := strings.TrimSpace(os.Getenv("CSM_BOOTSTRAP_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			defer func() {
				if err := logFile.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "CSM_BOOTSTRAP_LOG close failed: %v\n", err)
				}
			}()
		}
	}

	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
		if logFile != nil {
			fmt.Fprintf(logFile, format, args...)
			if !strings.HasSuffix(format, "\n") {
				_, _ = logFile.Write([]byte{'\n'})
			}
		}
	}

	if os.Geteuid() != 0 {
		return "", fmt.Errorf("bootstrap must be run as root (use sudo)")
	}

	if cfg.CS2User == "" {
		cfg.CS2User = DefaultCS2User
	}
	if cfg.NumServers <= 0 {
		cfg.NumServers = DefaultNumServers
	}
	if cfg.BaseGamePort == 0 {
		cfg.BaseGamePort = DefaultBaseGamePort
	}
	if cfg.BaseTVPort == 0 {
		cfg.BaseTVPort = DefaultBaseTVPort
	}
	if strings.TrimSpace(cfg.HostnamePrefix) == "" {
		cfg.HostnamePrefix = "CS2 Server"
	}
	if cfg.RCONPassword == "" {
		// Use a neutral fallback rather than an event-specific password; the
		// install wizard will normally require users to set this explicitly.
		log("  [!] No RCON password supplied; using default %q. You should change this after install.", DefaultRCONPassword)
		cfg.RCONPassword = DefaultRCONPassword
	}

	// Determine the project root for game_files/ and overrides/.
	// Priority:
	//   1) Explicit cfg.GameFilesDir / cfg.OverridesDir (from CLI env or caller)
	//   2) CSM_ROOT environment variable, if set
	//   3) Directory of the CSM executable
	//   4) Current working directory
	root := ""
	if v, ok := os.LookupEnv("CSM_ROOT"); ok && v != "" {
		root = v
	} else if exe, err := os.Executable(); err == nil && exe != "" {
		if dir := filepath.Dir(exe); dir != "" {
			root = dir
		}
	}
	if root == "" {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			root = wd
		} else {
			root = "."
		}
	}
	if cfg.GameFilesDir == "" {
		cfg.GameFilesDir = filepath.Join(root, "game_files")
	}
	if cfg.OverridesDir == "" {
		cfg.OverridesDir = filepath.Join(root, "overrides")
	}

	// If no overrides directory exists yet, seed it with the built-in defaults.
	var createdOverrideFiles []string
	if err := ensureDefaultOverridesWithTracking(cfg.OverridesDir, &createdOverrideFiles); err != nil {
		log("  [!] Failed to write default overrides to %s: %v", cfg.OverridesDir, err)
	}
	
	// Track if bootstrap succeeded - we'll use this in defer to decide cleanup
	bootstrapSucceeded := false
	defer func() {
		// Clean up overrides directory if bootstrap failed or was cancelled
		if !bootstrapSucceeded || ctx.Err() != nil {
			if len(createdOverrideFiles) > 0 {
				log("  [*] Cleaning up overrides directory due to failed/cancelled install...")
				if err := os.RemoveAll(cfg.OverridesDir); err != nil {
					log("  [!] Failed to clean up overrides directory: %v", err)
				} else {
					log("  [✓] Overrides directory cleaned up")
				}
			} else if ctx.Err() != nil {
				// Even if no files were created, if cancelled, clean up the directory if it didn't exist before
				// (This handles the case where ensureDefaultOverrides created the directory structure)
				if _, err := os.Stat(cfg.OverridesDir); err == nil {
					// Check if directory is empty or only contains wizard-managed files
					entries, _ := os.ReadDir(cfg.OverridesDir)
					if len(entries) == 0 {
						_ = os.RemoveAll(cfg.OverridesDir)
					}
				}
			}
		}
	}()

	log("[*] Setting up %d CS2 servers...", cfg.NumServers)
	log("")

	log("[1/5] Creating CS2 user...")
	if err := createCS2User(&buf, cfg.CS2User); err != nil {
		log("  [!] Failed to create CS2 user: %v", err)
		return buf.String(), err
	}
	log("")

	log("[2/5] Installing/updating master CS2 installation...")
	if err := installMasterViaSteamCMD(ctx, &buf, cfg); err != nil {
		log("  [!] Failed to install/update master: %v", err)
		log("  [*] Check /tmp/csm-bootstrap.log for detailed steamcmd output")
		return buf.String(), err
	}
	log("")

	log("[3/5] Setting up Steam SDK symlinks...")
	if err := setupSteamSDKLinksGo(&buf, cfg.CS2User); err != nil {
		log("  [!] Failed to set up Steam SDK links: %v", err)
		// Non-fatal; continue.
	}
	log("")

	log("[4/5] Provisioning MatchZy database (Docker)...")
	
	if err := setupMatchZyDatabaseGo(&buf, cfg); err != nil {
		log("  [!] MatchZy database provisioning skipped or failed: %v", err)
		log("      Install Docker and rerun bootstrap if you need the built-in database.")
	}
	log("")

	log("[5/5] Setting up shared configuration...")
	if err := setupSharedConfigGo(&buf, cfg); err != nil {
		log("  [!] Failed to set up shared config: %v", err)
		return buf.String(), err
	}
	log("")

	log("[*] Creating %d server instances...", cfg.NumServers)
	log("")

	// When running a fresh install, proactively delete all existing server-*
	// directories for this CS2 user so we don't leave old servers around
	// (for example, when shrinking from 4 servers down to 2). This happens
	// before we recreate the configured set below.
	if cfg.FreshInstall {
		homeDir := filepath.Join("/home", cfg.CS2User)
		entries, err := os.ReadDir(homeDir)
		if err == nil {
			for _, e := range entries {
				name := e.Name()
				if !e.IsDir() || !strings.HasPrefix(name, "server-") {
					continue
				}
				full := filepath.Join(homeDir, name)
				log("  [*] FRESH_INSTALL=1: Deleting existing %s", full)
				if err := os.RemoveAll(full); err != nil {
					log("  [!] Failed to delete %s: %v", full, err)
				}
			}
			log("")
		}
	}

	for i := 1; i <= cfg.NumServers; i++ {
		gamePort := cfg.BaseGamePort + (i-1)*10
		tvPort := cfg.BaseTVPort + (i-1)*10

		log("[%d/%d] Setting up server-%d...", i, cfg.NumServers, i)

		if err := stopTmuxServerGo(&buf, cfg.CS2User, i); err != nil {
			log("  [i] Could not stop tmux session for server-%d: %v", i, err)
		}

		if err := copyMasterToServerGo(ctx, &buf, cfg.CS2User, i, cfg.FreshInstall); err != nil {
			log("  [!] Copy master to server-%d failed: %v", i, err)
		}

		if err := overlayConfigToServerGo(ctx, &buf, cfg.CS2User, i); err != nil {
			log("  [!] Overlay config to server-%d failed: %v", i, err)
		}

		if err := configureMetamodGo(&buf, cfg.CS2User, i, cfg.EnableMetamod); err != nil {
			log("  [!] Configure Metamod for server-%d failed: %v", i, err)
		}

		if err := customizeServerCfgGo(&buf, cfg.CS2User, i, cfg.RCONPassword, cfg.HostnamePrefix, gamePort, tvPort, cfg.MaxPlayers); err != nil {
			log("  [!] Customize server.cfg for server-%d failed: %v", i, err)
		}

		// Store GSLT token if provided
		if cfg.GSLT != "" {
			if err := storeGSLTGo(&buf, cfg.CS2User, i, cfg.GSLT); err != nil {
				log("  [!] Failed to store GSLT for server-%d: %v", i, err)
			}
		}

		log("  [✓] Server-%d ready (port %d, TV %d)", i, gamePort, tvPort)
		log("")
	}

	log("=== Setup Complete ===")
	log("User              : %s", cfg.CS2User)
	log("Master install    : /home/%s/master-install", cfg.CS2User)
	log("Shared config     : /home/%s/cs2-config/game", cfg.CS2User)
	log("Server instances  : /home/%s/server-1 through server-%d", cfg.CS2User, cfg.NumServers)
	log("")
	log("RCON password : %s (override with RCON_PASSWORD=xxxxx)", cfg.RCONPassword)
	log("Plugin source : %s/game -> /home/%s/cs2-config/game", cfg.GameFilesDir, cfg.CS2User)
	log("Custom configs: %s/game -> /home/%s/cs2-config/game", cfg.OverridesDir, cfg.CS2User)
	log("Metamod       : %v", cfg.EnableMetamod)

	// Fix ownership of all server files to ensure everything is owned by cs2servermanager
	log("")
	log("[*] Fixing file ownership...")
	if err := fixServerOwnership(cfg.CS2User); err != nil {
		log("[!] Warning: Failed to fix ownership: %v", err)
		log("[!] You may need to manually run: sudo chown -R %s:%s /home/%s", cfg.CS2User, cfg.CS2User, cfg.CS2User)
	} else {
		log("[✓] File ownership fixed")
	}

	bootstrapSucceeded = true
	return buf.String(), nil
}

// --- core helpers ---

func createCS2User(w *bytes.Buffer, user string) error {
	// Check if user exists
	if err := exec.Command("id", "-u", user).Run(); err == nil {
		fmt.Fprintln(w, "  [i] User", user, "already exists")
	} else {
		// Check if group exists
		if err := exec.Command("getent", "group", user).Run(); err == nil {
			fmt.Fprintf(w, "  [*] Creating user %s with existing group %s\n", user, user)
			if err := exec.Command("useradd", "-m", "-s", "/bin/bash", "-g", user, user).Run(); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(w, "  [*] Creating user %s and matching group\n", user)
			if err := exec.Command("useradd", "-m", "-s", "/bin/bash", "-U", user).Run(); err != nil {
				return err
			}
		}
		_ = exec.Command("loginctl", "enable-linger", user).Run()
		fmt.Fprintf(w, "  [✓] User %s created\n", user)
	}

	home := filepath.Join("/home", user)
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	// Ensure /usr/games is in PATH for steamcmd
	bashrc := filepath.Join(home, ".bashrc")
	data, _ := os.ReadFile(bashrc)
	if !bytes.Contains(data, []byte("/usr/games")) {
		f, err := os.OpenFile(bashrc, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(`export PATH="/usr/games:$PATH"` + "\n")
			_ = f.Close()
		}
	}
	return nil
}

func installMasterViaSteamCMD(ctx context.Context, w *bytes.Buffer, cfg BootstrapConfig) error {
	homeDir := filepath.Join("/home", cfg.CS2User)
	masterDir := filepath.Join(homeDir, "master-install")
	gameinfo := filepath.Join(masterDir, "game", "csgo", "gameinfo.gi")

	if cfg.FreshInstall {
		if _, err := os.Stat(masterDir); err == nil {
			fmt.Fprintln(w, "  [*] FRESH_INSTALL=1: Deleting existing master install")
			if err := os.RemoveAll(masterDir); err != nil {
				// On some filesystems RemoveAll can fail with "directory not
				// empty" for deep trees. Fall back to a best-effort rm -rf
				// using the system tools so we don't leave a half-deleted
				// master install behind.
				fmt.Fprintf(w, "  [i] os.RemoveAll failed (%v), retrying with rm -rf\n", err)
				if err2 := runCmdLogged(w, "rm", "-rf", masterDir); err2 != nil {
					return fmt.Errorf("failed to delete master install %s: %w (rm -rf fallback also failed: %v)", masterDir, err, err2)
				}
			}
		}
	}

	if _, err := os.Stat(gameinfo); err == nil && !cfg.UpdateMaster {
		fmt.Fprintln(w, "  [i] Master install exists and UPDATE_MASTER=0, skipping")
		return nil
	}

	if _, err := os.Stat(gameinfo); err == nil && cfg.UpdateMaster {
		fmt.Fprintln(w, "  [*] Updating existing master install")
	} else {
		fmt.Fprintf(w, "  [*] Installing fresh CS2 master to %s\n", masterDir)
	}

	// Ensure the CS2 user's home directory and master install exist and are
	// owned by the CS2 user, mirroring the original bootstrap script. This
	// prevents permission issues when steamcmd tries to create ~/.steam or
	// write into the master install directory.
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return err
	}
	_ = exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cfg.CS2User, cfg.CS2User), homeDir).Run()

	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return err
	}
	_ = exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cfg.CS2User, cfg.CS2User), masterDir).Run()

	// Ensure dependencies (apt-get, steamcmd) - Debian/Ubuntu only for now.
	if err := ensureBootstrapDependencies(w); err != nil {
		return err
	}

	// Pre-create ~/.steam for the CS2 user and ensure it is owned correctly.
	steamDir := filepath.Join(homeDir, ".steam")
	if err := os.MkdirAll(steamDir, 0o755); err != nil {
		return err
	}
	_ = exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cfg.CS2User, cfg.CS2User), steamDir).Run()

	// Run steamcmd as CS2 user
	// Use a trap to ensure steamcmd is killed when the script receives SIGTERM/SIGKILL
	// This is necessary because su->shell->steamcmd chain doesn't automatically kill children
	// Don't use set -e since wait returns the steamcmd exit code and we want to handle it explicitly
	script := fmt.Sprintf(`
trap 'if [ -n "$STEAMCMD_PID" ]; then kill -TERM "$STEAMCMD_PID" 2>/dev/null; wait "$STEAMCMD_PID" 2>/dev/null; fi; exit' TERM INT EXIT
steamcmd +force_install_dir "%s" +login anonymous +app_update 730 validate +quit &
STEAMCMD_PID=$!
wait $STEAMCMD_PID
exit $?
`, masterDir)

	cmd := exec.CommandContext(ctx, "su", "-", cfg.CS2User, "-c", script)
	
	// Set process group so we can kill all child processes (steamcmd) when context is cancelled.
	// When su spawns a shell which spawns steamcmd, killing just su doesn't kill the children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// If CSM_BOOTSTRAP_LOG is set, stream steamcmd output into that file so
	// the TUI can show a live tail while the install is running.
	if logPath, ok := os.LookupEnv("CSM_BOOTSTRAP_LOG"); ok && logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer func() {
				if cerr := f.Close(); cerr != nil {
					fmt.Fprintf(os.Stderr, "CSM_BOOTSTRAP_LOG close failed: %v\n", cerr)
				}
			}()
			tw := &teeWriter{buf: w, file: f}
			cmd.Stdout = tw
			cmd.Stderr = tw
			if err := cmd.Start(); err != nil {
				return fmt.Errorf("steamcmd start failed: %w", err)
			}
			// When context is cancelled, kill all steamcmd processes related to master-install
			// This is more reliable than trying to manage process groups with su->shell->steamcmd chain
			go func() {
				<-ctx.Done()
				// Kill the main process first
				if cmd.Process != nil && cmd.Process.Pid > 0 {
					_ = cmd.Process.Kill()
				}
				// Then kill any remaining steamcmd processes for master-install
				// Bootstrap runs as root (checked at start), so we can kill processes directly
				// Use pkill as root to find and kill all steamcmd processes
				var pkillCmd *exec.Cmd
				if os.Geteuid() == 0 {
					// Already running as root, don't need sudo
					pkillCmd = exec.Command("pkill", "-9", "-f", "steamcmd.*master-install")
				} else {
					// Fallback to sudo (may prompt for password)
					pkillCmd = exec.Command("sudo", "pkill", "-9", "-f", "steamcmd.*master-install")
				}
				pkillCmd.Run() // Best effort, ignore errors
			}()
			if err := cmd.Wait(); err != nil {
				// Log the error details
				fmt.Fprintf(w, "  [!] SteamCMD process exited with error: %v\n", err)
				// Even if steamcmd exits with an error, check if the install actually succeeded
				// by looking for gameinfo.gi - sometimes steamcmd reports errors but still completes
				if _, statErr := os.Stat(gameinfo); statErr == nil {
					fmt.Fprintln(w, "  [i] SteamCMD reported an error, but game files appear to be installed")
					fmt.Fprintln(w, "  [i] Continuing with installation...")
					// Continue - the install likely succeeded despite the error
				} else {
					fmt.Fprintf(w, "  [!] SteamCMD failed and gameinfo.gi not found at %s\n", gameinfo)
					fmt.Fprintln(w, "  [*] Check /tmp/csm-bootstrap.log for detailed steamcmd output")
					return fmt.Errorf("steamcmd failed: %w (check /tmp/csm-bootstrap.log for details)", err)
				}
			}
		} else {
			// Fall back to non-streaming mode if we can't open the log file.
			// Note: CombinedOutput() doesn't allow us to kill process groups during execution,
			// but this is a fallback path and cancellation is less critical here.
			out, err := cmd.CombinedOutput()
			if len(out) > 0 {
				fmt.Fprintln(w, string(out))
			}
			if err != nil {
				fmt.Fprintf(w, "  [!] SteamCMD process exited with error: %v\n", err)
				// Even if steamcmd exits with an error, check if the install actually succeeded
				if _, statErr := os.Stat(gameinfo); statErr == nil {
					fmt.Fprintln(w, "  [i] SteamCMD reported an error, but game files appear to be installed")
					fmt.Fprintln(w, "  [i] Continuing with installation...")
				} else {
					fmt.Fprintf(w, "  [!] SteamCMD failed and gameinfo.gi not found at %s\n", gameinfo)
					return fmt.Errorf("steamcmd failed: %w (check logs for details)", err)
				}
			}
		}
	} else {
		// No streaming requested; just run and capture the full output.
		// Note: CombinedOutput() doesn't allow us to kill process groups during execution,
		// but this path is rarely used in the TUI (which prefers streaming).
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			fmt.Fprintln(w, string(out))
		}
		if err != nil {
			fmt.Fprintf(w, "  [!] SteamCMD process exited with error: %v\n", err)
			// Even if steamcmd exits with an error, check if the install actually succeeded
			if _, statErr := os.Stat(gameinfo); statErr == nil {
				fmt.Fprintln(w, "  [i] SteamCMD reported an error, but game files appear to be installed")
				fmt.Fprintln(w, "  [i] Continuing with installation...")
			} else {
				fmt.Fprintf(w, "  [!] SteamCMD failed and gameinfo.gi not found at %s\n", gameinfo)
				return fmt.Errorf("steamcmd failed: %w (check logs for details)", err)
			}
		}
	}

	if _, err := os.Stat(gameinfo); err == nil {
		fmt.Fprintln(w, "  [✓] Master install complete/updated")
		return nil
	}
	fmt.Fprintln(w, "  [!] Master install failed - gameinfo.gi not found")
	return fmt.Errorf("gameinfo.gi not found after steamcmd")
}

func ensureBootstrapDependencies(w io.Writer) error {
	return ensureBootstrapDependenciesContext(context.Background(), w)
}

// ensureBootstrapDependenciesContext is like ensureBootstrapDependencies but
// accepts a context so long-running apt-get operations can be cancelled by
// callers such as the TUI.
func ensureBootstrapDependenciesContext(ctx context.Context, w io.Writer) error {
	if _, err := exec.LookPath("apt-get"); err != nil {
		fmt.Fprintln(w, "Only Debian/Ubuntu-style distributions are supported for automated dependencies.")
		return fmt.Errorf("apt-get not found")
	}

	// Enable i386 architecture and install dependencies with full output so
	// users can see exactly what apt is doing in the logs.
	fmt.Fprintln(w, "[deps] Enabling i386 architecture: dpkg --add-architecture i386")
	_ = exec.Command("dpkg", "--add-architecture", "i386").Run()

	fmt.Fprintln(w, "[deps] Running: apt-get update")
	if err := runCmdLoggedContext(ctx, w, "apt-get", "update"); err != nil {
		return err
	}

	pkgs := []string{
		"curl", "wget", "file", "tar", "bzip2", "xz-utils", "unzip",
		"ca-certificates", "lib32gcc-s1", "lib32stdc++6", "libc6-i386",
		"net-tools", "tmux", "steamcmd", "rsync", "jq",
	}
	args := append([]string{"install", "-y"}, pkgs...)

	fmt.Fprintf(w, "[deps] Running: apt-get %s\n", strings.Join(args, " "))
	if err := runCmdLoggedContext(ctx, w, "apt-get", args...); err != nil {
		return err
	}

	// Ensure /usr/bin/steamcmd wrapper exists (linking to /usr/games/steamcmd).
	const wrapper = "/usr/bin/steamcmd"
	if fi, err := os.Stat(wrapper); err != nil || fi.Mode()&0o111 == 0 {
		_ = os.Remove(wrapper)
		content := "#!/usr/bin/env bash\nexec /usr/games/steamcmd \"$@\"\n"
		if err := os.WriteFile(wrapper, []byte(content), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func setupSteamSDKLinksGo(w *bytes.Buffer, user string) error {
	steamDir := filepath.Join("/home", user, ".steam")
	sdk64Dir := filepath.Join(steamDir, "sdk64")
	steamClientSrc := filepath.Join("/home", user, ".local", "share", "Steam", "steamcmd", "linux64", "steamclient.so")

	fmt.Fprintf(w, "  [*] Setting up Steam SDK symlinks for %s\n", user)

	if err := os.MkdirAll(sdk64Dir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(steamClientSrc); err == nil {
		dst := filepath.Join(sdk64Dir, "steamclient.so")
		_ = os.Remove(dst)
		if err := os.Symlink(steamClientSrc, dst); err != nil {
			return err
		}
		fmt.Fprintf(w, "  [✓] Steam SDK symlink created: %s -> %s\n", dst, steamClientSrc)
	} else {
		fmt.Fprintf(w, "  [!] steamclient.so not found at %s (will appear after first SteamCMD run)\n", steamClientSrc)
	}
	return nil
}

func setupSharedConfigGo(w *bytes.Buffer, cfg BootstrapConfig) error {
	configDir := filepath.Join("/home", cfg.CS2User, "cs2-config")
	fmt.Fprintf(w, "  [*] Setting up shared config directory at %s\n", configDir)
	if err := os.MkdirAll(filepath.Join(configDir, "game"), 0o755); err != nil {
		return err
	}

	// Always write shared server config (RCON password, maxplayers) regardless of UpdateMaster/updatePlugins settings
	if err := writeSharedServerConfig(cfg.CS2User, cfg.RCONPassword, cfg.MaxPlayers); err != nil {
		return fmt.Errorf("failed to write shared server config: %w", err)
	}

	// Copy plugin files from game_files/game
	srcGame := filepath.Join(cfg.GameFilesDir, "game")
	if fi, err := os.Stat(srcGame); err == nil && fi.IsDir() {
		fmt.Fprintln(w, "  [*] Copying plugin files from game_files/")
		if err := runCmdLogged(w, "rsync",
			"-a", "--delete",
			"--exclude", ".git/",
			srcGame+string(os.PathSeparator),
			filepath.Join(configDir, "game")+"/",
		); err != nil {
			return fmt.Errorf("rsync game_files -> cs2-config failed: %w", err)
		}
	} else {
		fmt.Fprintf(w, "  [!] %s not found - run plugin updater first\n", srcGame)
	}

	// Overlay custom configs from overrides/game
	srcOv := filepath.Join(cfg.OverridesDir, "game")
	if fi, err := os.Stat(srcOv); err == nil && fi.IsDir() {
		fmt.Fprintln(w, "  [*] Applying custom overrides from overrides/")
		if err := runCmdLogged(w, "rsync",
			"-a",
			"--exclude", ".git/",
			srcOv+string(os.PathSeparator),
			filepath.Join(configDir, "game")+"/",
		); err != nil {
			return fmt.Errorf("rsync overrides -> cs2-config failed: %w", err)
		}
	} else {
		fmt.Fprintf(w, "  [i] No overrides found at %s\n", srcOv)
	}

	fmt.Fprintf(w, "  [✓] Shared config ready at %s\n", filepath.Join(configDir, "game"))
	return nil
}

func copyMasterToServerGo(ctx context.Context, w io.Writer, user string, serverNum int, fresh bool) error {
	masterDir := filepath.Join("/home", user, "master-install")
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum))

	if fresh {
		if _, err := os.Stat(serverDir); err == nil {
			fmt.Fprintf(w, "  [*] FRESH_INSTALL=1: Deleting existing server-%d\n", serverNum)
			if err := os.RemoveAll(serverDir); err != nil {
				return err
			}
		}
	}

	if fi, err := os.Stat(serverDir); err == nil && fi.IsDir() {
		fmt.Fprintf(w, "  [i] Server %d already exists, skipping copy\n", serverNum)
		return nil
	}

	fmt.Fprintf(w, "  [*] Copying master to server-%d (this may take a minute)...\n", serverNum)
	if err := runCmdLoggedContext(ctx, w, "rsync",
		"-a", "--info=progress2", "--no-inc-recursive",
		masterDir+string(os.PathSeparator),
		serverDir+string(os.PathSeparator),
	); err != nil {
		return err
	}
	fmt.Fprintf(w, "  [✓] Master copied to server-%d\n", serverNum)
	return nil
}

func overlayConfigToServerGo(ctx context.Context, w io.Writer, user string, serverNum int) error {
	configDir := filepath.Join("/home", user, "cs2-config", "game")
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game")

	if fi, err := os.Stat(configDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(w, "  [i] No shared config to overlay for server-%d\n", serverNum)
		return nil
	}

	fmt.Fprintf(w, "  [*] Overlaying shared config to server-%d\n", serverNum)
	if err := runCmdLoggedContext(ctx, w, "rsync",
		"-a",
		"--exclude", ".git/",
		configDir+string(os.PathSeparator),
		serverDir+string(os.PathSeparator),
	); err != nil {
		return err
	}
	return nil
}

func configureMetamodGo(w io.Writer, user string, serverNum int, enable bool) error {
	gameinfo := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "gameinfo.gi")

	data, err := os.ReadFile(gameinfo)
	if err != nil {
		fmt.Fprintf(w, "  [!] gameinfo.gi not found for server-%d — cannot configure Metamod\n", serverNum)
		return nil
	}

	backup := gameinfo + ".backup"
	_ = os.WriteFile(backup, data, 0o644)

	content := string(data)
	const needle = "csgo/addons/metamod"

	if enable {
		fmt.Fprintf(w, "  [*] Enabling Metamod in gameinfo.gi for server-%d\n", serverNum)
		if !strings.Contains(content, needle) {
			// Simple insertion: append a new Game line after Game_LowViolence csgo_lv, similar to sed-based script.
			lines := strings.Split(content, "\n")
			var out []string
			inserted := false
			for _, line := range lines {
				out = append(out, line)
				if !inserted && strings.Contains(line, "Game_LowViolence") && strings.Contains(line, "csgo_lv") {
					out = append(out, "                        Game    csgo/addons/metamod")
					inserted = true
				}
			}
			if !inserted {
				out = append(out, "                        Game    csgo/addons/metamod")
			}
			content = strings.Join(out, "\n")
		}
	} else {
		fmt.Fprintf(w, "  [*] Disabling Metamod in gameinfo.gi for server-%d\n", serverNum)
		var out []string
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, needle) {
				continue
			}
			out = append(out, line)
		}
		content = strings.Join(out, "\n")
	}

	if err := os.WriteFile(gameinfo, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

// writeSharedServerConfig writes RCON password and maxplayers to the shared cs2-config/server.cfg
func writeSharedServerConfig(user string, rcon string, maxPlayers int) error {
	sharedCfgDir := filepath.Join("/home", user, "cs2-config", "game", "csgo", "cfg")
	if err := os.MkdirAll(sharedCfgDir, 0o755); err != nil {
		return err
	}
	sharedCfg := filepath.Join(sharedCfgDir, "server.cfg")

	// Read existing shared config if it exists to preserve other settings
	var existingLines []string
	if data, err := os.ReadFile(sharedCfg); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			// Skip RCON and maxplayers lines - we'll add them fresh
			if strings.HasPrefix(trimmed, "rcon_password") ||
				strings.HasPrefix(trimmed, "maxplayers") ||
				strings.HasPrefix(trimmed, "sv_maxplayers") {
				continue
			}
			existingLines = append(existingLines, line)
		}
	}

	// Build new config with shared values at the top
	var out []string
	out = append(out, fmt.Sprintf(`rcon_password "%s"`, rcon))
	if maxPlayers > 0 {
		out = append(out, fmt.Sprintf("maxplayers %d", maxPlayers))
	}
	out = append(out, "")
	out = append(out, existingLines...)

	if err := os.WriteFile(sharedCfg, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return err
	}
	return nil
}

func customizeServerCfgGo(w io.Writer, user string, serverNum int, rcon, hostnamePrefix string, gamePort, tvPort int, maxPlayers int) error {
	// Write shared config (RCON, maxplayers) to cs2-config first
	if err := writeSharedServerConfig(user, rcon, maxPlayers); err != nil {
		return fmt.Errorf("failed to write shared config: %w", err)
	}

	cfgDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}
	serverCfg := filepath.Join(cfgDir, "server.cfg")
	autoexecCfg := filepath.Join(cfgDir, "autoexec.cfg")

	fmt.Fprintf(w, "  [*] Customizing configs for server-%d\n", serverNum)

	if strings.TrimSpace(hostnamePrefix) == "" {
		hostnamePrefix = "CS2 Server"
	}
	fullName := fmt.Sprintf(`hostname "%s #%d"`, hostnamePrefix, serverNum)

	if data, err := os.ReadFile(serverCfg); err == nil {
		// Update existing server.cfg - but be conservative and only update
		// specific values we need to change (hostname, ports, RCON). Keep all
		// other configuration intact to avoid breaking custom settings.
		lines := strings.Split(string(data), "\n")
		var out []string
		updatedHostname := false
		
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			
			// Update hostname if found
			if strings.HasPrefix(trimmed, "hostname ") {
				out = append(out, fullName)
				updatedHostname = true
				continue
			}
			
			// Update or remove old rcon_password (we'll add it at the start)
			if strings.HasPrefix(trimmed, "rcon_password") {
				continue
			}
			
			// Remove old maxplayers/tv settings (we'll add them at the end)
			if strings.HasPrefix(trimmed, "maxplayers") ||
				strings.HasPrefix(trimmed, "sv_maxplayers") ||
				strings.HasPrefix(trimmed, "tv_enable") ||
				strings.HasPrefix(trimmed, "tv_delay") ||
				strings.HasPrefix(trimmed, "tv_port") {
				continue
			}
			
			out = append(out, line)
		}
		
		// Prepend rcon_password (must be first)
		out = append([]string{
			fmt.Sprintf(`rcon_password "%s"`, rcon),
		}, out...)
		
		// Add hostname if it wasn't in the file
		if !updatedHostname {
			out = append(out, "", fullName)
		}
		
		// Add maxplayers if specified
		if maxPlayers > 0 {
			out = append(out, "", fmt.Sprintf("maxplayers %d", maxPlayers))
		}
		
		// Append GOTV settings
		out = append(out, "",
			"// ========================================",
			"// GOTV Configuration (Required for Demo Recording)",
			"// ========================================",
			"tv_enable 1",
			"tv_delay 90",
			fmt.Sprintf("tv_port %d", tvPort),
		)
		
		if err := os.WriteFile(serverCfg, []byte(strings.Join(out, "\n")), 0o644); err != nil {
			return err
		}
	} else {
		// Create new server.cfg
		maxPlayersLine := ""
		if maxPlayers > 0 {
			maxPlayersLine = fmt.Sprintf("maxplayers %d\n", maxPlayers)
		}
		content := fmt.Sprintf(`// ======================================== 
// RCON Configuration
// ========================================
rcon_password "%s"
ip "0.0.0.0"
sv_rcon_banpenalty 0
sv_rcon_maxfailures 5
sv_rcon_minfailures 5
sv_rcon_minfailuretime 30

// ========================================
// Server Identity
// ========================================
%s

%s// ========================================
// Logging
// ========================================
log on
sv_logbans 1
sv_logecho 1
sv_logfile 1
sv_log_onefile 0

// ========================================
// Network Settings
// ========================================
sv_lan 0
sv_password ""

// ========================================
// GOTV Configuration (Required for Demo Recording)
// ========================================
tv_enable 1
tv_delay 90
tv_port %d

// ========================================
// Server Performance
// ========================================
sv_maxrate 0
sv_minrate 196608
sv_maxcmdrate 128
sv_mincmdrate 64
sv_hibernate_when_empty 0
`, rcon, fullName, maxPlayersLine, tvPort)
		if err := os.WriteFile(serverCfg, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// autoexec.cfg with startup info
	autoexec := fmt.Sprintf(`// ===================================================
// Auto-executed on server startup
// ===================================================

// RCON Configuration (ensures it's always set)
rcon_password "%s"
ip "0.0.0.0"

// Server Identity
%s

// Load a default map to initialize the server
// Without this, the server stays in "console" mode and doesn't execute server.cfg
map de_dust2

// Start warmup mode
startwarmup

// Startup message
echo "==========================================="
echo " %s"
echo " Port: Game %d, TV %d"
echo " RCON: Enabled on port %d (TCP)"
echo " RCON Password: %s"
echo "==========================================="
`, rcon, fullName, fullName, gamePort, tvPort, gamePort, rcon)
	if err := os.WriteFile(autoexecCfg, []byte(autoexec), 0o644); err != nil {
		return err
	}
	
	// Note: Ownership of cfg directory (and all other files) is fixed by the
	// comprehensive fixServerOwnership call at the end of bootstrap/reinstall.
	
	return nil
}

// storeGSLTGo writes the GSLT token to the shared cs2-config location.
// The serverNum parameter is kept for compatibility but all servers share the same GSLT.
func storeGSLTGo(w io.Writer, user string, serverNum int, gslt string) error {
	configDir := filepath.Join("/home", user, "cs2-config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}
	gsltFile := filepath.Join(configDir, "server.gslt")
	if err := os.WriteFile(gsltFile, []byte(gslt), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(w, "  [*] Stored shared GSLT token (applies to all servers)\n")
	return nil
}

// --- MatchZy DB (Docker) helpers ---

type matchzyDBConfig struct {
	DatabaseType  string `json:"DatabaseType"`
	MySQLHost     string `json:"MySqlHost"`
	MySQLPort     int    `json:"MySqlPort"`
	MySQLDatabase string `json:"MySqlDatabase"`
	MySQLUsername string `json:"MySqlUsername"`
	MySQLPassword string `json:"MySqlPassword"`
}

func setupMatchZyDatabaseGo(w *bytes.Buffer, cfg BootstrapConfig) error {
	matchzyCfgPath := filepath.Join(cfg.OverridesDir, "game", "csgo", "cfg", "MatchZy", "database.json")

	// When the install wizard provides an explicit DB mode, treat
	// database.json as wizard-managed and overwrite it from the wizard
	// settings on every run. This keeps the config in sync even if the source
	// defaults or old overrides drift over time.
	if strings.TrimSpace(cfg.DBMode) != "" {
		mode := strings.ToLower(strings.TrimSpace(cfg.DBMode))

		if err := os.MkdirAll(filepath.Dir(matchzyCfgPath), 0o755); err != nil {
			return err
		}

		dbCfg := matchzyDBConfig{
			DatabaseType: "MySQL",
		}

		if mode == "external" || cfg.MatchzySkipDocker {
			host := strings.TrimSpace(cfg.ExternalDBHost)
			if host == "" {
				host = "127.0.0.1"
			}
			port := cfg.ExternalDBPort
			if port <= 0 {
				port = 3306
			}
			name := strings.TrimSpace(cfg.ExternalDBName)
			if name == "" {
				name = DefaultMatchzyDBName
			}
			user := strings.TrimSpace(cfg.ExternalDBUser)
			if user == "" {
				user = DefaultMatchzyDBUser
			}
			pass := cfg.ExternalDBPassword
			if strings.TrimSpace(pass) == "" {
				pass = DefaultMatchzyDBPassword
			}

			dbCfg.MySQLHost = host
			dbCfg.MySQLPort = port
			dbCfg.MySQLDatabase = name
			dbCfg.MySQLUsername = user
			dbCfg.MySQLPassword = pass

			// Persist wizard-managed external DB config with an explicit mode
			// marker so tools like VerifyMatchzyDB can detect that Docker
			// provisioning should be skipped even outside the install wizard.
			onDisk := struct {
				matchzyDBConfig
				CSMNote string `json:"__CSM_NOTE,omitempty"`
				DBMode  string `json:"__CSM_DB_MODE,omitempty"`
			}{
				matchzyDBConfig: dbCfg,
				CSMNote:         "This file is managed by CSM's install wizard. Manual edits may be overwritten.",
				DBMode:          "external",
			}

			data, _ := json.MarshalIndent(onDisk, "", "  ")
			if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
				return err
			}

			fmt.Fprintf(w, "  [i] Using external MatchZy database at %s:%d (db=%s, user=%s)\n", host, port, name, user)
			// External DB mode: skip Docker provisioning entirely.
			return nil
		}

		// Docker-managed DB: start from sensible defaults; we'll still detect
		// the primary host IP below and rewrite database.json with that IP as
		// part of the legacy provisioning flow.
		dbCfg.MySQLHost = "127.0.0.1"
		dbCfg.MySQLPort = 3306
		dbCfg.MySQLDatabase = DefaultMatchzyDBName
		dbCfg.MySQLUsername = DefaultMatchzyDBUser
		dbCfg.MySQLPassword = DefaultMatchzyDBPassword

		onDisk := struct {
			matchzyDBConfig
			CSMNote string `json:"__CSM_NOTE,omitempty"`
			DBMode  string `json:"__CSM_DB_MODE,omitempty"`
		}{
			matchzyDBConfig: dbCfg,
			CSMNote:         "This file is managed by CSM's install wizard. Manual edits may be overwritten.",
			DBMode:          "docker",
		}

		data, _ := json.MarshalIndent(onDisk, "", "  ")
		if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
			return err
		}
	}

	// Create default config if missing.
	if _, err := os.Stat(matchzyCfgPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(matchzyCfgPath), 0o755); err != nil {
			return err
		}
		def := matchzyDBConfig{
			DatabaseType:  "MySQL",
			MySQLHost:     "127.0.0.1",
			MySQLPort:     3306,
			MySQLDatabase: DefaultMatchzyDBName,
			MySQLUsername: DefaultMatchzyDBUser,
			MySQLPassword: DefaultMatchzyDBPassword,
		}

		onDisk := struct {
			matchzyDBConfig
			CSMNote string `json:"__CSM_NOTE,omitempty"`
		}{
			matchzyDBConfig: def,
			CSMNote:         "This file is managed by CSM's install wizard. Manual edits may be overwritten.",
		}

		data, _ := json.MarshalIndent(onDisk, "", "  ")
		if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
			return err
		}
		fmt.Fprintf(w, "  [✓] Created %s with default values\n", matchzyCfgPath)
	}

	data, err := os.ReadFile(matchzyCfgPath)
	if err != nil {
		return err
	}
	var dbCfg matchzyDBConfig
	if err := json.Unmarshal(data, &dbCfg); err != nil {
		return fmt.Errorf("MatchZy database config is not valid JSON: %w", err)
	}

	if strings.ToLower(dbCfg.DatabaseType) != "mysql" {
		fmt.Fprintf(w, "  [i] DatabaseType=%s; skipping Docker provisioning\n", dbCfg.DatabaseType)
		return nil
	}
	if cfg.MatchzySkipDocker {
		fmt.Fprintln(w, "  [i] MATCHZY_SKIP_DOCKER=1: Skipping Docker provisioning (using external database).")
		return nil
	}

	if err := ensureDockerGo(w); err != nil {
		return err
	}

	if dbCfg.MySQLPort == 0 {
		dbCfg.MySQLPort = 3306
	}

	containerName := getenvDefault("MATCHZY_DB_CONTAINER", DefaultMatchzyContainerName)
	volumeName := getenvDefault("MATCHZY_DB_VOLUME", DefaultMatchzyVolumeName)
	imageName := getenvDefault("MATCHZY_DB_IMAGE", "mysql:8.0")
	rootPass := getenvDefault("MATCHZY_DB_ROOT_PASSWORD", DefaultMatchzyRootPassword)

	containerExists := false
	currentPort := ""

	if out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == containerName {
				containerExists = true
				break
			}
		}
	}

	// For a full fresh install, drop any existing MatchZy container and volume
	// so we start from a clean database state.
	if cfg.FreshInstall {
		fmt.Fprintf(w, "  [*] FRESH_INSTALL=1: Deleting existing MatchZy container %q (if present)\n", containerName)
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		fmt.Fprintf(w, "  [*] FRESH_INSTALL=1: Deleting existing MatchZy volume %q (if present)\n", volumeName)
		_ = exec.Command("docker", "volume", "rm", volumeName).Run()
		containerExists = false
		currentPort = ""
	}

	if containerExists {
		inspect := exec.Command("docker", "inspect", "-f", "{{range $p, $cfg := .NetworkSettings.Ports}}{{if eq $p \"3306/tcp\"}}{{(index $cfg 0).HostPort}}{{end}}{{end}}", containerName)
		if out, err := inspect.CombinedOutput(); err == nil {
			currentPort = strings.TrimSpace(string(out))
		}
	}

	if isPortInUse(dbCfg.MySQLPort) {
		if !containerExists || currentPort != strconv.Itoa(dbCfg.MySQLPort) {
			fmt.Fprintf(w, "  [!] Port %d is already in use on this host.\n", dbCfg.MySQLPort)
			return fmt.Errorf("mysql port %d in use", dbCfg.MySQLPort)
		}
	}

	// For Docker mode, always use localhost since the MySQL container is
	// exposed on the host's port 3306. The CS2 server runs on the same host
	// and should connect via 127.0.0.1, not the detected primary IP (which
	// might be a VPN interface like Wireguard that can't reach itself).
	hostIP := detectPrimaryIPGo()
	dbCfg.MySQLHost = "127.0.0.1" // Always use localhost for Docker mode

	// Update database.json with host/port/db/user/pass, preserving the
	// wizard-management note so users know manual edits may be overwritten.
	onDisk := struct {
		matchzyDBConfig
		CSMNote string `json:"__CSM_NOTE,omitempty"`
		DBMode  string `json:"__CSM_DB_MODE,omitempty"`
	}{
		matchzyDBConfig: dbCfg,
		CSMNote:         "This file is managed by CSM's install wizard. Manual edits may be overwritten.",
		DBMode:          "docker",
	}

	data, _ = json.MarshalIndent(onDisk, "", "  ")
	if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
		return err
	}

	recreate := false
	if containerExists {
		if currentPort != strconv.Itoa(dbCfg.MySQLPort) {
			fmt.Fprintf(w, "  [*] Recreating %s to use host port %d\n", containerName, dbCfg.MySQLPort)
			_ = exec.Command("docker", "rm", "-f", containerName).Run()
			recreate = true
		}
	} else {
		recreate = true
	}

	if recreate {
		_ = runCmdLogged(w, "docker", "pull", imageName)
		args := []string{
			"run", "-d",
			"--name", containerName,
			"-e", "MYSQL_ROOT_PASSWORD=" + rootPass,
			"-e", "MYSQL_DATABASE=" + dbCfg.MySQLDatabase,
			"-e", "MYSQL_USER=" + dbCfg.MySQLUsername,
			"-e", "MYSQL_PASSWORD=" + dbCfg.MySQLPassword,
			"-p", fmt.Sprintf("%d:3306", dbCfg.MySQLPort),
			"-v", volumeName + ":/var/lib/mysql",
			"--restart", "unless-stopped",
			imageName,
		}
		if err := runCmdLogged(w, "docker", args...); err != nil {
			return err
		}
		fmt.Fprintf(w, "  [✓] Started MatchZy MySQL container (%s) on port %d\n", containerName, dbCfg.MySQLPort)
	} else {
		if err := runCmdLogged(w, "docker", "start", containerName); err != nil {
			return err
		}
		fmt.Fprintf(w, "  [✓] MatchZy MySQL container (%s) already running\n", containerName)
	}

	// Wait for MySQL to be ready
	ready := false
	for i := 0; i < 30; i++ {
		cmd := exec.Command("docker", "exec", containerName, "mysqladmin", "ping", "-h", "127.0.0.1", "-uroot", "-p"+rootPass, "--silent")
		if err := cmd.Run(); err == nil {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if ready {
		fmt.Fprintf(w, "  [✓] MatchZy database is ready at %s:%d\n", hostIP, dbCfg.MySQLPort)
		if err := ensureMatchZyDatabaseExistsGo(w, containerName, dbCfg, rootPass); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(w, "  [i] MatchZy database is starting up (Docker container is running)")
	}

	return nil
}

func ensureMatchZyDatabaseExistsGo(w *bytes.Buffer, containerName string, cfg matchzyDBConfig, rootPass string) error {
	// Check if DB exists
	dbExistsCmd := exec.Command("docker", "exec", containerName, "mysql", "-uroot", "-p"+rootPass,
		"-e", "SHOW DATABASES LIKE '"+cfg.MySQLDatabase+"';", "-sN")
	out, err := dbExistsCmd.CombinedOutput()
	if err != nil {
		return err
	}
	dbExists := strings.TrimSpace(string(out)) == cfg.MySQLDatabase

	if !dbExists {
		fmt.Fprintf(w, "  [*] Database '%s' not found, creating it...\n", cfg.MySQLDatabase)
		createDB := exec.Command("docker", "exec", containerName, "mysql", "-uroot", "-p"+rootPass,
			"-e", "CREATE DATABASE IF NOT EXISTS `"+cfg.MySQLDatabase+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;")
		if err := createDB.Run(); err != nil {
			fmt.Fprintf(w, "  [!] Failed to create database '%s'\n", cfg.MySQLDatabase)
			return err
		}
		fmt.Fprintf(w, "  [✓] Database '%s' created successfully\n", cfg.MySQLDatabase)
	} else {
		fmt.Fprintf(w, "  [✓] Database '%s' already exists\n", cfg.MySQLDatabase)
	}

	// Ensure user exists and has permissions
	checkUser := exec.Command("docker", "exec", containerName, "mysql", "-uroot", "-p"+rootPass,
		"-e", "SELECT COUNT(*) FROM mysql.user WHERE User='"+cfg.MySQLUsername+"' AND Host='%';", "-sN")
	out, err = checkUser.CombinedOutput()
	if err != nil {
		return err
	}
	countStr := strings.TrimSpace(string(out))
	if countStr == "" {
		countStr = "0"
	}
	userExists := countStr != "0"

	if !userExists {
		fmt.Fprintf(w, "  [*] User '%s' not found, creating it...\n", cfg.MySQLUsername)
		createUser := exec.Command("docker", "exec", containerName, "mysql", "-uroot", "-p"+rootPass,
			"-e", "CREATE USER IF NOT EXISTS '"+cfg.MySQLUsername+"'@'%' IDENTIFIED BY '"+cfg.MySQLPassword+"'; GRANT ALL PRIVILEGES ON `"+cfg.MySQLDatabase+"`.* TO '"+cfg.MySQLUsername+"'@'%'; FLUSH PRIVILEGES;")
		if err := createUser.Run(); err != nil {
			fmt.Fprintf(w, "  [!] Failed to create user '%s'\n", cfg.MySQLUsername)
			return err
		}
		fmt.Fprintf(w, "  [✓] User '%s' created with permissions on '%s'\n", cfg.MySQLUsername, cfg.MySQLDatabase)
	} else {
		grant := exec.Command("docker", "exec", containerName, "mysql", "-uroot", "-p"+rootPass,
			"-e", "GRANT ALL PRIVILEGES ON `"+cfg.MySQLDatabase+"`.* TO '"+cfg.MySQLUsername+"'@'%'; FLUSH PRIVILEGES;")
		_ = grant.Run()
		fmt.Fprintf(w, "  [✓] User '%s' has permissions on '%s'\n", cfg.MySQLUsername, cfg.MySQLDatabase)
	}
	return nil
}

// teeWriter mirrors writes into both the in-memory bootstrap buffer and an
// on-disk log file so that the TUI can tail live output while steamcmd runs.
type teeWriter struct {
	buf  *bytes.Buffer
	file *os.File
}

func (t *teeWriter) Write(p []byte) (int, error) {
	n, err := t.buf.Write(p)
	if t.file != nil {
		if _, ferr := t.file.Write(p); ferr != nil && err == nil {
			err = ferr
		}
	}
	return n, err
}

func ensureDockerGo(w *bytes.Buffer) error {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(w, "  [!] Docker is required for the MatchZy database. Please install Docker Engine.")
		return fmt.Errorf("docker is required")
	}
	_ = exec.Command("systemctl", "enable", "docker").Run()
	_ = exec.Command("systemctl", "start", "docker").Run()
	return nil
}

func detectPrimaryIPGo() string {
	// Try routing table
	if out, err := exec.Command("ip", "route", "get", "1.1.1.1").CombinedOutput(); err == nil {
		fields := strings.Fields(string(out))
		for i, f := range fields {
			if f == "src" && i+1 < len(fields) {
				if ip := net.ParseIP(fields[i+1]); ip != nil {
					return ip.String()
				}
			}
		}
	}
	// Fallback: hostname -I
	if out, err := exec.Command("hostname", "-I").CombinedOutput(); err == nil {
		for _, tok := range strings.Fields(string(out)) {
			if ip := net.ParseIP(tok); ip != nil {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

func isPortInUse(port int) bool {
	if port <= 0 {
		return false
	}
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	_ = l.Close()
	return false
}

func stopTmuxServerGo(w *bytes.Buffer, user string, serverNum int) error {
	session := fmt.Sprintf("cs2-%d", serverNum)
	cmd := exec.Command("su", "-", user, "-c", "tmux has-session -t "+session)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(w, "  [i] Server %d not running in tmux, skipping stop\n", serverNum)
		return nil
	}

	fmt.Fprintf(w, "  [*] Stopping tmux session for server-%d\n", serverNum)
	_ = exec.Command("su", "-", user, "-c", "tmux send-keys -t "+session+" 'quit' C-m").Run()
	time.Sleep(2 * time.Second)
	_ = exec.Command("su", "-", user, "-c", "tmux kill-session -t "+session).Run()
	return nil
}

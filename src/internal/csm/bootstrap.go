package csm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BootstrapConfig mirrors the high-level options used by the original
// bootstrap_cs2.sh script.
type BootstrapConfig struct {
	CS2User           string
	NumServers        int
	BaseGamePort      int
	BaseTVPort        int
	EnableMetamod     bool
	FreshInstall      bool
	UpdateMaster      bool
	RCONPassword      string
	MatchzySkipDocker bool
	GameFilesDir      string // typically <root>/game_files
	OverridesDir      string // typically <root>/overrides
}

// Bootstrap installs or redeploys the CS2 servers, performing roughly the
// same steps as scripts/bootstrap_cs2.sh. It returns a human-readable log.
func Bootstrap(cfg BootstrapConfig) (string, error) {
	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	if os.Geteuid() != 0 {
		return "", fmt.Errorf("bootstrap must be run as root (use sudo)")
	}

	if cfg.CS2User == "" {
		cfg.CS2User = "cs2"
	}
	if cfg.NumServers <= 0 {
		cfg.NumServers = 3
	}
	if cfg.BaseGamePort == 0 {
		cfg.BaseGamePort = 27015
	}
	if cfg.BaseTVPort == 0 {
		cfg.BaseTVPort = 27020
	}
	if cfg.RCONPassword == "" {
		cfg.RCONPassword = "ntlan2025"
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
	if err := ensureDefaultOverrides(cfg.OverridesDir); err != nil {
		log("  [!] Failed to write default overrides to %s: %v", cfg.OverridesDir, err)
	}

	log("[*] Setting up %d CS2 servers...", cfg.NumServers)
	log("")

	log("[1/5] Creating CS2 user...")
	if err := createCS2User(&buf, cfg.CS2User); err != nil {
		log("  [!] Failed to create CS2 user: %v", err)
		return buf.String(), err
	}
	log("")

	log("[2/5] Installing/updating master CS2 installation...")
	if err := installMasterViaSteamCMD(&buf, cfg); err != nil {
		log("  [!] Failed to install/update master: %v", err)
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

	for i := 1; i <= cfg.NumServers; i++ {
		gamePort := cfg.BaseGamePort + (i-1)*10
		tvPort := cfg.BaseTVPort + (i-1)*10

		log("[%d/%d] Setting up server-%d...", i, cfg.NumServers, i)

		if err := stopTmuxServerGo(&buf, cfg.CS2User, i); err != nil {
			log("  [i] Could not stop tmux session for server-%d: %v", i, err)
		}

		if err := copyMasterToServerGo(&buf, cfg.CS2User, i, cfg.FreshInstall); err != nil {
			log("  [!] Copy master to server-%d failed: %v", i, err)
		}

		if err := overlayConfigToServerGo(&buf, cfg.CS2User, i); err != nil {
			log("  [!] Overlay config to server-%d failed: %v", i, err)
		}

		if err := configureMetamodGo(&buf, cfg.CS2User, i, cfg.EnableMetamod); err != nil {
			log("  [!] Configure Metamod for server-%d failed: %v", i, err)
		}

		if err := customizeServerCfgGo(&buf, cfg.CS2User, i, cfg.RCONPassword, gamePort, tvPort); err != nil {
			log("  [!] Customize server.cfg for server-%d failed: %v", i, err)
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

func installMasterViaSteamCMD(w *bytes.Buffer, cfg BootstrapConfig) error {
	homeDir := filepath.Join("/home", cfg.CS2User)
	masterDir := filepath.Join(homeDir, "master-install")
	gameinfo := filepath.Join(masterDir, "game", "csgo", "gameinfo.gi")

	if cfg.FreshInstall {
		if _, err := os.Stat(masterDir); err == nil {
			fmt.Fprintln(w, "  [*] FRESH_INSTALL=1: Deleting existing master install")
			if err := os.RemoveAll(masterDir); err != nil {
				return err
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
	script := fmt.Sprintf(`
set -e
steamcmd +force_install_dir "%s" +login anonymous +app_update 730 validate +quit
`, masterDir)

	cmd := exec.Command("su", "-", cfg.CS2User, "-c", script)

	// If CSM_BOOTSTRAP_LOG is set, stream steamcmd output into that file so
	// the TUI can show a live tail while the install is running.
	if logPath, ok := os.LookupEnv("CSM_BOOTSTRAP_LOG"); ok && logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			defer f.Close()
			tw := &teeWriter{buf: w, file: f}
			cmd.Stdout = tw
			cmd.Stderr = tw
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("steamcmd failed: %w", err)
			}
		} else {
			// Fall back to non-streaming mode if we can't open the log file.
			out, err := cmd.CombinedOutput()
			if len(out) > 0 {
				fmt.Fprintln(w, string(out))
			}
			if err != nil {
				return fmt.Errorf("steamcmd failed: %w", err)
			}
		}
	} else {
		// No streaming requested; just run and capture the full output.
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			fmt.Fprintln(w, string(out))
		}
		if err != nil {
			return fmt.Errorf("steamcmd failed: %w", err)
		}
	}

	if _, err := os.Stat(gameinfo); err == nil {
		fmt.Fprintln(w, "  [✓] Master install complete/updated")
		return nil
	}
	fmt.Fprintln(w, "  [!] Master install failed - gameinfo.gi not found")
	return fmt.Errorf("gameinfo.gi not found after steamcmd")
}

func ensureBootstrapDependencies(w *bytes.Buffer) error {
	if _, err := exec.LookPath("apt-get"); err != nil {
		fmt.Fprintln(w, "Only Debian/Ubuntu-style distributions are supported for automated dependencies.")
		return fmt.Errorf("apt-get not found")
	}

	// Enable i386 architecture and install dependencies quietly.
	_ = exec.Command("dpkg", "--add-architecture", "i386").Run()
	if err := runCmdLogged(w, "apt-get", "update", "-qq"); err != nil {
		return err
	}

	pkgs := []string{
		"curl", "wget", "file", "tar", "bzip2", "xz-utils", "unzip",
		"ca-certificates", "lib32gcc-s1", "lib32stdc++6", "libc6-i386",
		"net-tools", "tmux", "steamcmd", "rsync", "jq",
	}
	args := append([]string{"install", "-y", "-qq"}, pkgs...)
	if err := runCmdLogged(w, "apt-get", args...); err != nil {
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

func copyMasterToServerGo(w *bytes.Buffer, user string, serverNum int, fresh bool) error {
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

	fmt.Fprintf(w, "  [*] Copying master to server-%d\n", serverNum)
	if err := runCmdLogged(w, "rsync",
		"-a", "--info=PROGRESS2",
		masterDir+string(os.PathSeparator),
		serverDir+string(os.PathSeparator),
	); err != nil {
		return err
	}
	return nil
}

func overlayConfigToServerGo(w *bytes.Buffer, user string, serverNum int) error {
	configDir := filepath.Join("/home", user, "cs2-config", "game")
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game")

	if fi, err := os.Stat(configDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(w, "  [i] No shared config to overlay for server-%d\n", serverNum)
		return nil
	}

	fmt.Fprintf(w, "  [*] Overlaying shared config to server-%d\n", serverNum)
	if err := runCmdLogged(w, "rsync",
		"-a",
		"--exclude", ".git/",
		configDir+string(os.PathSeparator),
		serverDir+string(os.PathSeparator),
	); err != nil {
		return err
	}
	return nil
}

func configureMetamodGo(w *bytes.Buffer, user string, serverNum int, enable bool) error {
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

func customizeServerCfgGo(w *bytes.Buffer, user string, serverNum int, rcon string, gamePort, tvPort int) error {
	cfgDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return err
	}
	serverCfg := filepath.Join(cfgDir, "server.cfg")
	autoexecCfg := filepath.Join(cfgDir, "autoexec.cfg")

	fmt.Fprintf(w, "  [*] Customizing configs for server-%d\n", serverNum)

	if data, err := os.ReadFile(serverCfg); err == nil {
		// Update existing server.cfg
		lines := strings.Split(string(data), "\n")
		var out []string
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "hostname ") {
				out = append(out, fmt.Sprintf(`hostname "NTLAN CS2 Server #%d"`, serverNum))
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "rcon_password") {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "tv_enable") ||
				strings.HasPrefix(strings.TrimSpace(line), "tv_delay") ||
				strings.HasPrefix(strings.TrimSpace(line), "tv_port") {
				continue
			}
			out = append(out, line)
		}
		// Prepend rcon_password
		out = append([]string{
			fmt.Sprintf(`rcon_password "%s"`, rcon),
		}, out...)
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
		content := fmt.Sprintf(`// ======================================== 
// RCON Configuration
// ========================================
rcon_password "%s"
ip "0.0.0.0"

// ========================================
// Server Identity
// ========================================
hostname "NTLAN CS2 Server #%d"

// ========================================
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
`, rcon, serverNum, tvPort)
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
hostname "NTLAN CS2 Server #%d"

// Start warmup mode
startwarmup

// Startup message
echo "==========================================="
echo " NTLAN CS2 Server #%d"
echo " Port: Game %d, TV %d"
echo " RCON: Enabled on port %d (TCP)"
echo " RCON Password: %s"
echo "==========================================="
`, rcon, serverNum, serverNum, gamePort, tvPort, gamePort, rcon)
	if err := os.WriteFile(autoexecCfg, []byte(autoexec), 0o644); err != nil {
		return err
	}
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

	// Create default config if missing.
	if _, err := os.Stat(matchzyCfgPath); err != nil {
		if err := os.MkdirAll(filepath.Dir(matchzyCfgPath), 0o755); err != nil {
			return err
		}
		def := matchzyDBConfig{
			DatabaseType:  "MySQL",
			MySQLHost:     "127.0.0.1",
			MySQLPort:     3306,
			MySQLDatabase: "matchzy",
			MySQLUsername: "matchzy",
			MySQLPassword: "matchzy",
		}
		data, _ := json.MarshalIndent(def, "", "  ")
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

	containerName := getenvDefault("MATCHZY_DB_CONTAINER", "matchzy-mysql")
	volumeName := getenvDefault("MATCHZY_DB_VOLUME", "matchzy-mysql-data")
	imageName := getenvDefault("MATCHZY_DB_IMAGE", "mysql:8.0")
	rootPass := getenvDefault("MATCHZY_DB_ROOT_PASSWORD", "MatchZyRoot!2025")

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

	hostIP := detectPrimaryIPGo()
	dbCfg.MySQLHost = hostIP

	// Update database.json with host/port/db/user/pass
	data, _ = json.MarshalIndent(dbCfg, "", "  ")
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

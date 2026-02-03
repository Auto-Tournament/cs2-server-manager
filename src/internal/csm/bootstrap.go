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
	MaxPlayers        int    // 0 means use default
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
	switch cfg.RCONPassword {
	case "":
		// Use a neutral fallback rather than an event-specific password; the
		// install wizard will normally require users to set this explicitly.
		log("  [!] WARNING: No RCON password supplied; using default %q", DefaultRCONPassword)
		log("  [!] SECURITY: You MUST change the RCON password after installation!")
		log("  [!] SECURITY: Default passwords are insecure and publicly known!")
		cfg.RCONPassword = DefaultRCONPassword
	case DefaultRCONPassword:
		log("  [!] WARNING: You are using the default RCON password!")
		log("  [!] SECURITY: Please change it to a strong, unique password immediately!")
	}

	// Determine the persistent project root for game_files/ and logs/.
	// Use ResolveRoot so bootstrap matches the same on-disk layout as other
	// flows (notably plugin updates), and so global installs default to
	// /opt/cs2-server-manager instead of the directory containing the binary.
	root := ResolveRoot()
	if cfg.GameFilesDir == "" {
		cfg.GameFilesDir = filepath.Join(root, "game_files")
	}
	// Overrides are stored in the CS2 user's home directory for easier access
	if cfg.OverridesDir == "" {
		cfg.OverridesDir = filepath.Join("/home", cfg.CS2User, "overrides")
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
	// Add timeout to SteamCMD operation to prevent indefinite hanging
	steamCtx, steamCancel := contextWithTimeout(ctx, TimeoutSteamCMD)
	defer steamCancel()

	if err := installMasterViaSteamCMD(steamCtx, &buf, cfg); err != nil {
		if steamCtx.Err() == context.DeadlineExceeded {
			log("  [!] SteamCMD operation timed out after %v", TimeoutSteamCMD)
			log("  [*] This may indicate network issues or a very slow connection")
			log("  [*] Check /tmp/csm-bootstrap.log for detailed steamcmd output")
		} else {
			log("  [!] Failed to install/update master: %v", err)
			log("  [*] Check /tmp/csm-bootstrap.log for detailed steamcmd output")
		}
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
			if err := storeGSLTGo(&buf, cfg.CS2User, cfg.GSLT); err != nil {
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
// The following functions have been moved to separate files for better organization:
// - createCS2User -> user_management.go
// - installMasterViaSteamCMD -> steam.go
// - setupSteamSDKLinksGo -> steam.go
// - copyMasterToServerGo -> server_deployment.go
// - overlayConfigToServerGo -> server_deployment.go

type osReleaseInfo struct {
	ID              string
	VersionID       string
	VersionCodename string
}

func readOSRelease() osReleaseInfo {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return osReleaseInfo{}
	}
	var info osReleaseInfo
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch key {
		case "ID":
			info.ID = strings.ToLower(val)
		case "VERSION_ID":
			info.VersionID = strings.ToLower(val)
		case "VERSION_CODENAME":
			info.VersionCodename = strings.ToLower(val)
		}
	}
	return info
}

func parseUbuntuVersionID(versionID string) (major, minor int, ok bool) {
	versionID = strings.TrimSpace(strings.Trim(versionID, `"'`))
	parts := strings.Split(versionID, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

func parseDebianMajor(versionID string) (major int, ok bool) {
	versionID = strings.TrimSpace(strings.Trim(versionID, `"'`))
	maj, err := strconv.Atoi(versionID)
	if err != nil {
		return 0, false
	}
	return maj, true
}

// shouldUseSteamRuntimeLauncher reports whether we should prefer launching the
// CS2 server via Steam Runtime (SteamRT3) for CounterStrikeSharp compatibility.
//
// It can be overridden via CSM_STEAMRT:
//   - "1"/"true"/"on"  => force enable
//   - "0"/"false"/"off" => force disable
//   - empty/other      => auto
func shouldUseSteamRuntimeLauncher() (enabled bool, mode string) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CSM_STEAMRT"))) {
	case "1", "true", "on", "yes":
		return true, "forced_on"
	case "0", "false", "off", "no":
		return false, "forced_off"
	}

	osr := readOSRelease()
	switch osr.ID {
	case "debian":
		// Debian 13 (trixie) and newer have reported issues with CSS needing
		// Steam Runtime for compatibility.
		if maj, ok := parseDebianMajor(osr.VersionID); ok && maj >= 13 {
			return true, "auto_debian13plus"
		}
		if osr.VersionCodename == "trixie" || osr.VersionCodename == "forky" {
			return true, "auto_debian_new"
		}
	case "ubuntu":
		// Ubuntu 25.04+ has reported similar issues.
		if maj, min, ok := parseUbuntuVersionID(osr.VersionID); ok {
			if maj > 25 || (maj == 25 && min >= 4) {
				return true, "auto_ubuntu2504plus"
			}
		}
	}
	return false, "auto_off"
}

func readAptSourcesText() string {
	var b strings.Builder
	if data, err := os.ReadFile("/etc/apt/sources.list"); err == nil {
		b.WriteString(string(data))
		b.WriteString("\n")
	}
	entries, err := os.ReadDir("/etc/apt/sources.list.d")
	if err != nil {
		return b.String()
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".list") {
			continue
		}
		p := filepath.Join("/etc/apt/sources.list.d", name)
		if data, err := os.ReadFile(p); err == nil {
			b.WriteString(string(data))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func aptSourcesContainToken(sourcesText, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	// Very simple heuristic: check for the token as a standalone-ish word.
	// Good enough for "contrib", "non-free", "non-free-firmware", "multiverse".
	for _, field := range strings.Fields(strings.ToLower(sourcesText)) {
		if field == token {
			return true
		}
	}
	return false
}

func updateAptSourcesListFile(path string, addTokens []string) (backupPath string, changed bool, _ error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}

	backupPath = fmt.Sprintf("%s.csm.bak-%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backupPath, orig, 0o644); err != nil {
		return "", false, err
	}

	lines := strings.Split(string(orig), "\n")
	for i, line := range lines {
		raw := line
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !(strings.HasPrefix(trimmed, "deb ") || strings.HasPrefix(trimmed, "deb-src ")) {
			continue
		}

		// Preserve inline comments by only modifying the content before '#'.
		content, comment, hasComment := strings.Cut(raw, "#")
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		fields := strings.Fields(content)
		// Expected format:
		//   deb <uri> <suite> <component...>
		//   deb-src <uri> <suite> <component...>
		// We only modify lines that already specify at least one component.
		if len(fields) < 4 {
			continue
		}

		existing := map[string]bool{}
		for _, c := range fields[3:] {
			existing[strings.ToLower(c)] = true
		}
		var toAdd []string
		for _, t := range addTokens {
			tt := strings.ToLower(strings.TrimSpace(t))
			if tt == "" {
				continue
			}
			if !existing[tt] {
				toAdd = append(toAdd, tt)
			}
		}
		if len(toAdd) == 0 {
			continue
		}

		changed = true
		newFields := append(append([]string{}, fields...), toAdd...)
		newLine := strings.Join(newFields, " ")
		if hasComment {
			newLine = strings.TrimSpace(newLine) + " #" + comment
		}
		lines[i] = newLine
	}

	if !changed {
		// We still created a backup; keep it, but write nothing.
		return backupPath, false, nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return backupPath, false, err
	}
	return backupPath, true, nil
}

func tryEnableSteamcmdAptComponents(w io.Writer) (attempted bool, backupPath string, _ error) {
	osr := readOSRelease()
	sourcesText := readAptSourcesText()

	switch osr.ID {
	case "debian":
		need := []string{"contrib", "non-free", "non-free-firmware"}
		missing := false
		for _, t := range need {
			if !aptSourcesContainToken(sourcesText, t) {
				missing = true
				break
			}
		}
		if !missing {
			return false, "", nil
		}
		backup, changed, err := updateAptSourcesListFile("/etc/apt/sources.list", need)
		if err != nil {
			return true, backup, err
		}
		if changed {
			fmt.Fprintf(w, "[deps] Updated /etc/apt/sources.list to include: %s\n", strings.Join(need, ", "))
		}
		return true, backup, nil
	case "ubuntu":
		if aptSourcesContainToken(sourcesText, "multiverse") {
			return false, "", nil
		}
		backup, changed, err := updateAptSourcesListFile("/etc/apt/sources.list", []string{"multiverse"})
		if err != nil {
			return true, backup, err
		}
		if changed {
			fmt.Fprintln(w, "[deps] Updated /etc/apt/sources.list to include: multiverse")
		}
		return true, backup, nil
	default:
		return false, "", nil
	}
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

	// Recover from interrupted dpkg operations (common when a previous apt run
	// was killed). Without this, apt-get install will fail with:
	// "E: dpkg was interrupted, you must manually run 'sudo dpkg --configure -a'".
	fmt.Fprintln(w, "[deps] Ensuring dpkg is configured: dpkg --configure -a")
	if err := runCmdLoggedContext(ctx, w, "dpkg", "--configure", "-a"); err != nil {
		return fmt.Errorf("dpkg --configure -a failed: %w", err)
	}

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
	var installOut bytes.Buffer
	installWriter := io.MultiWriter(w, &installOut)
	if err := runCmdLoggedContext(ctx, installWriter, "apt-get", args...); err != nil {
		// SteamCMD is not available in default Debian sources unless the right
		// repo components are enabled (commonly contrib/non-free*). On Ubuntu it
		// is typically in multiverse. When we detect this specific failure,
		// return a much more actionable hint.
		out := strings.ToLower(installOut.String())
		if strings.Contains(out, "unable to locate package steamcmd") ||
			(strings.Contains(out, "package steamcmd") && strings.Contains(out, "no installation candidate")) {
			// Try to fix it automatically when running as root: update apt sources
			// to include the needed components, then retry apt-get update + install.
			fmt.Fprintln(w, "[deps] steamcmd not found; attempting to enable required apt components automatically...")
			attempted, backup, fixErr := tryEnableSteamcmdAptComponents(w)
			if attempted {
				if strings.TrimSpace(backup) != "" {
					fmt.Fprintf(w, "[deps] Backup written: %s\n", backup)
				}
				if fixErr == nil {
					fmt.Fprintln(w, "[deps] Running: apt-get update (after sources change)")
					if err2 := runCmdLoggedContext(ctx, w, "apt-get", "update"); err2 == nil {
						fmt.Fprintf(w, "[deps] Retrying: apt-get %s\n", strings.Join(args, " "))
						var retryOut bytes.Buffer
						retryWriter := io.MultiWriter(w, &retryOut)
						if err3 := runCmdLoggedContext(ctx, retryWriter, "apt-get", args...); err3 == nil {
							// Success after auto-fix.
							if werr := EnsureSteamcmdWrapper(w); werr != nil {
								return werr
							}
							return nil
						}
						// If retry failed, fall through to the user-facing error message below.
					}
				}
				// If the auto-fix failed, fall through to the user-facing error message below.
				if fixErr != nil {
					fmt.Fprintf(w, "[deps] Auto-fix failed: %v\n", fixErr)
				}
			}

			osr := readOSRelease()
			sourcesText := readAptSourcesText()

			var detectedHint string
			switch osr.ID {
			case "debian":
				missing := []string{}
				if !aptSourcesContainToken(sourcesText, "contrib") {
					missing = append(missing, "contrib")
				}
				if !aptSourcesContainToken(sourcesText, "non-free") {
					missing = append(missing, "non-free")
				}
				// Bookworm often needs this enabled too for firmware packages.
				if !aptSourcesContainToken(sourcesText, "non-free-firmware") {
					missing = append(missing, "non-free-firmware")
				}
				if len(missing) > 0 {
					codename := osr.VersionCodename
					if codename == "" {
						codename = "bookworm"
					}
					detectedHint = fmt.Sprintf(
						"\nDetected Debian (%s). Your apt sources appear to be missing: %s\n\n"+
							"Example /etc/apt/sources.list entries:\n"+
							"  deb http://deb.debian.org/debian %s main contrib non-free non-free-firmware\n"+
							"  deb http://deb.debian.org/debian %s-updates main contrib non-free non-free-firmware\n"+
							"  deb http://security.debian.org/debian-security %s-security main contrib non-free non-free-firmware\n",
						codename,
						strings.Join(missing, ", "),
						codename,
						codename,
						codename,
					)
				}
			case "ubuntu":
				if !aptSourcesContainToken(sourcesText, "multiverse") {
					codename := osr.VersionCodename
					if codename == "" {
						codename = "your-release-codename"
					}
					detectedHint = fmt.Sprintf(
						"\nDetected Ubuntu (%s). Your apt sources appear to be missing: multiverse\n",
						codename,
					)
				}
			}

			return fmt.Errorf(
				"steamcmd package not found in your apt repositories.\n\n"+
					"This usually means your apt sources don't include the components that provide SteamCMD.\n\n"+
					"Debian:\n"+
					"  - Enable contrib + non-free (and often non-free-firmware on Bookworm) in /etc/apt/sources.list\n"+
					"    or /etc/apt/sources.list.d/*.list, then run:\n"+
					"      sudo apt-get update\n"+
					"      sudo apt-get install steamcmd\n\n"+
					"Ubuntu:\n"+
					"  - Enable multiverse, then run:\n"+
					"      sudo add-apt-repository multiverse\n"+
					"      sudo apt-get update\n"+
					"      sudo apt-get install steamcmd\n\n"+
					"After installing SteamCMD, rerun this CSM action.\n\n"+
					"Tip: full logs are written to <CSM root>/logs/csm.log (default /opt/cs2-server-manager/logs/csm.log).\n\n"+
					"%s\n"+
					"Original error: %w",
				strings.TrimSpace(detectedHint),
				err,
			)
		}
		return err
	}

	// Ensure /usr/bin/steamcmd wrapper exists (linking to /usr/games/steamcmd).
	return EnsureSteamcmdWrapper(w)
}

// setupSteamSDKLinksGo is now in steam.go

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

// copyMasterToServerGo and overlayConfigToServerGo are now in server_deployment.go

func configureMetamodGo(w io.Writer, user string, serverNum int, enable bool) error {
	gameinfo := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "gameinfo.gi")

	data, err := os.ReadFile(gameinfo)
	if err != nil {
		fmt.Fprintf(w, "  [!] gameinfo.gi not found for server-%d — cannot configure Metamod\n", serverNum)
		return nil
	}

	backup := gameinfo + ".backup"
	if err := os.WriteFile(backup, data, 0o644); err != nil {
		fmt.Fprintf(w, "  [!] Warning: Failed to create backup %s: %v\n", backup, err)
	}

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

			// Remove old RCON ban settings (we'll add them fresh with disabled values)
			if strings.HasPrefix(trimmed, "sv_rcon_banpenalty") ||
				strings.HasPrefix(trimmed, "sv_rcon_maxfailures") ||
				strings.HasPrefix(trimmed, "sv_rcon_minfailures") ||
				strings.HasPrefix(trimmed, "sv_rcon_minfailuretime") {
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

		// Prepend rcon_password and RCON ban settings (must be first)
		// Use default disabled values (will be overridden by customizeServerCfgGoWithRCONBans if called)
		out = append([]string{
			fmt.Sprintf(`rcon_password "%s"`, rcon),
			`ip "0.0.0.0"`,
			`sv_rcon_banpenalty 0`,
			`sv_rcon_maxfailures 0`,
			`sv_rcon_minfailures 0`,
			`sv_rcon_minfailuretime 0`,
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
sv_rcon_maxfailures 0
sv_rcon_minfailures 0
sv_rcon_minfailuretime 0

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

// customizeServerCfgGoWithRCONBans is like customizeServerCfgGo but accepts RCON ban settings.
func customizeServerCfgGoWithRCONBans(w io.Writer, user string, serverNum int, rcon, hostnamePrefix string, gamePort, tvPort int, maxPlayers int, rconMaxFailures, rconMinFailures, rconMinFailureTime int) error {
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
		// Update existing server.cfg
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

			// Remove old RCON ban settings (we'll add them fresh)
			if strings.HasPrefix(trimmed, "sv_rcon_banpenalty") ||
				strings.HasPrefix(trimmed, "sv_rcon_maxfailures") ||
				strings.HasPrefix(trimmed, "sv_rcon_minfailures") ||
				strings.HasPrefix(trimmed, "sv_rcon_minfailuretime") {
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

		// Prepend rcon_password and RCON ban settings (must be first)
		out = append([]string{
			fmt.Sprintf(`rcon_password "%s"`, rcon),
			`ip "0.0.0.0"`,
			`sv_rcon_banpenalty 0`,
			fmt.Sprintf(`sv_rcon_maxfailures %d`, rconMaxFailures),
			fmt.Sprintf(`sv_rcon_minfailures %d`, rconMinFailures),
			fmt.Sprintf(`sv_rcon_minfailuretime %d`, rconMinFailureTime),
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
		// Create new server.cfg with RCON ban settings
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
sv_rcon_maxfailures %d
sv_rcon_minfailures %d
sv_rcon_minfailuretime %d

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
`, rcon, rconMaxFailures, rconMinFailures, rconMinFailureTime, fullName, maxPlayersLine, tvPort)
		if err := os.WriteFile(serverCfg, []byte(content), 0o644); err != nil {
			return err
		}
	}

	// autoexec.cfg with startup info (unchanged)
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

	return nil
}

// storeGSLTGo writes the GSLT token to the shared cs2-config location.
func storeGSLTGo(w io.Writer, user string, gslt string) error {
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

			data, err := json.MarshalIndent(onDisk, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal database config: %w", err)
			}
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

		// Warn about default database passwords
		if cfg.ExternalDBPassword == "" || cfg.ExternalDBPassword == DefaultMatchzyDBPassword {
			fmt.Fprintf(w, "  [!] WARNING: Using default MatchZy database password!\n")
			fmt.Fprintf(w, "  [!] SECURITY: Change the database password in production!\n")
		}

		onDisk := struct {
			matchzyDBConfig
			CSMNote string `json:"__CSM_NOTE,omitempty"`
			DBMode  string `json:"__CSM_DB_MODE,omitempty"`
		}{
			matchzyDBConfig: dbCfg,
			CSMNote:         "This file is managed by CSM's install wizard. Manual edits may be overwritten.",
			DBMode:          "docker",
		}

		data, err := json.MarshalIndent(onDisk, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal database config: %w", err)
		}
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

		data, err := json.MarshalIndent(onDisk, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal MatchZy database config: %w", err)
		}
		if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
			return fmt.Errorf("failed to write MatchZy database config to %s: %w", matchzyCfgPath, err)
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
	if rootPass == DefaultMatchzyRootPassword {
		fmt.Fprintf(w, "  [!] WARNING: Using default MySQL root password!\n")
		fmt.Fprintf(w, "  [!] SECURITY: Set MATCHZY_DB_ROOT_PASSWORD environment variable for production!\n")
	}

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
		if err := exec.Command("docker", "rm", "-f", containerName).Run(); err != nil {
			fmt.Fprintf(w, "  [i] Container %q may not exist (this is fine): %v\n", containerName, err)
		}
		fmt.Fprintf(w, "  [*] FRESH_INSTALL=1: Deleting existing MatchZy volume %q (if present)\n", volumeName)
		if err := exec.Command("docker", "volume", "rm", volumeName).Run(); err != nil {
			fmt.Fprintf(w, "  [i] Volume %q may not exist (this is fine): %v\n", volumeName, err)
		}
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

	data, err = json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal database config: %w", err)
	}
	if err := os.WriteFile(matchzyCfgPath, data, 0o664); err != nil {
		return err
	}

	recreate := false
	if containerExists {
		if currentPort != strconv.Itoa(dbCfg.MySQLPort) {
			fmt.Fprintf(w, "  [*] Recreating %s to use host port %d\n", containerName, dbCfg.MySQLPort)
			if err := exec.Command("docker", "rm", "-f", containerName).Run(); err != nil {
				fmt.Fprintf(w, "  [!] Warning: Failed to remove existing container: %v\n", err)
			}
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

// --- Bootstrap helper functions (previously moved to separate files) ---

// createCS2User creates the CS2 service user if it doesn't exist.
func createCS2User(w *bytes.Buffer, user string) error {
	fmt.Fprintf(w, "  [*] Checking for user %s...\n", user)
	cmd := exec.Command("id", "-u", user)
	if err := cmd.Run(); err == nil {
		fmt.Fprintf(w, "  [✓] User %s already exists\n", user)

		// Ensure home directory ownership is correct. This is important because
		// SteamCMD will try to create ~/.steam and will fail with "Permission denied"
		// if /home/<user> is owned by root (common after partial/manual installs).
		if err := fixServerOwnership(user); err != nil {
			fmt.Fprintf(w, "  [!] Warning: Failed to fix ownership for %s: %v\n", user, err)
		}
		return nil
	}

	fmt.Fprintf(w, "  [*] Creating user %s...\n", user)
	if err := runCmdLogged(w, "useradd", "-r", "-m", "-s", "/bin/bash", user); err != nil {
		return fmt.Errorf("failed to create user %s: %w", user, err)
	}
	if err := fixServerOwnership(user); err != nil {
		fmt.Fprintf(w, "  [!] Warning: Failed to fix ownership for %s: %v\n", user, err)
	}
	fmt.Fprintf(w, "  [✓] User %s created\n", user)
	return nil
}

// installMasterViaSteamCMD installs or updates the master CS2 installation via SteamCMD.
func installMasterViaSteamCMD(ctx context.Context, w *bytes.Buffer, cfg BootstrapConfig) error {
	masterDir := filepath.Join("/home", cfg.CS2User, "master-install")
	validateStr := "on"
	if !SteamcmdShouldValidate() {
		validateStr = "off"
	}
	fmt.Fprintf(w, "  [*] Installing/updating master CS2 at %s (steamcmd validate: %s)...\n", masterDir, validateStr)

	if err := os.MkdirAll(masterDir, 0o755); err != nil {
		return fmt.Errorf("failed to create master directory: %w", err)
	}

	// Check if steamcmd exists
	if _, err := exec.LookPath("steamcmd"); err != nil {
		return fmt.Errorf("steamcmd not found in PATH. Install it with: sudo apt-get install steamcmd")
	}

	// When invoked from the TUI install wizard, CSM_BOOTSTRAP_LOG points at a
	// temp file that the UI tails for live progress. SteamCMD can take a long
	// time and prints useful progress output, so mirror its stdout/stderr into
	// that log file too (not just the in-memory buffer).
	var logFile *os.File
	if logPath := strings.TrimSpace(os.Getenv("CSM_BOOTSTRAP_LOG")); logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			defer func() {
				if cerr := logFile.Close(); cerr != nil {
					fmt.Fprintf(os.Stderr, "CSM_BOOTSTRAP_LOG close failed: %v\n", cerr)
				}
			}()
		}
	}

	// Check if the CS2 user exists and can access the directory
	homeDir := filepath.Join("/home", cfg.CS2User)
	if _, err := os.Stat(homeDir); os.IsNotExist(err) {
		return fmt.Errorf("CS2 user home directory %s does not exist. User may not have been created properly", homeDir)
	}

	// Ensure the CS2 user can write to their home directory (SteamCMD needs this
	// to create ~/.steam and other state directories).
	if err := fixServerOwnership(cfg.CS2User); err != nil {
		fmt.Fprintf(w, "  [!] Warning: Failed to fix ownership of %s: %v\n", homeDir, err)
	}

	// Ensure the CS2 user owns the master directory
	if err := exec.Command("chown", "-R", fmt.Sprintf("%s:%s", cfg.CS2User, cfg.CS2User), masterDir).Run(); err != nil {
		fmt.Fprintf(w, "  [!] Warning: Failed to set ownership of %s: %v\n", masterDir, err)
	}

	// Use -H so HOME is set to the target user's home (SteamCMD writes to ~/.steam).
	args := []string{
		"sudo", "-u", cfg.CS2User, "-H", "steamcmd",
		"+force_install_dir", masterDir,
		"+login", "anonymous",
		"+app_update", "730",
	}
	if SteamcmdShouldValidate() {
		args = append(args, "validate")
	}
	args = append(args, "+quit")
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	// Capture both stdout and stderr
	var stderrBuf bytes.Buffer
	cmd.Stdout = w
	cmd.Stderr = io.MultiWriter(w, &stderrBuf)
	if logFile != nil {
		cmd.Stdout = io.MultiWriter(w, logFile)
		cmd.Stderr = io.MultiWriter(w, &stderrBuf, logFile)
	}

	if err := cmd.Run(); err != nil {
		// Include stderr in the error message for better debugging
		stderrStr := strings.TrimSpace(stderrBuf.String())
		if stderrStr != "" {
			return fmt.Errorf("steamcmd failed: %w\nSteamCMD stderr output:\n%s", err, stderrStr)
		}
		return fmt.Errorf("steamcmd failed: %w\nCheck the log output above for details", err)
	}
	fmt.Fprintf(w, "  [✓] Master CS2 installation ready\n")
	return nil
}

// setupSteamSDKLinksGo sets up Steam SDK symlinks for the CS2 user.
func setupSteamSDKLinksGo(w *bytes.Buffer, user string) error {
	homeDir := filepath.Join("/home", user)
	sdk64Dir := filepath.Join(homeDir, ".steam", "sdk64")
	sdk32Dir := filepath.Join(homeDir, ".steam", "sdk32")

	fmt.Fprintf(w, "  [*] Setting up Steam SDK (steamclient.so) links...\n")

	// Steamworks init looks specifically for ~/.steam/sdk64/steamclient.so (and sometimes sdk32).
	// The CS2 dedicated server install does not always ship steamclient.so in its own tree;
	// it is typically provided by SteamCMD under the user's Steam data directory.
	findSteamClient := func(candidates []string) string {
		for _, p := range candidates {
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				return p
			}
		}
		return ""
	}

	linkOrCopy := func(src, dst string) error {
		_ = os.Remove(dst)
		if err := os.Symlink(src, dst); err == nil {
			return nil
		}

		// Symlinks can fail on some systems/filesystems; fall back to copying.
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, in)
		return err
	}

	src64 := findSteamClient([]string{
		filepath.Join(homeDir, ".local", "share", "Steam", "steamcmd", "linux64", "steamclient.so"),
		filepath.Join(homeDir, ".steam", "steamcmd", "linux64", "steamclient.so"),
		filepath.Join(homeDir, ".steam", "root", "steamcmd", "linux64", "steamclient.so"),
		"/usr/lib/steam/steamcmd/linux64/steamclient.so",
		"/usr/lib64/steam/steamcmd/linux64/steamclient.so",
	})
	if src64 == "" {
		fmt.Fprintf(w, "  [i] steamclient.so not found (steamcmd may not have run yet); skipping\n")
		return nil
	}

	if err := os.MkdirAll(sdk64Dir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", sdk64Dir, err)
	}
	dst64 := filepath.Join(sdk64Dir, "steamclient.so")
	if err := linkOrCopy(src64, dst64); err != nil {
		return fmt.Errorf("failed to set up %s from %s: %w", dst64, src64, err)
	}

	// Best-effort sdk32 (not always required, but cheap to provide if present).
	src32 := findSteamClient([]string{
		filepath.Join(homeDir, ".local", "share", "Steam", "steamcmd", "linux32", "steamclient.so"),
		filepath.Join(homeDir, ".steam", "steamcmd", "linux32", "steamclient.so"),
		filepath.Join(homeDir, ".steam", "root", "steamcmd", "linux32", "steamclient.so"),
		"/usr/lib/steam/steamcmd/linux32/steamclient.so",
		"/usr/lib32/steam/steamcmd/linux32/steamclient.so",
	})
	if src32 != "" {
		if err := os.MkdirAll(sdk32Dir, 0o755); err != nil {
			return fmt.Errorf("failed to create %s: %w", sdk32Dir, err)
		}
		dst32 := filepath.Join(sdk32Dir, "steamclient.so")
		if err := linkOrCopy(src32, dst32); err != nil {
			fmt.Fprintf(w, "  [!] Warning: Failed to set up %s from %s: %v\n", dst32, src32, err)
		}
	}

	fmt.Fprintf(w, "  [✓] Steam SDK ready (sdk64 steamclient.so: %s)\n", src64)
	return nil
}

// copyMasterToServerGo copies the master installation to a server instance.
func copyMasterToServerGo(ctx context.Context, w io.Writer, user string, serverNum int, freshInstall bool) error {
	masterDir := filepath.Join("/home", user, "master-install")
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum))
	serverGameDir := filepath.Join(serverDir, "game")

	if freshInstall {
		if err := os.RemoveAll(serverGameDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to clean server-%d: %w", serverNum, err)
		}
	}

	if err := os.MkdirAll(serverGameDir, 0o755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	fmt.Fprintf(w, "  [*] Copying master install to server-%d...\n", serverNum)
	// Install-like copy: allow reflink when the destination is empty/clean.
	// Fresh installs remove the directory; new servers are also typically empty.
	allowReflink := true
	if err := copyMasterGameToServerGame(ctx, w, masterDir, serverGameDir, allowReflink, false); err != nil {
		return err
	}
	fmt.Fprintf(w, "  [✓] Server-%d game files copied\n", serverNum)
	return nil
}

// overlayConfigToServerGo applies configuration overlays to a server instance.
func overlayConfigToServerGo(ctx context.Context, w io.Writer, user string, serverNum int) error {
	sharedCfgDir := filepath.Join("/home", user, "cs2-config", "game", "csgo", "cfg")
	serverCfgDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg")
	sharedAddonsDir := filepath.Join("/home", user, "cs2-config", "game", "csgo", "addons")
	serverAddonsDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "addons")

	if fi, err := os.Stat(sharedCfgDir); err != nil || !fi.IsDir() {
		fmt.Fprintf(w, "  [i] No shared config found, skipping overlay for server-%d\n", serverNum)
		// Still try to sync addons if present; config overlay is optional.
	}

	fmt.Fprintf(w, "  [*] Applying config overlay to server-%d...\n", serverNum)
	if err := os.MkdirAll(serverCfgDir, 0o755); err != nil {
		return fmt.Errorf("failed to create server cfg directory: %w", err)
	}

	if fi, err := os.Stat(sharedCfgDir); err == nil && fi.IsDir() {
		if err := runCmdLoggedContext(ctx, w, "rsync", "-a",
			sharedCfgDir+string(os.PathSeparator),
			serverCfgDir+string(os.PathSeparator),
		); err != nil {
			return fmt.Errorf("rsync config failed: %w", err)
		}
	}

	// Also sync addons (Metamod + CounterStrikeSharp + plugins) from the shared
	// cs2-config tree. The master install sync explicitly excludes csgo/addons/,
	// so without this step servers will not have Metamod/CSS installed.
	if fi, err := os.Stat(sharedAddonsDir); err == nil && fi.IsDir() {
		fmt.Fprintf(w, "  [*] Syncing addons to server-%d...\n", serverNum)
		if err := os.MkdirAll(serverAddonsDir, 0o755); err != nil {
			return fmt.Errorf("failed to create server addons directory: %w", err)
		}
		if err := runCmdLoggedContext(ctx, w, "rsync", "-a", "--delete",
			sharedAddonsDir+string(os.PathSeparator),
			serverAddonsDir+string(os.PathSeparator),
		); err != nil {
			return fmt.Errorf("rsync addons failed: %w", err)
		}
	}

	fmt.Fprintf(w, "  [✓] Overlay applied to server-%d\n", serverNum)
	return nil
}

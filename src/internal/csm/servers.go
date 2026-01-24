package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AddServers creates one or more additional CS2 server instances based on the
// existing layout. It reuses the master install, shared config and MatchZy
// setup from previous installs so users can scale up without rerunning the
// full wizard.
func AddServers(count int) (string, error) {
	return AddServersWithContext(context.Background(), count)
}

// AddServersWithContext is like AddServers but accepts a context for
// cancellation. If the context is cancelled while adding servers, any
// servers successfully created in this call are rolled back.
func AddServersWithContext(ctx context.Context, count int) (string, error) {
	if count <= 0 {
		return "", fmt.Errorf("server count must be a positive integer")
	}

	var buf bytes.Buffer
	created := 0

	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			// Best-effort rollback of any servers created during this call.
			if created > 0 {
				_, _ = RemoveServers(created)
			}
			return buf.String(), ctx.Err()
		default:
		}

		out, err := AddServerInstanceWithContext(ctx)
		if strings.TrimSpace(out) != "" {
			buf.WriteString(out)
			if !strings.HasSuffix(out, "\n") {
				buf.WriteByte('\n')
			}
			buf.WriteByte('\n')
		}
		if err == nil {
			created++
		}
		if err != nil {
			// On error, roll back any newly created servers from this call.
			if created > 0 {
				_, _ = RemoveServers(created)
			}
			return buf.String(), err
		}
	}
	return buf.String(), nil
}

// AddServerInstance creates one additional CS2 server instance based on the
// existing layout. It is used by AddServers but can also be called directly by
// CLI/TUI helpers that want to add a single server.
func AddServerInstance() (string, error) {
	return AddServerInstanceWithContext(context.Background())
}

// AddServerInstanceWithContext is like AddServerInstance but accepts a context
// for cancellation. When the context is cancelled, long-running operations
// such as rsync are terminated and the partial log plus ctx.Err() are
// returned.
func AddServerInstanceWithContext(ctx context.Context) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no existing servers found; run the install wizard first")
	}

	user := mgr.CS2User
	last := mgr.NumServers
	newIdx := last + 1

	gamePortLast, tvPortLast := detectServerPorts(user, last)
	gamePortNew := gamePortLast + 10
	tvPortNew := tvPortLast + 10

	rcon := detectRCONPassword(user)
	hostnamePrefix := detectHostnamePrefix(user)
	enableMetamod := detectMetamodEnabled(user)
	maxPlayers := detectMaxPlayers(user)
	gslt := detectGSLT(user)

	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	log("[*] Scaling up: adding server-%d for user %s", newIdx, user)

	if err := stopTmuxServerGo(&buf, user, newIdx); err != nil {
		log("  [i] Could not stop tmux session for server-%d (likely fine): %v", newIdx, err)
	}

	if err := copyMasterToServerGo(ctx, &buf, user, newIdx, false); err != nil {
		log("  [!] Copy master to server-%d failed: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}

	if err := overlayConfigToServerGo(ctx, &buf, user, newIdx); err != nil {
		log("  [!] Overlay config to server-%d failed: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}

	if err := configureMetamodGo(&buf, user, newIdx, enableMetamod); err != nil {
		log("  [!] Configure Metamod for server-%d failed: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}

	if err := customizeServerCfgGo(&buf, user, newIdx, rcon, hostnamePrefix, gamePortNew, tvPortNew, maxPlayers); err != nil {
		log("  [!] Customize server.cfg for server-%d failed: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}

	// Store GSLT token if one was detected
	if gslt != "" {
		if err := storeGSLTGo(&buf, user, newIdx, gslt); err != nil {
			log("  [!] Failed to store GSLT for server-%d: %v", newIdx, err)
		}
	}

	log("  [✓] Server-%d ready (game %d, TV %d)", newIdx, gamePortNew, tvPortNew)

	// Automatically start the new server so scale-up feels complete without
	// requiring a separate "start" action.
	if err := mgr.Start(newIdx); err != nil {
		log("  [!] Failed to start server-%d via tmux: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}
	log("  [✓] Server-%d started via tmux", newIdx)

	// Fix ownership of all server files
	log("")
	log("  [*] Fixing file ownership...")
	if err := fixServerOwnership(user); err != nil {
		log("  [!] Warning: Failed to fix ownership: %v", err)
	} else {
		log("  [✓] File ownership fixed")
	}

	return buf.String(), nil
}

// cleanupPartialServerDir best-effort removes a partially created server-N
// directory when scale-up fails mid-way (for example, due to rsync being
// cancelled). Errors are logged into the provided buffer but not returned,
// since the primary error is the original failure.
func cleanupPartialServerDir(w *bytes.Buffer, user string, serverNum int) {
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum))
	fmt.Fprintf(w, "  [*] Cleaning up partially created %s\n", serverDir)
	if err := os.RemoveAll(serverDir); err != nil {
		fmt.Fprintf(w, "  [i] os.RemoveAll failed for %s (%v), retrying with rm -rf\n", serverDir, err)
		_ = runCmdLogged(w, "rm", "-rfv", serverDir)
	}
}

// RemoveServers stops and deletes the N highest-numbered server-* directories
// (server-M, server-M-1, ...) so users can scale down without a full
// reinstall. It mirrors the naming convention used by the installer.
func RemoveServers(count int) (string, error) {
	return RemoveServersWithContext(context.Background(), count)
}

// RemoveServersWithContext is like RemoveServers but accepts a context for
// cancellation. If the context is cancelled mid-way, any servers already
// removed are not restored, but further removals are skipped.
func RemoveServersWithContext(ctx context.Context, count int) (string, error) {
	if count <= 0 {
		return "", fmt.Errorf("server count must be a positive integer")
	}

	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no servers found to remove")
	}
	if count > mgr.NumServers {
		return "", fmt.Errorf("cannot remove %d servers; only %d server(s) found", count, mgr.NumServers)
	}

	var buf bytes.Buffer
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		default:
		}

		out, err := RemoveLastServerInstance()
		if strings.TrimSpace(out) != "" {
			buf.WriteString(out)
			if !strings.HasSuffix(out, "\n") {
				buf.WriteByte('\n')
			}
			buf.WriteByte('\n')
		}
		if err != nil {
			return buf.String(), err
		}
	}
	return buf.String(), nil
}

// RemoveLastServerInstance stops and deletes the highest-numbered server-N
// directory so users can scale down their server count without a full
// reinstall. It is used by RemoveServers but can also be called directly.
func RemoveLastServerInstance() (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no servers found to remove")
	}

	user := mgr.CS2User
	last := mgr.NumServers

	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	log("[*] Scaling down: removing server-%d for user %s", last, user)

	if err := stopTmuxServerGo(&buf, user, last); err != nil {
		log("  [i] Could not stop tmux session for server-%d (likely already stopped): %v", last, err)
	}

	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", last))
	log("  [*] Deleting %s", serverDir)
	if err := os.RemoveAll(serverDir); err != nil {
		// On some filesystems RemoveAll can fail with "directory not empty"
		// for deep trees. Fall back to a best-effort rm -rf using the system
		// tools so we don't leave a half-deleted server behind.
		log("  [i] os.RemoveAll failed (%v), retrying with rm -rf", err)
		if err2 := runCmdLogged(&buf, "rm", "-rfv", serverDir); err2 != nil {
			log("  [!] Failed to delete %s: %v (rm -rf fallback also failed: %v)", serverDir, err, err2)
			return buf.String(), fmt.Errorf("failed to delete %s: %w (rm -rf fallback also failed: %v)", serverDir, err, err2)
		}
	}

	log("  [✓] server-%d removed. New server count will be detected automatically.", last)
	return buf.String(), nil
}

// ReinstallServerInstance completely rebuilds a single server from the master
// installation. This is useful when a server's game files are corrupted or
// incomplete. It will stop the server, delete its directory, copy fresh files
// from master-install, and reconfigure everything.
func ReinstallServerInstance(serverNum int) (string, error) {
	return ReinstallServerInstanceWithContext(context.Background(), serverNum)
}

// ReinstallServerInstanceWithContext is like ReinstallServerInstance but
// accepts a context for cancellation.
func ReinstallServerInstanceWithContext(ctx context.Context, serverNum int) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no existing servers found; run the install wizard first")
	}
	if serverNum < 1 || serverNum > mgr.NumServers {
		return "", fmt.Errorf("server-%d does not exist (valid range: 1-%d)", serverNum, mgr.NumServers)
	}

	user := mgr.CS2User
	gamePort, tvPort := detectServerPorts(user, serverNum)
	rcon := detectRCONPassword(user)
	hostnamePrefix := detectHostnamePrefix(user)
	enableMetamod := detectMetamodEnabled(user)
	maxPlayers := detectMaxPlayers(user)
	gslt := detectGSLT(user)
	
	// Debug output
	fmt.Printf("[DEBUG] Detected settings:\n")
	fmt.Printf("  Metamod enabled: %v\n", enableMetamod)
	fmt.Printf("  RCON password: %s\n", rcon)
	fmt.Printf("  Hostname prefix: %s\n", hostnamePrefix)
	fmt.Printf("  Max players: %d\n", maxPlayers)
	fmt.Printf("  Ports: Game=%d, TV=%d\n", gamePort, tvPort)

	var buf bytes.Buffer
	var logFile *os.File
	isCLI := true

	// When invoked from the TUI, CSM_REINSTALL_LOG is set to a temp path that
	// the UI tails in real time. Mirror all log lines into that file so the
	// user can see progress while the reinstall is running.
	if logPath := strings.TrimSpace(os.Getenv("CSM_REINSTALL_LOG")); logPath != "" {
		isCLI = false
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			logFile = f
			defer func() {
				if err := logFile.Close(); err != nil {
					fmt.Fprintf(os.Stderr, "CSM_REINSTALL_LOG close failed: %v\n", err)
				}
			}()
		}
	}

	log := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		
		// Write to buffer for return value
		buf.WriteString(line)
		
		// Write to TUI log file if set
		if logFile != nil {
			_, _ = logFile.WriteString(line)
		}
		
		// If running in CLI mode, print to stdout for real-time feedback
		if isCLI {
			fmt.Print(line)
		}
	}
	
	// For operations that write directly to the buffer (like rsync), we need
	// to create a writer that also outputs to stdout in CLI mode
	var writer io.Writer = &buf
	if isCLI {
		writer = io.MultiWriter(&buf, os.Stdout)
	}

	log("[*] Reinstalling server-%d for user %s", serverNum, user)
	log("    This will completely rebuild the server from master-install")
	log("")

	// Stop the server first
	if err := stopTmuxServerGo(&buf, user, serverNum); err != nil {
		log("  [i] Could not stop tmux session for server-%d: %v", serverNum, err)
	}

	// Delete the existing server directory to ensure clean state
	serverDir := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum))
	log("  [*] Deleting existing server-%d directory", serverNum)
	if err := os.RemoveAll(serverDir); err != nil {
		log("  [i] os.RemoveAll failed (%v), retrying with rm -rf", err)
		if err2 := runCmdLogged(&buf, "rm", "-rf", serverDir); err2 != nil {
			log("  [!] Failed to delete %s: %v", serverDir, err)
			log("  [*] This may indicate permission issues or files in use")
			return buf.String(), fmt.Errorf("failed to delete server directory %s: %w (check permissions and ensure server is stopped)", serverDir, err)
		}
	}
	log("  [✓] Existing server-%d directory removed", serverNum)
	log("")

	// Copy fresh files from master-install
	if err := copyMasterToServerGo(ctx, writer, user, serverNum, false); err != nil {
		log("  [!] Copy master to server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Overlay shared config
	if err := overlayConfigToServerGo(ctx, writer, user, serverNum); err != nil {
		log("  [!] Overlay config to server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Configure Metamod
	if err := configureMetamodGo(writer, user, serverNum, enableMetamod); err != nil {
		log("  [!] Configure Metamod for server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Delete the copied server.cfg so customizeServerCfgGo creates a fresh one
	// with all proper settings instead of trying to update the incomplete one
	// from master-install
	serverCfgPath := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg", "server.cfg")
	_ = os.Remove(serverCfgPath)

	// Customize server.cfg with ports and settings (will create fresh config)
	if err := customizeServerCfgGo(writer, user, serverNum, rcon, hostnamePrefix, gamePort, tvPort, maxPlayers); err != nil {
		log("  [!] Customize server.cfg for server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Store GSLT token if one exists
	if gslt != "" {
		if err := storeGSLTGo(writer, user, serverNum, gslt); err != nil {
			log("  [!] Failed to store GSLT for server-%d: %v", serverNum, err)
		}
	}

	log("  [✓] Server-%d reinstalled successfully (game %d, TV %d)", serverNum, gamePort, tvPort)
	log("")

	// Automatically start the reinstalled server
	if err := mgr.Start(serverNum); err != nil {
		log("  [!] Failed to start server-%d: %v", serverNum, err)
		return buf.String(), err
	}
	log("  [✓] Server-%d started", serverNum)

	// Fix ownership of all server files
	log("")
	log("  [*] Fixing file ownership...")
	if err := fixServerOwnership(user); err != nil {
		log("  [!] Warning: Failed to fix ownership: %v", err)
	} else {
		log("  [✓] File ownership fixed")
	}

	return buf.String(), nil
}

// DetectRCONPassword best-effort reads the RCON password from an existing
// server-1 config so new servers reuse the same password. Falls back to the
// default if parsing fails.
func DetectRCONPassword(user string) string {
	return detectRCONPassword(user)
}

// sharedConfigPath returns the path to the shared server.cfg in cs2-config
func sharedConfigPath(user string) string {
	return filepath.Join("/home", user, "cs2-config", "game", "csgo", "cfg", "server.cfg")
}

// detectRCONPassword is the internal implementation.
// Reads from the shared cs2-config/server.cfg (applies to all servers)
func detectRCONPassword(user string) string {
	cfg := sharedConfigPath(user)
	data, err := os.ReadFile(cfg)
	if err != nil {
		// Silently return empty string - this is expected if config doesn't exist yet
		// (e.g., during first install). No need to log as this is a best-effort read.
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.HasPrefix(line, "rcon_password") {
			continue
		}
		// Expect formats like: rcon_password "value"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			val := strings.Trim(parts[1], `"`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}

// DetectHostnamePrefix reads server-1's hostname and derives the base prefix so
// that newly added servers follow the same naming pattern. When parsing fails,
// it falls back to a neutral default.
func DetectHostnamePrefix(user string) string {
	return detectHostnamePrefix(user)
}

// detectHostnamePrefix is the internal implementation.
func detectHostnamePrefix(user string) string {
	cfg := filepath.Join("/home", user, "server-1", "game", "csgo", "cfg", "server.cfg")
	data, err := os.ReadFile(cfg)
	if err != nil {
		return "CS2 Server"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "hostname ") {
			continue
		}
		// Expect formats like:
		//   hostname "My CS2 Server #1"
		//   hostname "My CS2 Server"
		rest := strings.TrimSpace(strings.TrimPrefix(line, "hostname"))
		rest = strings.Trim(rest, `"`)
		if rest == "" {
			continue
		}
		if strings.HasSuffix(rest, " #1") {
			return strings.TrimSuffix(rest, " #1")
		}
		return rest
	}
	return "CS2 Server"
}

// DetectRCONBanSettings best-effort reads RCON ban settings from an existing server config.
// Returns (maxFailures, minFailures, minFailureTime). Defaults to (0, 0, 0) if not found.
func DetectRCONBanSettings(user string) (maxFailures, minFailures, minFailureTime int) {
	return detectRCONBanSettings(user)
}

// detectRCONBanSettings is the internal implementation.
func detectRCONBanSettings(user string) (maxFailures, minFailures, minFailureTime int) {
	cfg := filepath.Join("/home", user, "server-1", "game", "csgo", "cfg", "server.cfg")
	data, err := os.ReadFile(cfg)
	if err != nil {
		return 0, 0, 0 // Default: disabled
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "sv_rcon_maxfailures ") {
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "sv_rcon_maxfailures "))); err == nil {
				maxFailures = n
			}
		} else if strings.HasPrefix(line, "sv_rcon_minfailures ") {
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "sv_rcon_minfailures "))); err == nil {
				minFailures = n
			}
		} else if strings.HasPrefix(line, "sv_rcon_minfailuretime ") {
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "sv_rcon_minfailuretime "))); err == nil {
				minFailureTime = n
			}
		}
	}
	return maxFailures, minFailures, minFailureTime
}

// DetectMetamodEnabled inspects server-1's gameinfo.gi to see whether the
// Metamod line is present. New servers follow the same setting.
func DetectMetamodEnabled(user string) bool {
	return detectMetamodEnabled(user)
}

// detectMetamodEnabled is the internal implementation.
func detectMetamodEnabled(user string) bool {
	// Check master-install instead of server-1 to avoid chicken-and-egg problem
	// where server-1 is broken and we're trying to detect if we should fix it
	gameinfo := filepath.Join("/home", user, "master-install", "game", "csgo", "gameinfo.gi")
	data, err := os.ReadFile(gameinfo)
	if err != nil {
		// If master doesn't exist or can't be read, default to enabled
		return true
	}
	metamodEnabled := strings.Contains(string(data), "csgo/addons/metamod")
	// If not in master, still default to true (metamod should be enabled)
	if !metamodEnabled {
		return true
	}
	return metamodEnabled
}

// DetectMaxPlayers reads server-1's server.cfg and extracts the maxplayers value.
// When parsing fails, it returns 0 (which means use default).
func DetectMaxPlayers(user string) int {
	return detectMaxPlayers(user)
}

// detectMaxPlayers is the internal implementation.
// Reads from the shared cs2-config/server.cfg (applies to all servers)
func detectMaxPlayers(user string) int {
	cfg := sharedConfigPath(user)
	data, err := os.ReadFile(cfg)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		// Check for maxplayers or sv_maxplayers
		if !strings.HasPrefix(line, "maxplayers") && !strings.HasPrefix(line, "sv_maxplayers") {
			continue
		}
		// Expect formats like: maxplayers 10 or sv_maxplayers 10
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			if val, err := strconv.Atoi(parts[1]); err == nil && val > 0 {
				return val
			}
		}
	}
	return 0
}

// DetectGSLT reads the GSLT token from server-1's config file.
func DetectGSLT(user string) string {
	return detectGSLT(user)
}

// sharedGSLTPath returns the path to the shared GSLT file in cs2-config
func sharedGSLTPath(user string) string {
	return filepath.Join("/home", user, "cs2-config", "server.gslt")
}

// detectGSLT is the internal implementation.
// Reads from the shared cs2-config/server.gslt (applies to all servers)
func detectGSLT(user string) string {
	gsltFile := sharedGSLTPath(user)
	data, err := os.ReadFile(gsltFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// UpdateServerConfig regenerates server.cfg and autoexec.cfg for a single server
// without reinstalling game files. This is much faster than reinstall when you
// just need to fix config issues.
func UpdateServerConfig(serverNum int) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no existing servers found; run the install wizard first")
	}
	if serverNum < 1 || serverNum > mgr.NumServers {
		return "", fmt.Errorf("server-%d does not exist (valid range: 1-%d)", serverNum, mgr.NumServers)
	}

	user := mgr.CS2User
	gamePort, tvPort := detectServerPorts(user, serverNum)
	rcon := detectRCONPassword(user)
	hostnamePrefix := detectHostnamePrefix(user)
	maxPlayers := detectMaxPlayers(user)

	var buf bytes.Buffer
	log := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		buf.WriteString(line)
		fmt.Print(line)
	}

	log("[*] Updating config for server-%d", serverNum)
	log("")

	// Delete existing server.cfg to force fresh creation
	serverCfgPath := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg", "server.cfg")
	_ = os.Remove(serverCfgPath)

	// Regenerate configs
	var writer io.Writer = &buf
	if err := customizeServerCfgGo(writer, user, serverNum, rcon, hostnamePrefix, gamePort, tvPort, maxPlayers); err != nil {
		log("  [!] Failed to update config: %v", err)
		return buf.String(), err
	}

	log("  [✓] Config updated for server-%d", serverNum)
	log("")
	log("  Restart the server for changes to take effect:")
	log("    sudo csm restart %d", serverNum)

	return buf.String(), nil
}

// DetectServerPorts reads the autoexec.cfg for a given server and extracts the
// "Port: Game X, TV Y" line. When parsing fails, it falls back to the default
// port pattern based on the server index.
func DetectServerPorts(user string, server int) (gamePort, tvPort int) {
	return detectServerPorts(user, server)
}

// detectServerPorts is the internal implementation.
func detectServerPorts(user string, server int) (gamePort, tvPort int) {
	autoexec := filepath.Join("/home", user, fmt.Sprintf("server-%d", server), "game", "csgo", "cfg", "autoexec.cfg")
	data, err := os.ReadFile(autoexec)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "Port: Game") {
				continue
			}
			// Trim up to the "Port: Game" portion to simplify parsing.
			idx := strings.Index(line, "Port: Game")
			if idx == -1 {
				continue
			}
			segment := line[idx:]
			var gp, tv int
			if _, err := fmt.Sscanf(segment, "Port: Game %d, TV %d", &gp, &tv); err == nil && gp > 0 && tv > 0 {
				return gp, tv
			}
		}
	}

	// Fallback: derive from the conventional pattern used by the installer.
	baseGame := DefaultBaseGamePort
	baseTV := DefaultBaseTVPort
	return baseGame + (server-1)*10, baseTV + (server-1)*10
}

// UnbanIP removes an IP address from a server's banned_ip.cfg file.
// If serverNum is 0, it removes the IP from all servers.
func UnbanIP(serverNum int, ip string) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}

	var result strings.Builder
	removed := false

	if serverNum == 0 {
		// Unban from all servers
		for i := 1; i <= mgr.NumServers; i++ {
			if ok, _ := unbanIPFromServer(mgr.CS2User, i, ip); ok {
				removed = true
				fmt.Fprintf(&result, "Removed %s from server-%d\n", ip, i)
				reloadBannedIPs(mgr.CS2User, i)
			}
		}
	} else {
		// Unban from specific server
		if ok, err := unbanIPFromServer(mgr.CS2User, serverNum, ip); err != nil {
			return "", err
		} else if ok {
			removed = true
			fmt.Fprintf(&result, "Removed %s from server-%d\n", ip, serverNum)
			reloadBannedIPs(mgr.CS2User, serverNum)
		} else {
			fmt.Fprintf(&result, "IP %s not found in server-%d banned list\n", ip, serverNum)
		}
	}

	if !removed && serverNum != 0 {
		fmt.Fprintf(&result, "IP %s was not banned on server-%d\n", ip, serverNum)
	}

	return result.String(), nil
}

// UnbanAllIPs clears all IP bans from a server's banned_ip.cfg file.
// If serverNum is 0, it clears bans from all servers.
func UnbanAllIPs(serverNum int) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}

	var result strings.Builder

	if serverNum == 0 {
		// Clear all servers
		for i := 1; i <= mgr.NumServers; i++ {
			if err := clearBannedIPs(mgr.CS2User, i); err == nil {
				fmt.Fprintf(&result, "Cleared all IP bans from server-%d\n", i)
				reloadBannedIPs(mgr.CS2User, i)
			}
		}
	} else {
		// Clear specific server
		if err := clearBannedIPs(mgr.CS2User, serverNum); err != nil {
			return "", err
		}
		fmt.Fprintf(&result, "Cleared all IP bans from server-%d\n", serverNum)
		reloadBannedIPs(mgr.CS2User, serverNum)
	}

	return result.String(), nil
}

// unbanIPFromServer removes an IP from a specific server's banned_ip.cfg file.
func unbanIPFromServer(user string, serverNum int, ip string) (bool, error) {
	banFile := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg", "banned_ip.cfg")
	
	data, err := os.ReadFile(banFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // File doesn't exist, IP not banned
		}
		return false, err
	}

	lines := strings.Split(string(data), "\n")
	var newLines []string
	found := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip comments and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			newLines = append(newLines, line)
			continue
		}
		// Check if this line contains the IP
		if strings.Contains(trimmed, ip) {
			found = true
			continue // Skip this line (remove the ban)
		}
		newLines = append(newLines, line)
	}

	if !found {
		return false, nil
	}

	// Write back the file without the banned IP
	content := strings.Join(newLines, "\n")
	if err := os.WriteFile(banFile, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("failed to write banned_ip.cfg: %w", err)
	}

	return true, nil
}

// clearBannedIPs clears all IP bans from a server's banned_ip.cfg file.
func clearBannedIPs(user string, serverNum int) error {
	banFile := filepath.Join("/home", user, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg", "banned_ip.cfg")
	
	// If file doesn't exist, nothing to clear
	if _, err := os.Stat(banFile); os.IsNotExist(err) {
		return nil
	}

	// Write empty file (or just header comment)
	content := "// Banned IP addresses for RCON access\n"
	if err := os.WriteFile(banFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to clear banned_ip.cfg: %w", err)
	}

	return nil
}

// reloadBannedIPs sends a command to reload banned_ip.cfg in a running server.
func reloadBannedIPs(user string, serverNum int) {
	session := fmt.Sprintf("cs2-%d", serverNum)
	// Try to send the exec command to reload banned IPs
	_ = exec.Command("su", "-", user, "-c", fmt.Sprintf("tmux send-keys -t %s 'exec banned_ip.cfg' C-m", session)).Run()
}

// ListBannedIPs returns a list of banned IP addresses for a server.
func ListBannedIPs(serverNum int) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}

	if serverNum < 1 || serverNum > mgr.NumServers {
		return "", fmt.Errorf("server-%d not found (only %d server(s) detected)", serverNum, mgr.NumServers)
	}

	banFile := filepath.Join("/home", mgr.CS2User, fmt.Sprintf("server-%d", serverNum), "game", "csgo", "cfg", "banned_ip.cfg")
	
	data, err := os.ReadFile(banFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("No banned IPs for server-%d (banned_ip.cfg not found)", serverNum), nil
		}
		return "", fmt.Errorf("failed to read banned_ip.cfg: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var bannedIPs []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Skip comments and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Extract IP from lines like "addip 0 172.19.0.3"
		if strings.HasPrefix(trimmed, "addip") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 3 {
				bannedIPs = append(bannedIPs, parts[2])
			}
		}
	}

	if len(bannedIPs) == 0 {
		return fmt.Sprintf("No banned IPs for server-%d", serverNum), nil
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Banned IPs for server-%d:\n", serverNum)
	for _, ip := range bannedIPs {
		fmt.Fprintf(&result, "  %s\n", ip)
	}

	return result.String(), nil
}

package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
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

	var buf bytes.Buffer
	var logFile *os.File

	// When invoked from the TUI, CSM_REINSTALL_LOG is set to a temp path that
	// the UI tails in real time. Mirror all log lines into that file so the
	// user can see progress while the reinstall is running.
	if logPath := strings.TrimSpace(os.Getenv("CSM_REINSTALL_LOG")); logPath != "" {
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
			return buf.String(), fmt.Errorf("failed to delete %s: %w", serverDir, err)
		}
	}
	log("  [✓] Existing server-%d directory removed", serverNum)
	log("")

	// Copy fresh files from master-install
	if err := copyMasterToServerGo(ctx, &buf, user, serverNum, false); err != nil {
		log("  [!] Copy master to server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Overlay shared config
	if err := overlayConfigToServerGo(ctx, &buf, user, serverNum); err != nil {
		log("  [!] Overlay config to server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Configure Metamod
	if err := configureMetamodGo(&buf, user, serverNum, enableMetamod); err != nil {
		log("  [!] Configure Metamod for server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Customize server.cfg with ports and settings
	if err := customizeServerCfgGo(&buf, user, serverNum, rcon, hostnamePrefix, gamePort, tvPort, maxPlayers); err != nil {
		log("  [!] Customize server.cfg for server-%d failed: %v", serverNum, err)
		return buf.String(), err
	}

	// Store GSLT token if one exists
	if gslt != "" {
		if err := storeGSLTGo(&buf, user, serverNum, gslt); err != nil {
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

// DetectMetamodEnabled inspects server-1's gameinfo.gi to see whether the
// Metamod line is present. New servers follow the same setting.
func DetectMetamodEnabled(user string) bool {
	return detectMetamodEnabled(user)
}

// detectMetamodEnabled is the internal implementation.
func detectMetamodEnabled(user string) bool {
	gameinfo := filepath.Join("/home", user, "server-1", "game", "csgo", "gameinfo.gi")
	data, err := os.ReadFile(gameinfo)
	if err != nil {
		return true
	}
	return strings.Contains(string(data), "csgo/addons/metamod")
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

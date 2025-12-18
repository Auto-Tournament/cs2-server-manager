package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	enableMetamod := detectMetamodEnabled(user)

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

	if err := customizeServerCfgGo(&buf, user, newIdx, rcon, gamePortNew, tvPortNew); err != nil {
		log("  [!] Customize server.cfg for server-%d failed: %v", newIdx, err)
		cleanupPartialServerDir(&buf, user, newIdx)
		return buf.String(), err
	}

	log("  [✓] Server-%d ready (game %d, TV %d)", newIdx, gamePortNew, tvPortNew)

	// As a final safety net, ensure the CS2 user owns everything under its home
	// directory so the newly created server files are writable by that user.
	homeDir := filepath.Join("/home", user)
	log("  [i] Ensuring ownership of %s for user %s", homeDir, user)
	_ = exec.Command("chown", "-R", fmt.Sprintf("%s:%s", user, user), homeDir).Run()

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

// detectRCONPassword best-effort reads the RCON password from an existing
// server-1 config so new servers reuse the same password. Falls back to the
// default if parsing fails.
func detectRCONPassword(user string) string {
	cfg := filepath.Join("/home", user, "server-1", "game", "csgo", "cfg", "server.cfg")
	data, err := os.ReadFile(cfg)
	if err != nil {
		return "changeme"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
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
	return "changeme"
}

// detectMetamodEnabled inspects server-1's gameinfo.gi to see whether the
// Metamod line is present. New servers follow the same setting.
func detectMetamodEnabled(user string) bool {
	gameinfo := filepath.Join("/home", user, "server-1", "game", "csgo", "gameinfo.gi")
	data, err := os.ReadFile(gameinfo)
	if err != nil {
		return true
	}
	return strings.Contains(string(data), "csgo/addons/metamod")
}

// detectServerPorts reads the autoexec.cfg for a given server and extracts the
// "Port: Game X, TV Y" line. When parsing fails, it falls back to the default
// port pattern based on the server index.
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

package csm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AddServerInstance creates one additional CS2 server instance based on the
// existing layout. It reuses the master install, shared config and MatchZy
// setup from previous installs so users can scale up without rerunning the
// full wizard.
func AddServerInstance() (string, error) {
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

	if err := copyMasterToServerGo(&buf, user, newIdx, false); err != nil {
		log("  [!] Copy master to server-%d failed: %v", newIdx, err)
		return buf.String(), err
	}

	if err := overlayConfigToServerGo(&buf, user, newIdx); err != nil {
		log("  [!] Overlay config to server-%d failed: %v", newIdx, err)
		return buf.String(), err
	}

	if err := configureMetamodGo(&buf, user, newIdx, enableMetamod); err != nil {
		log("  [!] Configure Metamod for server-%d failed: %v", newIdx, err)
		return buf.String(), err
	}

	if err := customizeServerCfgGo(&buf, user, newIdx, rcon, gamePortNew, tvPortNew); err != nil {
		log("  [!] Customize server.cfg for server-%d failed: %v", newIdx, err)
		return buf.String(), err
	}

	log("  [✓] Server-%d ready (game %d, TV %d)", newIdx, gamePortNew, tvPortNew)

	return buf.String(), nil
}

// RemoveLastServerInstance stops and deletes the highest-numbered server-N
// directory so users can scale down their server count without a full
// reinstall. It mirrors the naming convention used by the installer.
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
		log("  [!] Failed to delete %s: %v", serverDir, err)
		return buf.String(), err
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
		return "ntlan2025"
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
	return "ntlan2025"
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
	baseGame := 27015
	baseTV := 27020
	return baseGame + (server-1)*10, baseTV + (server-1)*10
}



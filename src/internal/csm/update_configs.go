package csm

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// UpdateServerConfigsConfig contains configuration for updating server configs.
type UpdateServerConfigsConfig struct {
	CS2User            string
	RCONPassword       string
	MaxPlayers         int    // 0 means don't change
	GSLT               string // empty means don't change
	HostnamePrefix     string // empty means don't change
	RCONMaxFailures    int    // 0 means disabled, -1 means don't change
	RCONMinFailures    int    // 0 means disabled, -1 means don't change
	RCONMinFailureTime int    // 0 means disabled, -1 means don't change
}

// UpdateServerConfigs updates server configurations (RCON password, maxplayers, GSLT)
// for all servers without requiring a full reinstall.
func UpdateServerConfigs(cfg UpdateServerConfigsConfig) (string, error) {
	return UpdateServerConfigsWithContext(context.Background(), cfg)
}

// UpdateServerConfigsWithContext is like UpdateServerConfigs but accepts a context for cancellation.
func UpdateServerConfigsWithContext(ctx context.Context, cfg UpdateServerConfigsConfig) (string, error) {
	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no CS2 servers found; run the install wizard first")
	}

	user := cfg.CS2User
	if user == "" {
		user = mgr.CS2User
	}

	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !strings.HasSuffix(format, "\n") {
			buf.WriteByte('\n')
		}
	}

	log("[*] Updating server configurations for %d server(s)...", mgr.NumServers)
	log("This will:")
	log("  • Stop all servers")
	log("  • Update server.cfg files (RCON password, maxplayers)")
	log("  • Update GSLT tokens")
	log("  • Restart all servers")
	log("")

	// Stop all servers first so config changes take effect on restart
	log("Stopping all servers...")
	if err := mgr.StopAll(); err != nil {
		log("  [i] Some servers may not have been running: %v", err)
	}
	log("All servers stopped.")
	log("")

	// Detect current values from server-1 if not provided
	rcon := cfg.RCONPassword
	if rcon == "" {
		rcon = detectRCONPassword(user)
	}
	// Validate RCON password is set
	if rcon == "" {
		return "", fmt.Errorf("RCON password is required")
	}

	maxPlayers := cfg.MaxPlayers
	if maxPlayers == 0 {
		maxPlayers = detectMaxPlayers(user)
	}

	gslt := cfg.GSLT
	if gslt == "" {
		gslt = detectGSLT(user)
	}

	hostnamePrefix := cfg.HostnamePrefix
	if hostnamePrefix == "" {
		hostnamePrefix = detectHostnamePrefix(user)
	}

	// RCON ban settings (-1 means don't change, use current values)
	rconMaxFailures := cfg.RCONMaxFailures
	if rconMaxFailures == -1 {
		rconMaxFailures, _, _ = detectRCONBanSettings(user)
	}
	rconMinFailures := cfg.RCONMinFailures
	if rconMinFailures == -1 {
		_, rconMinFailures, _ = detectRCONBanSettings(user)
	}
	rconMinFailureTime := cfg.RCONMinFailureTime
	if rconMinFailureTime == -1 {
		_, _, rconMinFailureTime = detectRCONBanSettings(user)
	}

	// Update each server
	for i := 1; i <= mgr.NumServers; i++ {
		select {
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		default:
		}

		gamePort, tvPort := detectServerPorts(user, i)

		log("[%d/%d] Updating server-%d...", i, mgr.NumServers, i)

		// Update server.cfg with new RCON password, maxplayers, hostname, and RCON ban settings
		if err := customizeServerCfgGoWithRCONBans(&buf, user, i, rcon, hostnamePrefix, gamePort, tvPort, maxPlayers, rconMaxFailures, rconMinFailures, rconMinFailureTime); err != nil {
			log("  [!] Failed to update server.cfg for server-%d: %v", i, err)
			continue
		}

		// Update GSLT if provided or if we detected one
		if cfg.GSLT != "" || (cfg.GSLT == "" && gslt != "") {
			if err := storeGSLTGo(&buf, user, gslt); err != nil {
				log("  [!] Failed to update GSLT for server-%d: %v", i, err)
			}
		}

		log("  [✓] Server-%d config updated", i)
		log("")
	}

	log("=== Config Update Complete ===")
	log("RCON password        : %s", rcon)
	if maxPlayers > 0 {
		log("Max players          : %d", maxPlayers)
	}
	if hostnamePrefix != "" {
		log("Hostname prefix      : %s", hostnamePrefix)
	}
	if gslt != "" {
		log("GSLT                 : (configured)")
	}
	log("RCON ban settings    : max=%d, min=%d, time=%d", rconMaxFailures, rconMinFailures, rconMinFailureTime)
	if rconMaxFailures == 0 {
		log("  (RCON bans disabled)")
	}
	log("")

	// Restart all servers so the new configs take effect
	log("Restarting all servers...")
	if err := mgr.StartAll(); err != nil {
		log("Error starting servers: %v", err)
		return buf.String(), err
	}
	log("[OK] All servers restarted with updated configurations.")

	return buf.String(), nil
}

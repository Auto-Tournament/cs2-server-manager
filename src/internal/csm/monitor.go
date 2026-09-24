package csm

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Update markers the monitor looks for in each server's tmux log.
const (
	// Legacy AutoUpdater plugin: the server shuts itself down.
	autoUpdaterShutdownMarker = "plugin:AutoUpdater Shutting the server down due to the new game update"
	// Auto Tournament CS2 plugin (MatchZy): printed in warn_only mode, the
	// server keeps running.
	matchzyUpdateAvailableMarker = "[MATCHZY_UPDATE_AVAILABLE] required_version="
)

// RunAutoUpdateMonitor checks every server's log for a pending CS2 update and
// applies it where that is safe (see auto_update.go):
//
//   - a stopped server is updated and started, as before;
//   - a running server is updated only once it has been idle (no players,
//     no match loaded) for the grace period, one server at a time;
//   - nothing is restarted while updates are on hold — either because the
//     host set `csm updates hold on`, or because the Auto Tournament platform
//     says a tournament is running, or because the platform could not be
//     reached and csm therefore cannot tell (platform_hold.go).
//
// Decisions are logged to auto_update_monitor.log.
func RunAutoUpdateMonitor() error {
	var buf bytes.Buffer
	log := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
		if !bytes.HasSuffix([]byte(format), []byte("\n")) {
			buf.WriteByte('\n')
		}
	}

	log("=== CS2 Auto-Update Monitor (Go) ===")
	log("Time: %s", time.Now().Format(time.RFC3339))

	// The monitor is intended to be run as root (typically via root's cron)
	// because it ultimately shells out to SteamCMD and rsync in the same way
	// as the interactive wizard / CLI update-game flow. When invoked without
	// root privileges, return a clear error instead of propagating a bare
	// "exit status 1".
	if os.Geteuid() != 0 {
		log("RunAutoUpdateMonitor must be run as root (use sudo or the install-monitor-cron helper).")
		return writeMonitorLog(buf.String(), fmt.Errorf("monitor must be run as root (use sudo)"))
	}

	unlock, err := lockAutoUpdate()
	if err != nil {
		log("Skipping this cycle: %v", err)
		return writeMonitorLog(buf.String(), nil)
	}
	defer unlock()

	mgr, err := NewTmuxManager()
	if err != nil {
		log("Failed to initialize tmux manager: %v", err)
		return writeMonitorLog(buf.String(), err)
	}

	if mgr.NumServers <= 0 {
		log("No CS2 servers found for user %s (no /home/%s/server-* directories). Skipping update cycle.", mgr.CS2User, mgr.CS2User)
		return writeMonitorLog(buf.String(), nil)
	}

	ctx := context.Background()

	settings, err := LoadAutoUpdateSettings()
	hold := UpdateHold{}
	if err != nil {
		// Fail safe: an unreadable settings file must not lift a hold.
		log("Could not read auto-update settings (%v); treating updates as on hold.", err)
		hold = UpdateHold{On: true, Source: HoldSourceManual,
			Reason: fmt.Sprintf("the settings file could not be read: %v", err)}
	} else {
		// One question per cycle, not one per server: the answer is about the
		// tournament, not about any single server, and a cycle that asked
		// repeatedly would hammer the platform for the same answer.
		hold = ResolveUpdateHold(ctx, settings)
	}
	grace := settings.IdleGrace()
	log("Detected %d CS2 servers for user %s (hold: %s, idle grace: %s)", mgr.NumServers, mgr.CS2User, hold.Describe(), grace)

	state := loadAutoUpdateState()
	saveState := func() {
		if err := state.save(); err != nil {
			log("Failed to save auto-update state: %v", err)
		}
	}

	for i := 1; i <= mgr.NumServers; i++ {
		logPath := mgr.ServerLogPath(i)
		if strings.TrimSpace(logPath) == "" {
			log("Server-%d: no tmux log path available; skipping.", i)
			continue
		}
		st := state.server(i)

		marker, logSize, err := logHasMarkerAfter(logPath, st.LogOffset, autoUpdaterShutdownMarker, matchzyUpdateAvailableMarker)
		if err != nil {
			if !os.IsNotExist(err) {
				log("Server-%d: failed to read tmux log %s: %v", i, logPath, err)
			}
			continue
		}
		if marker == "" {
			if st.IdleSince != 0 {
				st.IdleSince = 0
				saveState()
			}
			continue
		}
		log("Server-%d: CS2 update available (marker in %s).", i, logPath)

		if hold.On {
			log("Server-%d: not restarting: updates are on hold (%s: %s). "+
				"Run `sudo csm update-server %d` to update it now, or see `csm updates status`.",
				i, hold.Source, hold.Reason, i)
			continue
		}
		if st.LastUpdate > 0 {
			if since := time.Since(time.Unix(st.LastUpdate, 0)); since < autoUpdateCooldown {
				log("Server-%d: updated %s ago; waiting for the %s cooldown.", i, since.Round(time.Second), autoUpdateCooldown)
				continue
			}
		}

		running := mgr.IsRunning(i)
		gamePort, _ := detectServerPorts(mgr.CS2User, i)
		addr := fmt.Sprintf("127.0.0.1:%d", gamePort)
		password := serverRCONPassword(mgr.CS2User, i)

		if running {
			// Ready Up knows better than RCON whether a match is live; an
			// explicit update_safe=false always wins (FLEET.md §18.3).
			ru := ProbeReadyUp(ctx, http.DefaultClient, FleetTarget{Server: i, Dir: mgr.serverDir(i), GamePort: gamePort, Running: true})
			if safe, known := ru.UpdateSafe(); known && !safe {
				if st.IdleSince != 0 {
					st.IdleSince = 0
					saveState()
				}
				log("Server-%d: not updating: Ready Up reports a match in progress on %s (update_safe=false).", i, describeBusyServer(ru))
				continue
			}
			probe := probeServerIdle(addr, password)
			var idleSince time.Time
			if st.IdleSince > 0 {
				idleSince = time.Unix(st.IdleSince, 0)
			}
			d := decideAutoUpdate(probe, hold, idleSince, time.Now(), grace)
			if d.Idle {
				st.IdleSince = d.IdleSince.Unix()
			} else {
				st.IdleSince = 0
			}
			saveState()
			if !d.Update {
				log("Server-%d: not updating yet: %s.", i, d.Reason)
				continue
			}
			log("Server-%d: %s; updating now.", i, d.Reason)
		} else {
			log("Server-%d: server is stopped; updating.", i)
		}

		// Everything logged so far has been handled; markers printed after
		// the restart count as a new update.
		st.LogOffset = logSize
		st.LastUpdate = time.Now().Unix()
		st.IdleSince = 0
		saveState()

		if running {
			err = updateIdleServer(ctx, log, i, addr, password)
		} else {
			var out string
			out, err = UpdateServerWithContext(ctx, i)
			if out != "" {
				log("%s", out)
			}
		}
		if err != nil {
			log("Server-%d: automatic update failed: %v", i, err)
			continue
		}
		log("Server-%d: automatic update done.", i)
	}

	log("Monitor cycle complete.")
	return writeMonitorLog(buf.String(), nil)
}

// InstallAutoUpdateCron installs a root cron entry that periodically runs
// `csm monitor`. The optional interval string can override the default */5.
func InstallAutoUpdateCron(interval string) (string, error) {
	return InstallAutoUpdateCronWithContext(context.Background(), interval)
}

// InstallAutoUpdateCronWithContext is like InstallAutoUpdateCron but accepts a
// context. While installing a cron job is typically fast, this allows TUI
// callers to cancel before or during the underlying shell command if needed.
func InstallAutoUpdateCronWithContext(ctx context.Context, interval string) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("install-monitor-cron must be run as root (use sudo)")
	}
	if interval == "" {
		interval = "*/5"
	}

	// Basic safety validation for the cron interval to avoid injecting arbitrary
	// shell content into the constructed crontab entry. We intentionally accept
	// only digits and simple cron operators (*/,-).
	var cronIntervalRe = regexp.MustCompile(`^[0-9*/,\-]+$`)
	if !cronIntervalRe.MatchString(interval) {
		return "", fmt.Errorf("invalid cron interval %q; allowed characters are digits, '*', '/', ',', '-'", interval)
	}

	// Determine which csm binary to use in the cron entry. Prefer an explicit
	// override, then whatever is on PATH, and finally fall back to the common
	// /usr/local/bin/csm location.
	binPath := getenvDefault("CSM_BIN_PATH", "")
	if binPath == "" {
		if p, err := exec.LookPath("csm"); err == nil {
			binPath = p
		} else {
			binPath = "/usr/local/bin/csm"
		}
	}

	entry := fmt.Sprintf("%s * * * * %s monitor >/dev/null 2>&1", interval, binPath)

	// Merge with existing crontab, removing any previous cs2_auto_update_monitor lines.
	cmd := exec.CommandContext(ctx, "bash", "-lc",
		fmt.Sprintf("(crontab -l 2>/dev/null | grep -v 'csm monitor' || true; echo '%s') | crontab -", entry))
	if out, err := cmd.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("failed to install cron entry: %w", err)
	}

	return fmt.Sprintf("Installed auto-update cronjob: %s\n", entry), nil
}

// RemoveAutoUpdateCron removes the auto-update monitor cron job from root's crontab.
func RemoveAutoUpdateCron() (string, error) {
	return RemoveAutoUpdateCronWithContext(context.Background())
}

// RemoveAutoUpdateCronWithContext is like RemoveAutoUpdateCron but accepts a
// context for cancellation support.
func RemoveAutoUpdateCronWithContext(ctx context.Context) (string, error) {
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("remove-monitor-cron must be run as root (use sudo)")
	}

	// Remove any lines containing 'csm monitor' from root's crontab.
	cmd := exec.CommandContext(ctx, "bash", "-lc",
		"(crontab -l 2>/dev/null | grep -v 'csm monitor' || true) | crontab -")
	if out, err := cmd.CombinedOutput(); err != nil {
		return string(out), fmt.Errorf("failed to remove cron entry: %w", err)
	}

	// Also clean up the monitor's per-server state (idle tracking, handled
	// log offsets) and the state files older versions kept in /tmp.
	_ = os.Remove(autoUpdateStatePath())
	mgr, err := NewTmuxManager()
	if err == nil && mgr.NumServers > 0 {
		for i := 1; i <= mgr.NumServers; i++ {
			_ = os.Remove(fmt.Sprintf("/tmp/cs2_auto_update_server_%s_%d", mgr.CS2User, i))
		}
	}

	return "Removed auto-update monitor cronjob\n", nil
}

func writeMonitorLog(content string, err error) error {
	AppendLog("auto_update_monitor.log", content)
	return err
}

package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RunAutoUpdateMonitor implements a simplified Go-native auto-update monitor.
// It is intentionally conservative: it only attempts an update when all
// servers are stopped and logs its decisions to /var/log/cs2_auto_update_monitor.log.
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

	mgr, err := NewTmuxManager()
	if err != nil {
		log("Failed to initialize tmux manager: %v", err)
		return writeMonitorLog(buf.String(), err)
	}

	if mgr.NumServers <= 0 {
		log("No CS2 servers found for user %s (no /home/%s/server-* directories). Skipping update cycle.", mgr.CS2User, mgr.CS2User)
		return writeMonitorLog(buf.String(), nil)
	}

	log("Detected %d CS2 servers for user %s", mgr.NumServers, mgr.CS2User)

	// Step 1: Inspect each server log for update markers.
	// - Legacy workflow: server shuts down with an AutoUpdater shutdown marker.
	// - MAT workflow (default): MatchZy Enhanced emits an "available" marker in warn-only mode.
	//
	// This monitor only runs the update when the tmux session is NOT running.
	// If we see the "available" marker while the session is running, we log a
	// targeted hint (stop the server or switch MatchZy to restart mode).
	const shutdownMarker = "plugin:AutoUpdater Shutting the server down due to the new game update"
	const matchzyAvailableMarker = "[MATCHZY_UPDATE_AVAILABLE] required_version="

	for i := 1; i <= mgr.NumServers; i++ {
		logPath := mgr.ServerLogPath(i)
		if strings.TrimSpace(logPath) == "" {
			log("Server-%d: no tmux log path available; skipping.", i)
			continue
		}

		session := mgr.sessionName(i)
		cmd := mgr.runAsCS2User("tmux has-session -t " + session)
		if err := cmd.Run(); err == nil {
			// Session still running. In warn-only mode MatchZy will not quit the server,
			// so the shutdown marker never appears. If we detect the MatchZy marker
			// while running, provide actionable guidance.
			foundAvailable, err := tailContains(logPath, matchzyAvailableMarker, 64*1024)
			if err != nil {
				log("Server-%d: failed to read tmux log %s: %v", i, logPath, err)
				continue
			}
			if foundAvailable {
				log("Server-%d: MatchZy update marker found, but server is still running. (Default MAT behavior is warn-only.)", i)
				log("  - To proceed automatically: set matchzy_safeautoupdater_action restart (MatchZy will quit once idle/postgame).")
				log("  - To proceed manually: stop server-%d, then rerun: sudo csm monitor", i)
			}
			continue
		}

		foundShutdown, err := tailContains(logPath, shutdownMarker, 64*1024)
		if err != nil {
			log("Server-%d: failed to read tmux log %s: %v", i, logPath, err)
			continue
		}
		foundAvailable, err := tailContains(logPath, matchzyAvailableMarker, 64*1024)
		if err != nil {
			log("Server-%d: failed to read tmux log %s: %v", i, logPath, err)
			continue
		}
		if !foundShutdown && !foundAvailable {
			continue
		}

		if foundShutdown {
			log("Server-%d: AutoUpdater shutdown marker found in tmux log (%s).", i, logPath)
		} else {
			log("Server-%d: MatchZy update marker found in tmux log (%s).", i, logPath)
		}

		info, err := os.Stat(logPath)
		if err != nil {
			log("Server-%d: failed to stat tmux log %s: %v", i, logPath, err)
			continue
		}
		eventUnix := info.ModTime().Unix()

		stateFile := fmt.Sprintf("/tmp/cs2_auto_update_server_%s_%d", mgr.CS2User, i)

		// Step 2: Apply a per-server cooldown (so we don't spam updates if
		// AutoUpdater bounces the server repeatedly) and ensure we only react
		// to log entries that are newer than the last processed event.
		should, reason, err := shouldProcessUpdate(stateFile, eventUnix)
		if err != nil {
			log("Server-%d: failed to evaluate auto-update cooldown state: %v", i, err)
			continue
		}
		if !should {
			log("Server-%d: %s", i, reason)
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		log("Server-%d: proceeding with automated update via UpdateServerWithContext()...", i)
		out, err := UpdateServerWithContext(ctx, i)
		if out != "" {
			log("%s", out)
		}
		if err != nil {
			log("Server-%d: UpdateServerWithContext failed: %v", i, err)
			continue
		}

		if err := markUpdateProcessed(stateFile, log); err != nil {
			log("Server-%d: failed to record auto-update state: %v", i, err)
		}
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

	// Also clean up any state files (per-server state files from the monitor)
	mgr, err := NewTmuxManager()
	if err == nil && mgr.NumServers > 0 {
		for i := 1; i <= mgr.NumServers; i++ {
			stateFile := fmt.Sprintf("/tmp/cs2_auto_update_server_%s_%d", mgr.CS2User, i)
			_ = os.Remove(stateFile) // Ignore errors if file doesn't exist
		}
	}

	return "Removed auto-update monitor cronjob\n", nil
}

func writeMonitorLog(content string, err error) error {
	AppendLog("auto_update_monitor.log", content)
	return err
}

// tailContains checks whether the last up-to-maxBytes contents of path contain
// the given substring. It avoids reading the entire file when logs grow large.
func tailContains(path, substr string, maxBytes int64) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, err
	}

	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return false, err
	}

	buf := make([]byte, size-start)
	if _, err := f.Read(buf); err != nil {
		return false, err
	}

	return strings.Contains(string(buf), substr), nil
}

// shouldProcessUpdate enforces a simple cooldown based on a timestamp file on
// disk so the monitor does not run updates too frequently (for example, if
// AutoUpdater restarts servers multiple times in quick succession). The
// eventUnix argument represents the timestamp of the triggering log entry; if
// it is not newer than the last processed timestamp, the update is skipped.
func shouldProcessUpdate(stateFile string, eventUnix int64) (bool, string, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		// No state file: we should process the update.
		return true, "", nil
	}
	str := strings.TrimSpace(string(data))
	if str == "" {
		return true, "", nil
	}
	last, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return true, "", nil
	}

	now := time.Now().Unix()
	diff := now - last
	if eventUnix > 0 && eventUnix <= last {
		return false, "No new AutoUpdater shutdown detected since last processed update; skipping", nil
	}
	if diff > 3600 {
		return true, "", nil
	}
	return false, fmt.Sprintf("Update already processed recently (%ds ago), skipping", diff), nil
}

// markUpdateProcessed writes the current timestamp into the state file so
// subsequent monitor runs can enforce a cooldown.
func markUpdateProcessed(stateFile string, logf func(string, ...any)) error {
	now := time.Now().Unix()
	if err := os.WriteFile(stateFile, []byte(fmt.Sprintf("%d\n", now)), 0o644); err != nil {
		return err
	}
	logf("Marked update as processed at %s", time.Unix(now, 0).Format(time.RFC3339))
	return nil
}

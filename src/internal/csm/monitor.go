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

	// Step 1: For each server, inspect the tmux log for MatchZy auto-update
	// markers. While the session is running we only record that an update is
	// pending; once the server is stopped and a shutdown marker is present we
	// run the actual game update for that specific server.
	const (
		matchzyAvailableMarker = "[MATCHZY_UPDATE_AVAILABLE]"
		matchzyShutdownMarker  = "[MATCHZY_UPDATE_SHUTDOWN]"
	)

	for i := 1; i <= mgr.NumServers; i++ {
		session := mgr.sessionName(i)
		cmd := mgr.runAsCS2User("tmux has-session -t " + session)
		running := cmd.Run() == nil

		logPath := mgr.ServerLogPath(i)
		if strings.TrimSpace(logPath) == "" {
			log("Server-%d: no tmux log path available; skipping.", i)
			continue
		}

		// While the server is running, only record that an update is pending
		// so operators and dashboards can see why a restart will happen later.
		if running {
			if found, version, err := findMatchzyUpdateMarker(logPath, matchzyAvailableMarker, 64*1024); err == nil && found {
				if version != "" {
					log("Server-%d: MatchZy reports a pending CS2 update (required_version=%s); waiting for a safe shutdown before applying.", i, version)
				} else {
					log("Server-%d: MatchZy reports a pending CS2 update; waiting for a safe shutdown before applying.", i)
				}
			}
			// Do not attempt updates while matches are live; MatchZy will
			// trigger a clean shutdown once it is safe.
			continue
		}

		// When the server is stopped, look for the MatchZy shutdown marker to
		// distinguish intentional update restarts from crashes.
		found, version, err := findMatchzyUpdateMarker(logPath, matchzyShutdownMarker, 64*1024)
		if err != nil {
			log("Server-%d: failed to read tmux log %s: %v", i, logPath, err)
			continue
		}
		if !found {
			continue
		}

		if version != "" {
			log("Server-%d: MatchZy shutdown marker found in tmux log (%s), required_version=%s.", i, logPath, version)
		} else {
			log("Server-%d: MatchZy shutdown marker found in tmux log (%s).", i, logPath)
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

func writeMonitorLog(content string, err error) error {
	AppendLog("auto_update_monitor.log", content)
	return err
}

// findMatchzyUpdateMarker scans up to maxBytes from the end of the given log
// file looking for the last occurrence of a MatchZy auto-update marker line
// and, when found, attempts to parse the required_version=<number> token.
// It returns whether the marker was found at all, the parsed version string
// (which may be empty on parse failure) and any I/O error encountered.
func findMatchzyUpdateMarker(path, marker string, maxBytes int64) (bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return false, "", err
	}

	size := info.Size()
	start := int64(0)
	if size > maxBytes {
		start = size - maxBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return false, "", err
	}

	buf := make([]byte, size-start)
	if _, err := f.Read(buf); err != nil {
		return false, "", err
	}

	text := string(buf)
	if !strings.Contains(text, marker) {
		return false, "", nil
	}

	lines := strings.Split(text, "\n")
	re := regexp.MustCompile(`\[MATCHZY_UPDATE_(AVAILABLE|SHUTDOWN)\]\s+required_version=(\d+)`)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.Contains(line, marker) {
			continue
		}
		if m := re.FindStringSubmatch(line); len(m) == 3 {
			return true, m[2], nil
		}
	}

	// Marker was present in the tail, but we couldn't parse a version token.
	return true, "", nil
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

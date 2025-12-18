package csm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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

	// Simple heuristic: only run an update if no cs2-* tmux sessions exist.
	out, listErr := mgr.ListSessions()
	if listErr != nil {
		log("Failed to list tmux sessions; skipping update this cycle: %v", listErr)
		return writeMonitorLog(buf.String(), listErr)
	}
	if out != "" {
		log("Detected running tmux sessions; skipping update this cycle.")
		return writeMonitorLog(buf.String(), nil)
	}

	log("All servers appear to be stopped; running UpdateGame()...")
	if out, err := UpdateGame(); out != "" || err != nil {
		if out != "" {
			log("%s", out)
		}
		if err != nil {
			log("UpdateGame failed: %v", err)
			return writeMonitorLog(buf.String(), err)
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

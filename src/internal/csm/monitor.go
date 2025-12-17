package csm

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
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
	out, _ := mgr.ListSessions()
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
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("install-monitor-cron must be run as root (use sudo)")
	}
	if interval == "" {
		interval = "*/5"
	}

	entry := fmt.Sprintf("%s * * * * /usr/local/bin/csm monitor >/dev/null 2>&1", interval)

	// Merge with existing crontab, removing any previous cs2_auto_update_monitor lines.
	cmd := exec.Command("bash", "-lc",
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

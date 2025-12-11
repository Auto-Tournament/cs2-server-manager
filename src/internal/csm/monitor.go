package csm

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	monitorStateFile = "/tmp/cs2_auto_update_monitor.state"
	monitorLogFile   = "/var/log/cs2_auto_update_monitor.log"

	autoUpdaterMsg = "plugin:AutoUpdater Shutting the server down due to the new game update"
)

// monitorLogger writes timestamped log lines both to stdout and to the
// monitor log file, roughly matching the original shell script behaviour.
type monitorLogger struct {
	f *os.File
}

func newMonitorLogger() *monitorLogger {
	f, _ := os.OpenFile(monitorLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) // best effort
	return &monitorLogger{f: f}
}

func (l *monitorLogger) close() {
	if l.f != nil {
		_ = l.f.Close()
	}
}

func (l *monitorLogger) log(level, format string, args ...any) {
	ts := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s\n", ts, strings.ToUpper(level), msg)
	os.Stdout.WriteString(line)
	if l.f != nil {
		_, _ = l.f.WriteString(line)
	}
}

func (l *monitorLogger) Info(format string, args ...any)    { l.log("INFO", format, args...) }
func (l *monitorLogger) Warn(format string, args ...any)    { l.log("WARN", format, args...) }
func (l *monitorLogger) Error(format string, args ...any)   { l.log("ERROR", format, args...) }
func (l *monitorLogger) Success(format string, args ...any) { l.log("SUCCESS", format, args...) }

// RunAutoUpdateMonitor performs a single auto-update check, intended to be
// run periodically (e.g. via cron: `csm monitor`).
func RunAutoUpdateMonitor() error {
	logger := newMonitorLogger()
	defer logger.close()

	if os.Geteuid() != 0 {
		logger.Error("This command must be run as root (use sudo)")
		return fmt.Errorf("auto-update monitor requires root")
	}

	logger.Info("Starting CS2 Auto-Update Monitor check")

	mgr, err := NewTmuxManager()
	if err != nil {
		logger.Error("Failed to initialize tmux manager: %v", err)
		return err
	}

	// Step 1: Check if all servers are down.
	if !allServersDown(mgr) {
		logger.Info("Servers are running normally, no action needed")
		return nil
	}

	logger.Info("All servers are shut down, checking logs for AutoUpdater message...")

	// Step 2: Look for AutoUpdater shutdown message.
	found, err := checkAutoUpdaterShutdownLogs(mgr, logger)
	if err != nil {
		logger.Error("Error while checking logs: %v", err)
		return err
	}
	if !found {
		logger.Info("No AutoUpdater shutdown message found in logs")
		logger.Info("Servers may have been stopped manually or for other reasons")
		return nil
	}

	logger.Warn("AutoUpdater shutdown detected!")

	// Step 3: Cooldown check so we don't loop.
	if !shouldProcessUpdate(logger) {
		return nil
	}

	logger.Info("Proceeding with automated update...")

	// Step 4: Run game update via Go-native updater.
	logger.Info("Step 1: Updating CS2 game files...")
	out, err := UpdateGame()
	if out != "" {
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			logger.Info("[UpdateGame] %s", line)
		}
	}
	if err != nil {
		logger.Error("Game update failed: %v", err)
		return err
	}
	logger.Success("Game update completed successfully")

	// Brief pause for servers to stabilise.
	time.Sleep(5 * time.Second)

	// Step 5: Log current server status.
	if status, err := mgr.Status(); err == nil {
		logger.Info("Server status after update:\n%s", status)
	} else {
		logger.Warn("Could not retrieve server status after update: %v", err)
	}

	markUpdateProcessed(logger)
	logger.Success("Automated update process completed successfully")
	return nil
}

// InstallAutoUpdateCron installs (or replaces) a cronjob for the auto-update
// monitor. The interval should be a minute field like "*/5" (default when
// empty). It returns a human-readable summary of what was done.
func InstallAutoUpdateCron(interval string) (string, error) {
	if interval == "" {
		interval = "*/5"
	}

	if os.Geteuid() != 0 {
		return "", fmt.Errorf("cron installation must be run as root (use sudo)")
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Setting up CS2 auto-update monitor cronjob\n")

	// Ensure log file exists.
	if err := ensureMonitorLogFile(&buf); err != nil {
		return buf.String(), err
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(&buf, "ERROR: could not determine executable path: %v\n", err)
		return buf.String(), err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		fmt.Fprintf(&buf, "ERROR: could not resolve absolute path for executable: %v\n", err)
		return buf.String(), err
	}

	cronCommand := fmt.Sprintf("%s monitor >> %s 2>&1", exe, monitorLogFile)
	cronLine := fmt.Sprintf("%s * * * * %s", interval, cronCommand)

	// Read existing crontab (if any).
	existing, _ := exec.Command("crontab", "-l").Output()
	lines := strings.Split(string(existing), "\n")
	filtered := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.Contains(l, "cs2_auto_update_monitor") || strings.Contains(l, exe+" monitor") {
			// drop old lines
			continue
		}
		if strings.TrimSpace(l) != "" {
			filtered = append(filtered, l)
		}
	}
	filtered = append(filtered, cronLine)
	newCrontab := strings.Join(filtered, "\n") + "\n"

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(newCrontab)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(&buf, "ERROR: failed to install cronjob: %v\n", err)
		return buf.String(), err
	}

	fmt.Fprintf(&buf, "Cronjob installed successfully.\n")
	fmt.Fprintf(&buf, "  Schedule: %s * * * *\n", interval)
	fmt.Fprintf(&buf, "  Command : %s\n", cronCommand)
	fmt.Fprintf(&buf, "  Log file: %s\n", monitorLogFile)

	return buf.String(), nil
}

// --- helpers ---

func ensureMonitorLogFile(w *bytes.Buffer) error {
	f, err := os.OpenFile(monitorLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(w, "ERROR: could not create log file %s: %v\n", monitorLogFile, err)
		return err
	}
	_ = f.Close()
	fmt.Fprintf(w, "Log file ensured at %s\n", monitorLogFile)
	return nil
}

func allServersDown(m *TmuxManager) bool {
	for i := 1; i <= m.NumServers; i++ {
		session := m.sessionName(i)
		if m.sessionExists(session) {
			return false
		}
	}
	return true
}

func checkAutoUpdaterShutdownLogs(m *TmuxManager, logger *monitorLogger) (bool, error) {
	for i := 1; i <= m.NumServers; i++ {
		logDir := filepath.Join("/home", m.CS2User, fmt.Sprintf("server-%d", i), "game", "csgo", "addons", "counterstrikesharp", "logs")
		fi, err := os.Stat(logDir)
		if err != nil || !fi.IsDir() {
			logger.Warn("Log directory not found for server %d: %s", i, logDir)
			continue
		}

		latest, err := latestLogFile(logDir, "log-all")
		if err != nil {
			logger.Warn("No log files found for server %d in %s (%v)", i, logDir, err)
			continue
		}

		logger.Info("Checking latest log for server %d: %s", i, latest)
		lines, err := tailFileLines(latest, 200)
		if err != nil {
			logger.Warn("Could not read log file %s: %v", latest, err)
			continue
		}

		for _, line := range lines {
			if strings.Contains(line, autoUpdaterMsg) {
				logger.Info("Found AutoUpdater shutdown message in server %d log: %s", i, latest)
				return true, nil
			}
		}
	}
	return false, nil
}

func latestLogFile(dir, prefix string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var (
		bestPath string
		bestTime time.Time
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestTime) || bestPath == "" {
			bestTime = info.ModTime()
			bestPath = filepath.Join(dir, name)
		}
	}
	if bestPath == "" {
		return "", fmt.Errorf("no matching log files")
	}
	return bestPath, nil
}

func tailFileLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if n > 0 && len(lines) > n {
			lines = lines[1:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func shouldProcessUpdate(logger *monitorLogger) bool {
	// If state file doesn't exist, we should process.
	data, err := os.ReadFile(monitorStateFile)
	if err != nil {
		return true
	}

	var last int64
	if _, err := fmt.Sscanf(string(bytes.TrimSpace(data)), "%d", &last); err != nil {
		return true
	}

	now := time.Now().Unix()
	diff := now - last
	if diff > 3600 {
		return true
	}

	logger.Info("Update already processed recently (%ds ago), skipping", diff)
	return false
}

func markUpdateProcessed(logger *monitorLogger) {
	now := time.Now().Unix()
	_ = os.WriteFile(monitorStateFile, []byte(fmt.Sprintf("%d\n", now)), 0o644)
	logger.Info("Marked update as processed at %s", time.Unix(now, 0).Format(time.RFC3339))
}



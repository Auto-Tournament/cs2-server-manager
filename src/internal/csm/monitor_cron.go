package csm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// monitorCronMarker identifies csm's auto-update monitor entry in a crontab.
// It matches the `grep -v 'csm monitor'` older csm versions used to replace
// the entry, so entries written by any version are recognised.
const monitorCronMarker = "csm monitor"

// isMonitorCronLine reports whether a crontab line runs the csm monitor.
func isMonitorCronLine(line string) bool {
	return strings.Contains(line, monitorCronMarker)
}

// splitCrontabLines splits crontab text into lines, dropping the trailing
// empty line a final newline produces.
func splitCrontabLines(tab string) []string {
	if tab == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(tab, "\r\n", "\n"), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinCrontabLines joins lines back into crontab text. crontab needs a
// newline after the last entry.
func joinCrontabLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// stripMonitorCronLines removes every csm monitor entry from a crontab and
// returns the remaining text plus the removed lines, in order.
func stripMonitorCronLines(tab string) (kept string, removed []string) {
	var keep []string
	for _, line := range splitCrontabLines(tab) {
		if isMonitorCronLine(line) {
			removed = append(removed, line)
			continue
		}
		keep = append(keep, line)
	}
	return joinCrontabLines(keep), removed
}

// withMonitorCronLine returns the crontab with exactly one csm monitor entry,
// entry, replacing any existing ones and keeping every other line.
func withMonitorCronLine(tab, entry string) string {
	kept, _ := stripMonitorCronLines(tab)
	return joinCrontabLines(append(splitCrontabLines(kept), entry))
}

// crontabArgs returns the crontab arguments for user ("" = the current user).
func crontabArgs(user string, args ...string) []string {
	if strings.TrimSpace(user) == "" {
		return args
	}
	return append([]string{"-u", user}, args...)
}

// readCrontab returns the crontab of user ("" = the current user). A user
// without a crontab has an empty one. Reading another user's crontab needs
// root.
func readCrontab(ctx context.Context, user string) (string, error) {
	cmd := exec.CommandContext(ctx, "crontab", crontabArgs(user, "-l")...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// writeCrontab replaces the crontab of user ("" = the current user).
func writeCrontab(ctx context.Context, user, content string) error {
	cmd := exec.CommandContext(ctx, "crontab", crontabArgs(user, "-")...)
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab write failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

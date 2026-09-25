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

// migrateMonitorCron moves the csm monitor entry from root's crontab to the
// CS2 user's. It returns the new contents of both and whether anything moved.
// When root has several entries, the last one wins (as in cron order). An
// entry the user already has is replaced, so running it twice is a no-op.
func migrateMonitorCron(rootTab, userTab string) (newRoot, newUser string, moved bool) {
	keptRoot, removed := stripMonitorCronLines(rootTab)
	if len(removed) == 0 {
		return rootTab, userTab, false
	}
	return keptRoot, withMonitorCronLine(userTab, removed[len(removed)-1]), true
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

// MigrateMonitorCronToUser moves the csm monitor entry from root's crontab to
// cs2User's crontab. It must run as root. Running it again does nothing.
func MigrateMonitorCronToUser(ctx context.Context, cs2User string) (string, error) {
	if _, err := exec.LookPath("crontab"); err != nil {
		return "crontab not found (cron not installed); nothing to move.\n", nil
	}
	rootTab, err := readCrontab(ctx, "")
	if err != nil {
		return "", err
	}
	userTab, err := readCrontab(ctx, cs2User)
	if err != nil {
		return "", err
	}
	newRoot, newUser, moved := migrateMonitorCron(rootTab, userTab)
	if !moved {
		if _, has := stripMonitorCronLines(userTab); len(has) > 0 {
			return fmt.Sprintf("Monitor cron entry is already in %s's crontab.\n", cs2User), nil
		}
		return "No monitor cron entry in root's crontab; nothing to move (install one as the CS2 user with `csm install-monitor-cron`).\n", nil
	}
	// Write the user's entry first: if the second write fails, the monitor
	// runs from both crontabs (they share a lock) rather than from neither.
	if err := writeCrontab(ctx, cs2User, newUser); err != nil {
		return "", err
	}
	if err := writeCrontab(ctx, "", newRoot); err != nil {
		return "", err
	}
	return fmt.Sprintf("Moved the monitor cron entry from root's crontab to %s's crontab.\n", cs2User), nil
}

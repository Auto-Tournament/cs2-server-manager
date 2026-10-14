package csm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Moving an old install's state out of /usr/local/bin
//
// An older `csm agent install` wrote the binary's directory into the agent
// unit (Environment=CSM_ROOT=/usr/local/bin), so that host keeps its fleet
// credentials, license, update state and logs next to the binaries. Today's
// ResolveRoot never picks a system bin dir. The root self-update moves that
// state to the CS2 user's home and points the agent unit, the self-update
// timer (it copies the agent's root) and the monitor cron line at it, so no
// one runs a command.

// binDirStateNames is csm's state in a CSM_ROOT. Only these names move: the
// bin dir also holds binaries that are not csm's.
var binDirStateNames = []string{
	"fleet",
	"license.json",
	"license_checkin.json",
	"auto-update-state.json",
	"auto-update.lock",
	"instances.json",
	"plugins.json",
	".csm-update.json",
	"overrides",
	"game_files",
	"logs",
	"csm.log",
}

// movedAsideSuffix names a file the home already had where the bin dir's
// copy (the one the agent used) now goes: kept, never overwritten.
const movedAsideSuffix = ".before-move"

// MigrateStateOutOfBinDirs moves the state of every user-mode host agent
// whose unit runs it from a system bin dir to that user's home. Root only;
// it returns what it did, one line each. An agent is stopped while its
// state moves and started again after.
func MigrateStateOutOfBinDirs(ctx context.Context) []string {
	if os.Geteuid() != 0 {
		return nil
	}
	var done []string
	units, _ := filepath.Glob("/home/*/.config/systemd/user/" + HostAgentUnit)
	for _, unit := range units {
		user := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(unit)))))
		home := homeOf(user)
		if home == "" {
			home = filepath.Join("/home", user)
		}
		line, err := migrateAgentState(ctx, unit, user, home)
		if err != nil {
			done = append(done, fmt.Sprintf("could not move %s's csm state out of the bin dir: %v", user, err))
			continue
		}
		if line != "" {
			done = append(done, line)
		}
	}
	return done
}

func migrateAgentState(ctx context.Context, unit, user, home string) (string, error) {
	data, err := os.ReadFile(unit)
	if err != nil {
		return "", err
	}
	from := unitCSMRoot(string(data))
	if from == "" || !isSystemBinDir(from) {
		return "", nil
	}
	if !sameFilesystem(from, home) {
		// A copy across disks could fill one: leave it where it works.
		return "", fmt.Errorf("%s and %s are on different filesystems", from, home)
	}

	userctl := func(args ...string) error {
		return exec.CommandContext(ctx, "systemctl", append([]string{"--user", "-M", user + "@"}, args...)...).Run()
	}
	_ = userctl("stop", HostAgentUnit)
	// The monitor first, so a cycle that starts mid-move writes to the home.
	repointMonitorCron(ctx, user, from, home)

	moved, err := moveStateEntries(from, home)
	if err != nil {
		// Whatever moved stays moved; the unit still points at the bin dir,
		// which keeps the rest, so start the agent (and the monitor) as they were.
		repointMonitorCron(ctx, user, home, from)
		_ = userctl("start", HostAgentUnit)
		return "", err
	}
	rewritten := strings.Replace(string(data), "Environment=CSM_ROOT="+from, "Environment=CSM_ROOT="+home, 1)
	if err := os.WriteFile(unit, []byte(rewritten), 0o644); err != nil {
		_ = userctl("start", HostAgentUnit)
		return "", err
	}
	_ = userctl("daemon-reload")
	_ = userctl("start", HostAgentUnit)
	return fmt.Sprintf("moved %s's csm state (%d items) from %s to %s", user, moved, from, home), nil
}

// moveStateEntries moves csm's state from one root to another. A directory
// already in the target is merged; a file there is renamed with
// movedAsideSuffix first. It returns how many top-level items moved.
func moveStateEntries(from, to string) (int, error) {
	n := 0
	for _, name := range binDirStateNames {
		src := filepath.Join(from, name)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if err := moveInto(src, filepath.Join(to, name)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func moveInto(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	dstInfo, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return os.Rename(src, dst)
	}
	if err != nil {
		return err
	}
	if srcInfo.IsDir() && dstInfo.IsDir() {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := moveInto(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Remove(src)
	}
	aside := dst + movedAsideSuffix
	for i := 2; ; i++ {
		if _, err := os.Lstat(aside); errors.Is(err, os.ErrNotExist) {
			break
		}
		aside = fmt.Sprintf("%s%s.%d", dst, movedAsideSuffix, i)
	}
	if err := os.Rename(dst, aside); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func sameFilesystem(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

// repointMonitorCron rewrites the user's monitor cron line from the old root
// to the new one, keeping its interval and binary.
func repointMonitorCron(ctx context.Context, user, from, to string) {
	tab, err := readCrontab(ctx, user)
	if err != nil {
		return
	}
	lines := splitCrontabLines(tab)
	changed := false
	for i, line := range lines {
		if isMonitorCronLine(line) && strings.Contains(line, "CSM_ROOT="+from+" ") {
			lines[i] = strings.Replace(line, "CSM_ROOT="+from+" ", "CSM_ROOT="+to+" ", 1)
			changed = true
		}
	}
	if changed {
		_ = writeCrontab(ctx, user, joinCrontabLines(lines))
	}
}

package csm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// checkRoundBackupFlag verifies that every running server's cs2 process was
// started with -allow_round_backup_files. A server started before csm 1.27.4
// keeps running without it and writes no round backups, so a round restore
// finds nothing. The flag only takes effect at launch, so the fix is a restart.
func checkRoundBackupFlag(meta DoctorMeta, running func(server int) bool) DoctorCheck {
	const title = "Running servers have " + roundBackupFlag
	if len(meta.Servers) == 0 {
		return DoctorCheck{ID: "round-backup-flag", Title: title, Status: DoctorOK, Detail: "No servers to check."}
	}

	var missing, ok, unknown []int
	for _, n := range meta.Servers {
		if !running(n) {
			continue
		}
		dir := filepath.Join(userHomeDir(meta.CS2User), fmt.Sprintf("server-%d", n))
		cmdlines := serverCS2Cmdlines("/proc", dir)
		switch {
		case len(cmdlines) == 0:
			unknown = append(unknown, n)
		case anyCmdlineHasFlag(cmdlines, roundBackupFlag):
			ok = append(ok, n)
		default:
			missing = append(missing, n)
		}
	}

	if len(missing) > 0 {
		var restarts []string
		for _, n := range missing {
			restarts = append(restarts, fmt.Sprintf("csm restart server-%d", n))
		}
		return DoctorCheck{
			ID:      "round-backup-flag",
			Title:   title,
			Status:  DoctorFail,
			Detail:  fmt.Sprintf("%s running without %s: CS2 writes no round backups, so a round restore finds none.", serverNames(missing), roundBackupFlag),
			FixHint: "Restart: " + strings.Join(restarts, ", ") + " (csm adds the flag at launch; wait for the match to end first).",
		}
	}
	if len(ok) == 0 && len(unknown) == 0 {
		return DoctorCheck{ID: "round-backup-flag", Title: title, Status: DoctorOK, Detail: "No servers running."}
	}
	var lines []string
	if len(ok) > 0 {
		lines = append(lines, fmt.Sprintf("%s started with %s.", serverNames(ok), roundBackupFlag))
	}
	status := DoctorOK
	if len(unknown) > 0 {
		status = DoctorWarn
		lines = append(lines, fmt.Sprintf("%s: tmux session is up but no cs2 process found (still starting?); not checked.", serverNames(unknown)))
	}
	return DoctorCheck{ID: "round-backup-flag", Title: title, Status: status, Detail: strings.Join(lines, "\n")}
}

func serverNames(ns []int) string {
	var parts []string
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("server-%d", n))
	}
	return strings.Join(parts, ", ")
}

// serverCS2Cmdlines returns the command line (NUL-separated, as read from
// /proc/<pid>/cmdline) of every cs2 process whose working directory is under
// serverDir. procRoot is "/proc" (a parameter for tests).
func serverCS2Cmdlines(procRoot, serverDir string) [][]byte {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	prefix := filepath.Clean(serverDir) + string(os.PathSeparator)
	var out [][]byte
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(procRoot, e.Name(), "cwd"))
		if err != nil || !strings.HasPrefix(filepath.Clean(cwd)+string(os.PathSeparator), prefix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		argv0 := strings.SplitN(string(raw), "\x00", 2)[0]
		if filepath.Base(argv0) != "cs2" {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func anyCmdlineHasFlag(cmdlines [][]byte, flag string) bool {
	for _, raw := range cmdlines {
		for _, arg := range strings.Split(string(raw), "\x00") {
			if arg == flag {
				return true
			}
		}
	}
	return false
}

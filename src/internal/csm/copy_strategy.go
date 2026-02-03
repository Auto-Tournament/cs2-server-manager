package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func rsyncArgsLegacyCopy(srcRoot, dstRoot string, progress2 bool) []string {
	args := []string{
		"-a", "--delete",
		"--exclude", "csgo/addons/",
	}
	if progress2 {
		args = append(args, "--info=PROGRESS2")
	}
	args = append(args, srcRoot, dstRoot)
	return args
}

func rsyncArgsTunedCopy(srcRoot, dstRoot string, progress2 bool, chownUser string) []string {
	args := []string{
		"-a",
		"--whole-file",
		"--omit-dir-times",
		"--delete",
		"--exclude", "csgo/addons/",
	}
	// When running as root, have rsync write the correct ownership directly so
	// we can avoid a slow recursive chown over large game trees.
	if os.Geteuid() == 0 && strings.TrimSpace(chownUser) != "" {
		args = append(args, "--chown="+strings.TrimSpace(chownUser)+":"+strings.TrimSpace(chownUser))
	} else {
		// Fall back to the older tuned behaviour (avoid trying to preserve
		// ownership/group when we cannot set them).
		args = append(args, "--no-owner", "--no-group")
	}
	if progress2 {
		args = append(args, "--info=PROGRESS2")
	}
	args = append(args, srcRoot, dstRoot)
	return args
}

// copyMasterGameToServerGame replicates <masterDir>/game/ into <serverGameDir>/.
// It respects CSM_COPY_MODE for the copy strategy.
func copyMasterGameToServerGame(ctx context.Context, w io.Writer, cs2User, masterDir, serverGameDir string, allowReflink bool, progress2 bool) error {
	mode := CopyModeFromEnv()
	recordCopyNote(string(mode))

	srcRoot := filepath.Join(masterDir, "game") + string(os.PathSeparator)
	dstRoot := serverGameDir + string(os.PathSeparator)

	// For install-like operations, auto/reflink can try reflink cloning as a
	// best-effort speedup when the destination looks safe to populate.
	if allowReflink && (mode == CopyModeAuto || mode == CopyModeReflink) {
		// Only attempt reflink when the destination is missing/empty to avoid
		// leaving stale files (rsync --delete handles that; cp does not).
		if destLooksEmpty(serverGameDir) {
			if ok, why, err := tryReflinkClone(ctx, w, filepath.Join(masterDir, "game"), serverGameDir); ok {
				// Mirror legacy behaviour: master copy must not define addons; those
				// come from the overlay step.
				_ = os.RemoveAll(filepath.Join(serverGameDir, "csgo", "addons"))
				if strings.TrimSpace(why) != "" {
					fmt.Fprintf(w, "  [i] Reflink copy succeeded (%s)\n", why)
					RecordCopyReflinkSuccess(why)
				} else {
					fmt.Fprintln(w, "  [i] Reflink copy succeeded")
					RecordCopyReflinkSuccess("reflink")
				}
				return nil
			} else if err != nil {
				// For reflink/auto, treat failure as a fallback to tuned rsync.
				if strings.TrimSpace(why) != "" {
					fmt.Fprintf(w, "  [i] Reflink copy unavailable (%s); falling back to rsync\n", why)
					RecordCopyReflinkFallback(why)
				} else {
					fmt.Fprintln(w, "  [i] Reflink copy unavailable; falling back to rsync")
					RecordCopyReflinkFallback("reflink unavailable")
				}
				// fall through
			}
		} else {
			fmt.Fprintln(w, "  [i] Destination not empty; skipping reflink and using rsync")
			RecordCopyReflinkSkipped("dest not empty")
		}
	}

	// Rsync path (legacy or tuned).
	var args []string
	switch mode {
	case CopyModeLegacy:
		args = rsyncArgsLegacyCopy(srcRoot, dstRoot, progress2)
		RecordCopyRsyncLegacy("rsync legacy")
	default:
		args = rsyncArgsTunedCopy(srcRoot, dstRoot, progress2, cs2User)
		RecordCopyRsyncTuned("rsync tuned")
	}

	if err := runCmdLoggedContext(ctx, w, "rsync", args...); err != nil {
		return fmt.Errorf("rsync failed: %w", err)
	}
	return nil
}

func destLooksEmpty(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return true
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return len(ents) == 0
}

func tryReflinkClone(ctx context.Context, w io.Writer, srcDir, dstDir string) (ok bool, reason string, _ error) {
	// `cp --reflink=always` is the most reliable “must reflink or fail” variant.
	// We capture its output to decide whether to fall back.
	var out bytes.Buffer
	target := io.MultiWriter(w, &out)

	// Ensure destination exists.
	_ = os.MkdirAll(dstDir, 0o755)

	cmd := exec.CommandContext(ctx, "cp", "-a", "--reflink=always", filepath.Join(srcDir, "."), dstDir)
	cmd.Stdout = target
	cmd.Stderr = target
	err := cmd.Run()
	if err == nil {
		return true, "cp --reflink=always", nil
	}

	l := strings.ToLower(out.String())
	// Common reasons for reflink not working:
	// - unsupported FS (operation not supported / invalid argument)
	// - cp implementation doesn't support --reflink
	if strings.Contains(l, "reflink") && strings.Contains(l, "not supported") {
		return false, "reflink not supported", err
	}
	if strings.Contains(l, "operation not supported") || strings.Contains(l, "not supported") {
		return false, "operation not supported", err
	}
	if strings.Contains(l, "invalid argument") {
		return false, "invalid argument (likely unsupported)", err
	}
	if strings.Contains(l, "unrecognized option") || strings.Contains(l, "unknown option") {
		return false, "cp does not support --reflink", err
	}

	// Unknown failure: still allow fallback in auto/reflink modes.
	return false, "cp reflink failed", err
}

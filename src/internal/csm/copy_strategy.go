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

// rsyncExcludeVPK keeps rsync away from *.vpk files when they are managed as
// hardlinks by the VPK linker. Excluded files are also protected from
// --delete, so stale VPKs are pruned separately (see pruneStaleVPKs).
var rsyncExcludeVPK = []string{"--exclude", "*.vpk"}

func rsyncArgsLegacyCopy(srcRoot, dstRoot string, progress2, excludeVPK bool, keep ...string) []string {
	args := []string{
		"-a", "--delete",
		"--exclude", "csgo/addons/",
	}
	if excludeVPK {
		args = append(args, rsyncExcludeVPK...)
	}
	args = append(args, keep...)
	if progress2 {
		args = append(args, "--info=PROGRESS2")
	}
	args = append(args, srcRoot, dstRoot)
	return args
}

func rsyncArgsTunedCopy(srcRoot, dstRoot string, progress2 bool, chownUser string, excludeVPK bool, keep ...string) []string {
	args := []string{
		"-a",
		"--whole-file",
		"--omit-dir-times",
		"--delete",
		"--exclude", "csgo/addons/",
	}
	if excludeVPK {
		args = append(args, rsyncExcludeVPK...)
	}
	args = append(args, keep...)
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
//
// Unless CSM_VPK_HARDLINK=0, *.vpk files are not copied: they are hardlinked
// to master's files after the rsync of everything else (falling back to a
// real copy if linking fails). Every non-VPK file stays a per-server copy.
func copyMasterGameToServerGame(ctx context.Context, w io.Writer, cs2User, masterDir, serverGameDir string, allowReflink bool, progress2 bool) error {
	mode := CopyModeFromEnv()
	recordCopyNote(string(mode))

	linkVPK := VPKHardlinksEnabled()
	masterGame := filepath.Join(masterDir, "game")
	var linker *vpkLinker
	if linkVPK {
		linker = newVPKLinker(w)
	}
	finishVPKs := func(pruned VPKStats) error {
		if linker == nil {
			return nil
		}
		st, err := linker.linkServerVPKs(ctx, masterGame, serverGameDir)
		st.add(pruned)
		fmt.Fprintf(w, "  [i] %s\n", st.syncSummary())
		if err != nil {
			return fmt.Errorf("link VPKs: %w", err)
		}
		return nil
	}

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
				return finishVPKs(VPKStats{})
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

	// With *.vpk excluded, rsync --delete no longer removes VPKs that are gone
	// from master, so prune those first (before rsync, so it can still delete
	// directories that only held such files).
	var pruned VPKStats
	if linker != nil {
		st, err := linker.pruneStaleVPKs(ctx, masterGame, serverGameDir)
		if err != nil {
			return fmt.Errorf("prune stale VPKs: %w", err)
		}
		pruned = st
	}

	// Keep the server's own configs (server.cfg with its rcon_password,
	// autoexec.cfg, ban lists, plugin cfg folders) out of the sync.
	keep := serverCfgKeepExcludes(masterGame, serverGameDir)

	// Rsync path (legacy or tuned).
	var args []string
	switch mode {
	case CopyModeLegacy:
		args = rsyncArgsLegacyCopy(srcRoot, dstRoot, progress2, linkVPK, keep...)
		RecordCopyRsyncLegacy("rsync legacy")
	default:
		args = rsyncArgsTunedCopy(srcRoot, dstRoot, progress2, cs2User, linkVPK, keep...)
		RecordCopyRsyncTuned("rsync tuned")
	}

	if err := runCmdLoggedContext(ctx, w, "rsync", args...); err != nil {
		return fmt.Errorf("rsync failed: %w", err)
	}
	return finishVPKs(pruned)
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

// serverOwnedCfgFiles are files in a server's csgo/cfg that CSM or the game
// server writes per server. A master -> server sync must never replace them
// with master's copy: server.cfg and autoexec.cfg carry rcon_password (and
// the RCON ban settings), banned_*.cfg are the server's ban lists.
var serverOwnedCfgFiles = []string{
	"server.cfg",
	"autoexec.cfg",
	"banned_ip.cfg",
	"banned_user.cfg",
}

// serverCfgKeepExcludes returns rsync --exclude rules that keep a server's own
// configs through a master -> server sync (update-game, fix-libv8, reinstall
// of an existing server). It protects:
//
//   - the server-owned files above, when the server already has them, from
//     being overwritten by master's version, and
//   - every csgo/cfg entry master does not have (AutoTournamentCS2/, custom cfgs) from
//     rsync --delete.
//
// Before this, update-game replaced server.cfg with master's copy and deleted
// autoexec.cfg, so rcon_password was lost after every game update and the
// platform's RCON login failed until the server banned its IP.
//
// On a fresh server (no csgo/cfg yet) it returns nothing, so master's cfg
// files are copied as before.
func serverCfgKeepExcludes(masterGame, serverGame string) []string {
	serverCfg := filepath.Join(serverGame, "csgo", "cfg")
	entries, err := os.ReadDir(serverCfg)
	if err != nil {
		return nil
	}
	owned := make(map[string]bool, len(serverOwnedCfgFiles))
	for _, name := range serverOwnedCfgFiles {
		owned[name] = true
	}
	masterCfg := filepath.Join(masterGame, "csgo", "cfg")
	var out []string
	for _, e := range entries {
		name := e.Name()
		keep := owned[name]
		if !keep {
			if _, err := os.Lstat(filepath.Join(masterCfg, name)); os.IsNotExist(err) {
				keep = true
			}
		}
		if !keep {
			continue
		}
		pattern := "/csgo/cfg/" + name
		if e.IsDir() {
			pattern += "/"
		}
		out = append(out, "--exclude", pattern)
	}
	return out
}

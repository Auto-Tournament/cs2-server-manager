package csm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DedupeVPKOptions configures the `csm dedupe-vpk` migration.
type DedupeVPKOptions struct {
	Server       int  // 0 = all servers
	Verify       bool // byte-compare files before linking (slow: reads every VPK twice)
	DryRun       bool // report only, change nothing
	Undo         bool // replace hardlinks with independent copies (rollback)
	AllowRunning bool // proceed even if target servers are running
}

// dedupeTarget is one server game dir to process.
type dedupeTarget struct {
	num  int
	game string
}

// DedupeVPKs converts existing full-copy servers to hardlinked VPKs (or back,
// with Undo). Output includes before/after disk usage for master-install plus
// all servers. Progress is streamed to live (may be nil) as it happens; the
// full log is also returned.
func DedupeVPKs(ctx context.Context, live io.Writer, opts DedupeVPKOptions) (string, error) {
	var buf bytes.Buffer
	var w io.Writer = &buf
	if live != nil {
		w = io.MultiWriter(&buf, live)
	}

	mgr, err := NewTmuxManager()
	if err != nil {
		return "", err
	}
	if mgr.NumServers <= 0 {
		return "", fmt.Errorf("no servers found for user %s", mgr.CS2User)
	}
	home := filepath.Join("/home", mgr.CS2User)
	masterDir := filepath.Join(home, "master-install")

	var targets []dedupeTarget
	var allServerDirs []string
	for i := 1; i <= mgr.NumServers; i++ {
		d := mgr.serverDir(i)
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			continue
		}
		allServerDirs = append(allServerDirs, d)
		if opts.Server == 0 || opts.Server == i {
			targets = append(targets, dedupeTarget{num: i, game: filepath.Join(d, "game")})
		}
	}
	if opts.Server > 0 && len(targets) == 0 {
		return "", fmt.Errorf("server-%d not found", opts.Server)
	}

	return runDedupeVPKs(ctx, w, &buf, masterDir, allServerDirs, targets, opts, mgr.IsRunning)
}

func runDedupeVPKs(ctx context.Context, w io.Writer, buf *bytes.Buffer, masterDir string, allServerDirs []string, targets []dedupeTarget, opts DedupeVPKOptions, isRunning func(int) bool) (string, error) {
	masterGame := filepath.Join(masterDir, "game")
	if fi, err := os.Stat(masterGame); err != nil || !fi.IsDir() {
		return buf.String(), fmt.Errorf("master install not found at %s", masterGame)
	}

	action := "Hardlink identical VPKs to master-install"
	if opts.Undo {
		action = "Replace hardlinked VPKs with independent copies (undo)"
	}
	fmt.Fprintf(w, "=== %s ===\n", action)
	fmt.Fprintf(w, "Master: %s\n", masterGame)
	if opts.DryRun {
		fmt.Fprintln(w, "Dry run: nothing will be changed.")
	}

	if !opts.DryRun && !opts.AllowRunning && isRunning != nil {
		var running []string
		for _, t := range targets {
			if isRunning(t.num) {
				running = append(running, fmt.Sprintf("server-%d", t.num))
			}
		}
		if len(running) > 0 {
			fmt.Fprintf(w, "[!] Running: %s\n", strings.Join(running, ", "))
			fmt.Fprintln(w, "    Stop them first (sudo csm stop), or pass --allow-running.")
			return buf.String(), fmt.Errorf("servers are running: %s", strings.Join(running, ", "))
		}
	}

	roots := append([]string{masterDir}, allServerDirs...)
	fmt.Fprintln(w, "Measuring disk usage (hardlinks counted once)...")
	before, err := diskUsage(roots...)
	if err != nil {
		return buf.String(), fmt.Errorf("measure disk usage: %w", err)
	}
	fmt.Fprintf(w, "Before: %s used by master-install + %d server(s)\n\n", formatGB(before), len(allServerDirs))

	linker := newVPKLinker(w)
	var total VPKStats
	var firstErr error
	for _, t := range targets {
		if err := vpkCheckCtx(ctx); err != nil {
			return buf.String(), err
		}
		if fi, err := os.Stat(t.game); err != nil || !fi.IsDir() {
			fmt.Fprintf(w, "[!] server-%d: %s not found, skipping\n", t.num, t.game)
			continue
		}
		var st VPKStats
		if opts.Undo {
			need, err := linkedVPKBytes(masterGame, t.game)
			if err != nil {
				return buf.String(), err
			}
			if free, ferr := freeBytes(t.game); ferr == nil && !opts.DryRun && need > 0 && free < need+need/20 {
				err := fmt.Errorf("server-%d: need %s free to undo, only %s available", t.num, formatGB(need), formatGB(free))
				fmt.Fprintf(w, "[!] %v\n", err)
				return buf.String(), err
			}
			st, err = linker.unlinkServer(ctx, masterGame, t.game, opts.DryRun)
			if err != nil {
				fmt.Fprintf(w, "[!] server-%d: %v\n", t.num, err)
				if firstErr == nil {
					firstErr = err
				}
			}
			verb := "copied"
			if opts.DryRun {
				verb = "would copy"
			}
			fmt.Fprintf(w, "server-%d: %s %d VPK(s) (%s)\n", t.num, verb, st.Unlinked, formatGB(st.BytesUnlinked))
		} else {
			st, err = linker.dedupeServer(ctx, masterGame, t.game, opts.Verify, opts.DryRun)
			if err != nil {
				fmt.Fprintf(w, "[!] server-%d: %v\n", t.num, err)
				if firstErr == nil {
					firstErr = err
				}
			}
			verb := "linked"
			if opts.DryRun {
				verb = "would link"
			}
			fmt.Fprintf(w, "server-%d: %s %d (%s), already linked %d, differs %d, missing %d, link failed %d (of %d)\n",
				t.num, verb, st.Linked, formatGB(st.BytesLinked), st.AlreadyLinked, st.Differs, st.Missing, st.KeptCopy, st.Files)
		}
		total.add(st)
	}

	after, err := diskUsage(roots...)
	if err != nil {
		return buf.String(), fmt.Errorf("measure disk usage: %w", err)
	}
	fmt.Fprintln(w)
	switch {
	case opts.DryRun && !opts.Undo:
		fmt.Fprintf(w, "Before: %s, estimated after: %s (would save ~%s)\n", formatGB(before), formatGB(before-total.BytesLinked), formatGB(total.BytesLinked))
	case opts.DryRun && opts.Undo:
		fmt.Fprintf(w, "Before: %s, estimated after: %s (would use ~%s more)\n", formatGB(before), formatGB(before+total.BytesUnlinked), formatGB(total.BytesUnlinked))
	default:
		diff := before - after
		if diff >= 0 {
			fmt.Fprintf(w, "Before: %s, after: %s, saved %s\n", formatGB(before), formatGB(after), formatGB(diff))
		} else {
			fmt.Fprintf(w, "Before: %s, after: %s, used %s more\n", formatGB(before), formatGB(after), formatGB(-diff))
		}
	}
	if total.Differs > 0 {
		fmt.Fprintf(w, "[i] %d VPK(s) differ from master and were left as copies; `sudo csm update-game` (or reinstall) re-links them.\n", total.Differs)
	}
	if total.LinkFailures > 0 {
		fmt.Fprintf(w, "[!] %d hardlink attempt(s) failed; those servers keep full copies.\n", total.LinkFailures)
	}
	return buf.String(), firstErr
}

func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

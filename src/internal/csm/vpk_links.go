package csm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// VPK hardlinking
//
// Every server-N/game tree used to be a full copy of master-install/game
// (~67 GB each). About 98% of that is *.vpk archives, which CS2 only ever
// reads. Those files are now hardlinked from master-install into each server,
// while every other file (cfg, addons, gameinfo.gi, MatchZy data, demos, logs)
// stays a real per-server copy.
//
// Hardlinks (not symlinks) are used on purpose: the game resolves paths
// relative to its own install dir, and symlinked directories broke demo
// recording and per-server configs. A hardlink is indistinguishable from a
// regular file to the game.
//
// Update safety: nothing in CSM writes into a VPK in place. SteamCMD only
// updates master-install, and it stages changed files and moves them into
// place, so an updated VPK gets a new inode. Servers are then re-linked with
// link(tmp) + rename(tmp, dst), which is atomic per file: a running process
// keeps reading the old inode until it reopens the file.

// vpkTempSuffix is appended to temp files created next to a VPK while it is
// being atomically replaced.
const vpkTempSuffix = ".csm-vpk-tmp"

// VPKHardlinksEnabled reports whether master -> server syncs should hardlink
// *.vpk files. It is on by default; set CSM_VPK_HARDLINK=0 to go back to full
// per-server copies for new syncs.
func VPKHardlinksEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CSM_VPK_HARDLINK"))) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// VPKStats summarises what a link/dedupe/undo pass did for one server.
type VPKStats struct {
	Files         int   // VPKs present in master-install
	AlreadyLinked int   // server file was already a hardlink to master
	Linked        int   // server file replaced by / created as a hardlink
	Copied        int   // hardlink failed, real copy written instead
	KeptCopy      int   // hardlink failed, existing identical copy kept
	Differs       int   // dedupe: server file differs from master, left alone
	Missing       int   // dedupe: server has no file at that path
	Removed       int   // sync: server VPK not present in master was removed
	Unlinked      int   // undo: hardlink replaced by an independent copy
	LinkFailures  int   // number of failed link attempts
	BytesLinked   int64 // bytes covered by newly created links
	BytesUnlinked int64 // bytes duplicated by undo
}

func (s *VPKStats) add(o VPKStats) {
	s.Files += o.Files
	s.AlreadyLinked += o.AlreadyLinked
	s.Linked += o.Linked
	s.Copied += o.Copied
	s.KeptCopy += o.KeptCopy
	s.Differs += o.Differs
	s.Missing += o.Missing
	s.Removed += o.Removed
	s.Unlinked += o.Unlinked
	s.LinkFailures += o.LinkFailures
	s.BytesLinked += o.BytesLinked
	s.BytesUnlinked += o.BytesUnlinked
}

// vpkLinker holds the injectable filesystem operations so tests can simulate
// link failures (EXDEV, EPERM) without a second filesystem.
type vpkLinker struct {
	w    io.Writer
	link func(oldname, newname string) error

	// linkDisabled is set after a cross-device error: every further attempt
	// would fail the same way, so go straight to copies.
	linkDisabled bool
	warned       bool
}

func newVPKLinker(w io.Writer) *vpkLinker {
	if w == nil {
		w = io.Discard
	}
	return &vpkLinker{w: w, link: os.Link}
}

func isVPKName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".vpk")
}

// listVPKs returns the slash-separated relative paths of all regular *.vpk
// files under root. csgo/addons is skipped: it is managed by the plugin
// deploy, never comes from master-install, and is not touched here.
func listVPKs(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "csgo/addons" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isVPKName(d.Name()) || strings.HasSuffix(d.Name(), vpkTempSuffix) {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func tempSibling(dst string) string {
	return filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+vpkTempSuffix)
}

// sameMeta reports whether two files look identical by size and mtime (the
// same check rsync uses by default; rsync -a preserves nanosecond mtimes).
func sameMeta(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// ensureParentDir creates the parent directory of dst if needed, copying the
// owner of the matching master directory when running as root.
func ensureParentDir(masterGame, serverGame, rel string) error {
	dir := filepath.Dir(filepath.Join(serverGame, filepath.FromSlash(rel)))
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		// Walk up from the new dir to serverGame, fixing ownership of each
		// level that exists in master too.
		relDir := filepath.Dir(filepath.FromSlash(rel))
		for relDir != "." && relDir != string(os.PathSeparator) {
			if mfi, err := os.Stat(filepath.Join(masterGame, relDir)); err == nil {
				if st, ok := mfi.Sys().(*syscall.Stat_t); ok {
					_ = os.Lchown(filepath.Join(serverGame, relDir), int(st.Uid), int(st.Gid))
				}
			}
			relDir = filepath.Dir(relDir)
		}
	}
	return nil
}

// replaceWithLink atomically makes dst a hardlink to src: link to a temp name
// in the same directory, then rename over dst.
func (l *vpkLinker) replaceWithLink(src, dst string) error {
	tmp := tempSibling(dst)
	_ = os.Remove(tmp)
	if err := l.link(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// rename(2) is a no-op when tmp and dst are already the same inode and
	// leaves tmp behind; clean it up in that case.
	_ = os.Remove(tmp)
	return nil
}

// replaceWithCopy atomically replaces dst with an independent copy of src,
// preserving mode, mtime and (as root) ownership.
func replaceWithCopy(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	tmp := tempSibling(dst)
	_ = os.Remove(tmp)

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sfi.Mode().Perm())
	if err != nil {
		return err
	}
	cleanup := func(e error) error {
		_ = out.Close()
		_ = os.Remove(tmp)
		return e
	}
	if _, err := io.Copy(out, in); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(tmp, sfi.Mode().Perm())
	if os.Geteuid() == 0 {
		if st, ok := sfi.Sys().(*syscall.Stat_t); ok {
			_ = os.Lchown(tmp, int(st.Uid), int(st.Gid))
		}
	}
	if err := os.Chtimes(tmp, sfi.ModTime(), sfi.ModTime()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (l *vpkLinker) noteLinkFailure(err error) {
	if errors.Is(err, syscall.EXDEV) {
		l.linkDisabled = true
	}
	if !l.warned {
		l.warned = true
		fmt.Fprintf(l.w, "  [!] Hardlinking VPKs failed (%v); falling back to full copies\n", err)
	}
}

func vpkCheckCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// pruneStaleVPKs removes server VPKs that no longer exist in master. This
// mirrors what rsync --delete did before *.vpk was excluded from rsync. It is
// run before rsync so rsync can delete directories that only held such files.
func (l *vpkLinker) pruneStaleVPKs(ctx context.Context, masterGame, serverGame string) (VPKStats, error) {
	var st VPKStats
	if fi, err := os.Stat(serverGame); err != nil || !fi.IsDir() {
		return st, nil
	}
	master, err := listVPKs(masterGame)
	if err != nil {
		return st, fmt.Errorf("list master VPKs: %w", err)
	}
	inMaster := make(map[string]struct{}, len(master))
	for _, r := range master {
		inMaster[r] = struct{}{}
	}
	server, err := listVPKs(serverGame)
	if err != nil {
		return st, fmt.Errorf("list server VPKs: %w", err)
	}
	for _, rel := range server {
		if err := vpkCheckCtx(ctx); err != nil {
			return st, err
		}
		if _, ok := inMaster[rel]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(serverGame, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return st, fmt.Errorf("remove stale %s: %w", rel, err)
		}
		st.Removed++
	}
	return st, nil
}

// linkServerVPKs makes every master VPK present in serverGame as a hardlink
// to master's file, replacing whatever is there. When linking is impossible
// it keeps an existing identical copy or writes a fresh copy.
func (l *vpkLinker) linkServerVPKs(ctx context.Context, masterGame, serverGame string) (VPKStats, error) {
	var st VPKStats
	master, err := listVPKs(masterGame)
	if err != nil {
		return st, fmt.Errorf("list master VPKs: %w", err)
	}
	st.Files = len(master)
	for _, rel := range master {
		if err := vpkCheckCtx(ctx); err != nil {
			return st, err
		}
		src := filepath.Join(masterGame, filepath.FromSlash(rel))
		dst := filepath.Join(serverGame, filepath.FromSlash(rel))

		sfi, err := os.Stat(src)
		if err != nil {
			return st, err
		}
		dfi, derr := os.Lstat(dst)
		if derr == nil && os.SameFile(sfi, dfi) {
			st.AlreadyLinked++
			continue
		}
		if derr == nil && dfi.IsDir() {
			return st, fmt.Errorf("%s is a directory, expected a VPK file", dst)
		}
		if err := ensureParentDir(masterGame, serverGame, rel); err != nil {
			return st, err
		}

		if !l.linkDisabled {
			lerr := l.replaceWithLink(src, dst)
			if lerr == nil {
				st.Linked++
				st.BytesLinked += sfi.Size()
				continue
			}
			st.LinkFailures++
			l.noteLinkFailure(lerr)
		}

		if derr == nil && dfi.Mode().IsRegular() && sameMeta(sfi, dfi) {
			st.KeptCopy++
			continue
		}
		if err := replaceWithCopy(src, dst); err != nil {
			return st, fmt.Errorf("copy %s: %w", rel, err)
		}
		st.Copied++
	}
	return st, nil
}

// relinkServer runs prune + link for one server. The rsync of non-VPK files
// normally happens between the two steps; see copyMasterGameToServerGame.
func (l *vpkLinker) relinkServer(ctx context.Context, masterGame, serverGame string) (VPKStats, error) {
	st, err := l.pruneStaleVPKs(ctx, masterGame, serverGame)
	if err != nil {
		return st, err
	}
	ls, err := l.linkServerVPKs(ctx, masterGame, serverGame)
	st.add(ls)
	return st, err
}

// dedupeServer is the migration pass for existing full-copy servers: each
// server VPK that matches master (size + mtime, plus a byte comparison when
// verify is set) is atomically replaced by a hardlink. Files that differ or
// are missing are left untouched and counted. Running it twice is a no-op.
func (l *vpkLinker) dedupeServer(ctx context.Context, masterGame, serverGame string, verify, dryRun bool) (VPKStats, error) {
	var st VPKStats
	master, err := listVPKs(masterGame)
	if err != nil {
		return st, fmt.Errorf("list master VPKs: %w", err)
	}
	st.Files = len(master)
	for _, rel := range master {
		if err := vpkCheckCtx(ctx); err != nil {
			return st, err
		}
		src := filepath.Join(masterGame, filepath.FromSlash(rel))
		dst := filepath.Join(serverGame, filepath.FromSlash(rel))
		sfi, err := os.Stat(src)
		if err != nil {
			return st, err
		}
		dfi, err := os.Lstat(dst)
		if err != nil {
			if os.IsNotExist(err) {
				st.Missing++
				continue
			}
			return st, err
		}
		if os.SameFile(sfi, dfi) {
			st.AlreadyLinked++
			continue
		}
		if !dfi.Mode().IsRegular() || !sameMeta(sfi, dfi) {
			st.Differs++
			continue
		}
		if verify {
			eq, err := filesEqual(src, dst)
			if err != nil {
				return st, err
			}
			if !eq {
				st.Differs++
				continue
			}
		}
		if dryRun {
			st.Linked++
			st.BytesLinked += sfi.Size()
			continue
		}
		if l.linkDisabled {
			st.KeptCopy++
			continue
		}
		if err := l.replaceWithLink(src, dst); err != nil {
			st.LinkFailures++
			l.noteLinkFailure(err)
			st.KeptCopy++
			continue
		}
		st.Linked++
		st.BytesLinked += sfi.Size()
	}
	return st, nil
}

// unlinkServer is the rollback pass: every server VPK that shares an inode
// with master is atomically replaced by an independent copy.
func (l *vpkLinker) unlinkServer(ctx context.Context, masterGame, serverGame string, dryRun bool) (VPKStats, error) {
	var st VPKStats
	master, err := listVPKs(masterGame)
	if err != nil {
		return st, fmt.Errorf("list master VPKs: %w", err)
	}
	st.Files = len(master)
	for _, rel := range master {
		if err := vpkCheckCtx(ctx); err != nil {
			return st, err
		}
		src := filepath.Join(masterGame, filepath.FromSlash(rel))
		dst := filepath.Join(serverGame, filepath.FromSlash(rel))
		sfi, err := os.Stat(src)
		if err != nil {
			return st, err
		}
		dfi, err := os.Lstat(dst)
		if err != nil || !os.SameFile(sfi, dfi) {
			continue
		}
		if !dryRun {
			if err := replaceWithCopy(src, dst); err != nil {
				return st, fmt.Errorf("copy %s: %w", rel, err)
			}
		}
		st.Unlinked++
		st.BytesUnlinked += sfi.Size()
	}
	return st, nil
}

// linkedVPKBytes returns the bytes a server shares with master via hardlinks,
// i.e. what an undo would need to write.
func linkedVPKBytes(masterGame, serverGame string) (int64, error) {
	st, err := newVPKLinker(nil).unlinkServer(context.Background(), masterGame, serverGame, true)
	return st.BytesUnlinked, err
}

// diskUsage returns the allocated bytes of all files under roots, counting
// each inode once, so hardlinked VPKs are not double counted.
func diskUsage(roots ...string) (int64, error) {
	seen := make(map[inodeKey]struct{})
	var total int64
	for _, root := range roots {
		b, err := allocatedBytes(root, seen)
		total += b
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func formatGB(b int64) string {
	return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
}

// summary renders a one-line description of a sync pass.
func (s VPKStats) syncSummary() string {
	parts := []string{
		fmt.Sprintf("%d hardlinked", s.Linked),
		fmt.Sprintf("%d already linked", s.AlreadyLinked),
	}
	if s.Copied > 0 || s.KeptCopy > 0 {
		parts = append(parts, fmt.Sprintf("%d copied, %d existing copies kept (hardlink unavailable)", s.Copied, s.KeptCopy))
	}
	if s.Removed > 0 {
		parts = append(parts, fmt.Sprintf("%d stale removed", s.Removed))
	}
	return fmt.Sprintf("VPKs: %s (of %d)", strings.Join(parts, ", "), s.Files)
}

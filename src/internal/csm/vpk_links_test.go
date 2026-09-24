package csm

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// writeFile creates p (and parents) with content and a fixed mtime so copies
// made with preserved mtimes compare equal.
func writeFile(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}

var testMtime = time.Date(2026, 9, 15, 11, 32, 12, 121974339, time.UTC)

var masterVPKs = []string{
	"csgo/pak01_dir.vpk",
	"csgo/pak01_000.vpk",
	"csgo/maps/de_cache.vpk",
	"core/pak01_000.vpk",
}

// newMaster builds a fake master-install/game tree with VPKs and non-VPK files.
func newMaster(t *testing.T, root string) string {
	t.Helper()
	game := filepath.Join(root, "master-install", "game")
	for _, rel := range masterVPKs {
		writeFile(t, filepath.Join(game, rel), "vpk:"+rel, testMtime)
	}
	writeFile(t, filepath.Join(game, "csgo", "cfg", "server.cfg"), "hostname master", testMtime)
	writeFile(t, filepath.Join(game, "csgo", "gameinfo.gi"), "gameinfo", testMtime)
	return game
}

// copyTree simulates an old-style full copy of master into a server.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, b, fi.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(target, fi.ModTime(), fi.ModTime())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLinkServerVPKsCreatesHardlinks(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")

	var log bytes.Buffer
	st, err := newVPKLinker(&log).relinkServer(context.Background(), master, server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Linked != len(masterVPKs) || st.Copied != 0 || st.Files != len(masterVPKs) {
		t.Fatalf("unexpected stats: %+v", st)
	}
	for _, rel := range masterVPKs {
		if !sameInode(t, filepath.Join(master, rel), filepath.Join(server, rel)) {
			t.Errorf("%s is not hardlinked to master", rel)
		}
	}
	// Non-VPK files are not the linker's business.
	if _, err := os.Stat(filepath.Join(server, "csgo", "cfg", "server.cfg")); !os.IsNotExist(err) {
		t.Errorf("linker must not create non-VPK files, stat err=%v", err)
	}
	assertNoTempFiles(t, server)
}

func TestLinkFallsBackToCopyOnLinkError(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")

	var log bytes.Buffer
	l := newVPKLinker(&log)
	calls := 0
	l.link = func(oldname, newname string) error {
		calls++
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EXDEV}
	}
	st, err := l.relinkServer(context.Background(), master, server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != len(masterVPKs) || st.Linked != 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if calls != 1 {
		t.Errorf("EXDEV should disable further link attempts, got %d calls", calls)
	}
	for _, rel := range masterVPKs {
		src, dst := filepath.Join(master, rel), filepath.Join(server, rel)
		if sameInode(t, src, dst) {
			t.Errorf("%s should be an independent copy", rel)
		}
		if readFile(t, src) != readFile(t, dst) {
			t.Errorf("%s content mismatch", rel)
		}
		sfi, _ := os.Stat(src)
		dfi, _ := os.Stat(dst)
		if !sameMeta(sfi, dfi) {
			t.Errorf("%s copy did not preserve size/mtime", rel)
		}
	}
	if n := strings.Count(log.String(), "falling back to full copies"); n != 1 {
		t.Errorf("fallback should be logged once, got %d:\n%s", n, log.String())
	}

	// Second pass with identical copies present keeps them instead of copying again.
	st2, err := l.relinkServer(context.Background(), master, server)
	if err != nil {
		t.Fatal(err)
	}
	if st2.KeptCopy != len(masterVPKs) || st2.Copied != 0 {
		t.Fatalf("expected existing copies to be kept, got %+v", st2)
	}
	assertNoTempFiles(t, server)
}

func TestLinkFallbackOnPermissionErrorKeepsTrying(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")

	l := newVPKLinker(io.Discard)
	calls := 0
	l.link = func(oldname, newname string) error {
		calls++
		if calls == 1 {
			return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
		}
		return os.Link(oldname, newname)
	}
	st, err := l.relinkServer(context.Background(), master, server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 || st.Linked != len(masterVPKs)-1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestRelinkIsAtomicForOpenHandles(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)

	rel := "csgo/pak01_dir.vpk"
	// Simulate a SteamCMD update: master gets a new file (new inode) with new content.
	newMasterFile := filepath.Join(master, rel)
	tmp := newMasterFile + ".staged"
	writeFile(t, tmp, "vpk:updated", testMtime.Add(time.Hour))
	if err := os.Rename(tmp, newMasterFile); err != nil {
		t.Fatal(err)
	}

	// A "running server" holds the old file open.
	f, err := os.Open(filepath.Join(server, rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if _, err := newVPKLinker(io.Discard).relinkServer(context.Background(), master, server); err != nil {
		t.Fatal(err)
	}

	old, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "vpk:"+rel {
		t.Errorf("open handle should still read the old inode, got %q", old)
	}
	if got := readFile(t, filepath.Join(server, rel)); got != "vpk:updated" {
		t.Errorf("path should now resolve to master's new file, got %q", got)
	}
	if !sameInode(t, newMasterFile, filepath.Join(server, rel)) {
		t.Error("server file should be hardlinked to master's new inode")
	}
	assertNoTempFiles(t, server)
}

func TestReplaceWithLinkLeavesNoTempWhenAlreadyLinked(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.vpk")
	dst := filepath.Join(dir, "b.vpk")
	writeFile(t, src, "x", testMtime)
	if err := os.Link(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := newVPKLinker(nil).replaceWithLink(src, dst); err != nil {
		t.Fatal(err)
	}
	assertNoTempFiles(t, dir)
}

func TestPruneStaleVPKsSkipsAddonsAndNonVPK(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)
	writeFile(t, filepath.Join(server, "csgo", "maps", "de_removed.vpk"), "old", testMtime)
	writeFile(t, filepath.Join(server, "csgo", "addons", "plugin.vpk"), "addon", testMtime)
	writeFile(t, filepath.Join(server, "csgo", "AutoTournamentCS2", "backup.txt"), "keep", testMtime)

	st, err := newVPKLinker(io.Discard).relinkServer(context.Background(), master, server)
	if err != nil {
		t.Fatal(err)
	}
	if st.Removed != 1 {
		t.Fatalf("expected 1 stale VPK removed, got %+v", st)
	}
	if _, err := os.Stat(filepath.Join(server, "csgo", "maps", "de_removed.vpk")); !os.IsNotExist(err) {
		t.Error("stale VPK should be removed")
	}
	for _, keep := range []string{"csgo/addons/plugin.vpk", "csgo/AutoTournamentCS2/backup.txt"} {
		if _, err := os.Stat(filepath.Join(server, keep)); err != nil {
			t.Errorf("%s should be untouched: %v", keep, err)
		}
	}
}

func TestNonVPKFilesStayIndependent(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	s1 := filepath.Join(root, "server-1", "game")
	s2 := filepath.Join(root, "server-2", "game")
	for _, s := range []string{s1, s2} {
		copyTree(t, master, s)
		if _, err := newVPKLinker(io.Discard).dedupeServer(context.Background(), master, s, false, false); err != nil {
			t.Fatal(err)
		}
	}

	cfg := filepath.Join("csgo", "cfg", "server.cfg")
	if sameInode(t, filepath.Join(s1, cfg), filepath.Join(master, cfg)) {
		t.Fatal("server.cfg must not be hardlinked")
	}
	if err := os.WriteFile(filepath.Join(s1, cfg), []byte("hostname server-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(s2, cfg)); got != "hostname master" {
		t.Errorf("server-2 cfg changed: %q", got)
	}
	if got := readFile(t, filepath.Join(master, cfg)); got != "hostname master" {
		t.Errorf("master cfg changed: %q", got)
	}
	for _, rel := range masterVPKs {
		if !sameInode(t, filepath.Join(s1, rel), filepath.Join(s2, rel)) {
			t.Errorf("%s should be shared between servers", rel)
		}
	}
}

func TestDedupeIsIdempotentAndSkipsDifferentFiles(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)

	// Different size.
	writeFile(t, filepath.Join(server, "csgo", "pak01_000.vpk"), "locally modified!", testMtime)
	// Same size, different mtime.
	writeFile(t, filepath.Join(server, "core", "pak01_000.vpk"), "vpk:core/pak01_000.vpk", testMtime.Add(time.Second))
	// Missing.
	if err := os.Remove(filepath.Join(server, "csgo", "maps", "de_cache.vpk")); err != nil {
		t.Fatal(err)
	}

	l := newVPKLinker(io.Discard)
	st, err := l.dedupeServer(context.Background(), master, server, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Linked != 1 || st.Differs != 2 || st.Missing != 1 {
		t.Fatalf("unexpected first-run stats: %+v", st)
	}
	if got := readFile(t, filepath.Join(server, "csgo", "pak01_000.vpk")); got != "locally modified!" {
		t.Errorf("differing file must be left alone, got %q", got)
	}

	st2, err := l.dedupeServer(context.Background(), master, server, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Linked != 0 || st2.AlreadyLinked != 1 || st2.Differs != 2 || st2.Missing != 1 {
		t.Fatalf("second run should be a no-op, got %+v", st2)
	}
	if _, err := os.Stat(filepath.Join(server, "csgo", "maps", "de_cache.vpk")); !os.IsNotExist(err) {
		t.Error("dedupe must not create missing files")
	}
	assertNoTempFiles(t, server)
}

func TestDedupeVerifyCatchesSameMetaDifferentBytes(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)
	rel := "csgo/pak01_dir.vpk"
	writeFile(t, filepath.Join(server, rel), "vpk:csgo/pak01_XXX.vpk", testMtime) // same length

	st, err := newVPKLinker(io.Discard).dedupeServer(context.Background(), master, server, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Differs != 1 || st.Linked != len(masterVPKs)-1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if sameInode(t, filepath.Join(master, rel), filepath.Join(server, rel)) {
		t.Error("byte-different file must not be linked with --verify")
	}
}

func TestDedupeDryRunChangesNothing(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)

	st, err := newVPKLinker(io.Discard).dedupeServer(context.Background(), master, server, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Linked != len(masterVPKs) || st.BytesLinked == 0 {
		t.Fatalf("dry run should report would-link counts, got %+v", st)
	}
	for _, rel := range masterVPKs {
		if sameInode(t, filepath.Join(master, rel), filepath.Join(server, rel)) {
			t.Errorf("dry run linked %s", rel)
		}
	}
}

func TestDedupeLinkFailureKeepsCopy(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)

	var log bytes.Buffer
	l := newVPKLinker(&log)
	l.link = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: syscall.EPERM}
	}
	st, err := l.dedupeServer(context.Background(), master, server, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.KeptCopy != len(masterVPKs) || st.Linked != 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	for _, rel := range masterVPKs {
		if readFile(t, filepath.Join(server, rel)) != "vpk:"+rel {
			t.Errorf("%s damaged after failed link", rel)
		}
	}
	assertNoTempFiles(t, server)
}

func TestUndoRestoresIndependentCopies(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	if _, err := newVPKLinker(io.Discard).relinkServer(context.Background(), master, server); err != nil {
		t.Fatal(err)
	}
	need, err := linkedVPKBytes(master, server)
	if err != nil || need == 0 {
		t.Fatalf("linkedVPKBytes = %d, %v", need, err)
	}
	st, err := newVPKLinker(io.Discard).unlinkServer(context.Background(), master, server, false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Unlinked != len(masterVPKs) || st.BytesUnlinked != need {
		t.Fatalf("unexpected stats: %+v (need %d)", st, need)
	}
	for _, rel := range masterVPKs {
		if sameInode(t, filepath.Join(master, rel), filepath.Join(server, rel)) {
			t.Errorf("%s still linked after undo", rel)
		}
		if readFile(t, filepath.Join(server, rel)) != "vpk:"+rel {
			t.Errorf("%s content wrong after undo", rel)
		}
	}
	st2, _ := newVPKLinker(io.Discard).unlinkServer(context.Background(), master, server, false)
	if st2.Unlinked != 0 {
		t.Errorf("second undo should be a no-op, got %+v", st2)
	}
}

func TestDiskUsageCountsHardlinksOnce(t *testing.T) {
	root := t.TempDir()
	master := newMaster(t, root)
	server := filepath.Join(root, "server-1", "game")
	copyTree(t, master, server)
	// Make one VPK big enough to have allocated blocks.
	big := strings.Repeat("x", 1<<20)
	writeFile(t, filepath.Join(master, "csgo", "pak01_000.vpk"), big, testMtime)
	writeFile(t, filepath.Join(server, "csgo", "pak01_000.vpk"), big, testMtime)

	before, err := diskUsage(master, server)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newVPKLinker(io.Discard).dedupeServer(context.Background(), master, server, false, false); err != nil {
		t.Fatal(err)
	}
	after, err := diskUsage(master, server)
	if err != nil {
		t.Fatal(err)
	}
	if before-after < 1<<20 {
		t.Errorf("expected at least 1 MiB saved, before=%d after=%d", before, after)
	}
}

func TestRunDedupeVPKsReportsAndRefusesRunning(t *testing.T) {
	root := t.TempDir()
	newMaster(t, root)
	masterDir := filepath.Join(root, "master-install")
	s1 := filepath.Join(root, "server-1")
	copyTree(t, filepath.Join(masterDir, "game"), filepath.Join(s1, "game"))
	targets := []dedupeTarget{{num: 1, game: filepath.Join(s1, "game")}}

	var buf bytes.Buffer
	_, err := runDedupeVPKs(context.Background(), &buf, &buf, masterDir, []string{s1}, targets, DedupeVPKOptions{}, func(int) bool { return true })
	if err == nil || !strings.Contains(buf.String(), "--allow-running") {
		t.Fatalf("expected refusal while running, err=%v out=%s", err, buf.String())
	}

	buf.Reset()
	out, err := runDedupeVPKs(context.Background(), &buf, &buf, masterDir, []string{s1}, targets, DedupeVPKOptions{}, func(int) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Before:", "after:", "saved", "server-1: linked 4"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	for _, rel := range masterVPKs {
		if !sameInode(t, filepath.Join(masterDir, "game", rel), filepath.Join(s1, "game", rel)) {
			t.Errorf("%s not linked", rel)
		}
	}
}

func TestRsyncArgsExcludeVPK(t *testing.T) {
	has := func(args []string) bool {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--exclude" && args[i+1] == "*.vpk" {
				return true
			}
		}
		return false
	}
	if !has(rsyncArgsLegacyCopy("a/", "b/", false, true)) || !has(rsyncArgsTunedCopy("a/", "b/", false, "u", true)) {
		t.Error("rsync args must exclude *.vpk when hardlinking")
	}
	if has(rsyncArgsLegacyCopy("a/", "b/", false, false)) || has(rsyncArgsTunedCopy("a/", "b/", false, "u", false)) {
		t.Error("rsync args must not exclude *.vpk when hardlinking is disabled")
	}
	for _, a := range append(rsyncArgsLegacyCopy("a/", "b/", true, true), rsyncArgsTunedCopy("a/", "b/", true, "u", true)...) {
		if a == "--inplace" {
			t.Error("--inplace would write through hardlinks")
		}
	}
}

// TestCopyMasterGameToServerGameWithRsync exercises the real sync path
// (prune -> rsync without VPKs -> link) when rsync is installed.
func TestCopyMasterGameToServerGameWithRsync(t *testing.T) {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	t.Setenv("CSM_COPY_MODE", "legacy")
	t.Setenv("CSM_VPK_HARDLINK", "")

	root := t.TempDir()
	master := newMaster(t, root)
	masterDir := filepath.Dir(master)
	s1 := filepath.Join(root, "server-1", "game")
	s2 := filepath.Join(root, "server-2", "game")

	for _, s := range []string{s1, s2} {
		// Callers create server-N/game before syncing; rsync only creates the last level.
		if err := os.MkdirAll(s, 0o755); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := copyMasterGameToServerGame(context.Background(), &out, "", masterDir, s, false, false); err != nil {
			t.Fatalf("sync failed: %v\n%s", err, out.String())
		}
	}
	for _, rel := range masterVPKs {
		if !sameInode(t, filepath.Join(master, rel), filepath.Join(s1, rel)) {
			t.Errorf("%s not hardlinked into server-1", rel)
		}
	}
	cfg := filepath.Join("csgo", "cfg", "server.cfg")
	if sameInode(t, filepath.Join(master, cfg), filepath.Join(s1, cfg)) {
		t.Fatal("server.cfg must be a real copy")
	}
	if err := os.WriteFile(filepath.Join(s1, cfg), []byte("hostname one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(s2, cfg)) != "hostname master" || readFile(t, filepath.Join(master, cfg)) != "hostname master" {
		t.Error("editing server-1 cfg leaked into other trees")
	}

	// Simulate a game update: one VPK replaced (new inode), one removed, one added.
	upd := filepath.Join(master, "csgo", "pak01_dir.vpk")
	writeFile(t, upd+".staged", "vpk:new-dir", testMtime.Add(time.Hour))
	if err := os.Rename(upd+".staged", upd); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(master, "csgo", "maps", "de_cache.vpk")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(master, "csgo", "maps", "de_new.vpk"), "vpk:new-map", testMtime)

	var out bytes.Buffer
	if err := copyMasterGameToServerGame(context.Background(), &out, "", masterDir, s1, false, false); err != nil {
		t.Fatalf("resync failed: %v\n%s", err, out.String())
	}
	if !sameInode(t, upd, filepath.Join(s1, "csgo", "pak01_dir.vpk")) {
		t.Error("updated VPK not relinked")
	}
	if !sameInode(t, filepath.Join(master, "csgo", "maps", "de_new.vpk"), filepath.Join(s1, "csgo", "maps", "de_new.vpk")) {
		t.Error("new VPK not linked")
	}
	if _, err := os.Stat(filepath.Join(s1, "csgo", "maps", "de_cache.vpk")); !os.IsNotExist(err) {
		t.Error("VPK removed from master should be removed from server")
	}
	// server-2 was not resynced: it still has the old inode, unchanged.
	if got := readFile(t, filepath.Join(s2, "csgo", "pak01_dir.vpk")); got != "vpk:csgo/pak01_dir.vpk" {
		t.Errorf("server-2 VPK changed by master update: %q", got)
	}
	if !strings.Contains(out.String(), "VPKs:") {
		t.Errorf("expected VPK summary in output:\n%s", out.String())
	}
	assertNoTempFiles(t, s1)
}

func assertNoTempFiles(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, vpkTempSuffix) {
			t.Errorf("leftover temp file %s", p)
		}
		return nil
	})
}

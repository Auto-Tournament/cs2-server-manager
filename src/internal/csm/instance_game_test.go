package csm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func inode(t *testing.T, p string) uint64 {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

func readInst(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSteamcmdOverlayCmdline(t *testing.T) {
	c, err := steamcmdOverlayCmdline("/usr/games/steamcmd", "/h/master-install", "/i/games/.update-x/upper", "/i/games/.update-x/work", "/i/games/.update-x/mnt", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unshare --user --map-root-user --mount",
		"lowerdir=/h/master-install,upperdir=/i/games/.update-x/upper,workdir=/i/games/.update-x/work,userxattr,metacopy=off",
		"+force_install_dir", "/i/games/.update-x/mnt", "+app_update", "730", "validate", "+quit", "nice -n 10",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("cmdline lacks %q: %s", want, c)
		}
	}
	c, _ = steamcmdOverlayCmdline("steamcmd", "/l", "/u", "/w", "/m", false)
	if strings.Contains(c, "validate") {
		t.Fatal("validate off still validates")
	}
	if _, err := steamcmdOverlayCmdline("steamcmd", "/l,x", "/u", "/w", "/m", false); err == nil {
		t.Fatal("a comma in the lower path was accepted")
	}
}

// fakeVersion lays out a minimal CS2 install.
func fakeVersion(t *testing.T, dir string, build string) {
	t.Helper()
	writeInstFile(t, filepath.Join(dir, "game/bin/linuxsteamrt64/cs2"), "binary")
	writeInstFile(t, filepath.Join(dir, "game/csgo/steam.inf"), "ServerVersion="+build+"\n")
	writeInstFile(t, filepath.Join(dir, "game/csgo/pak01_dir.vpk"), "big vpk")
	writeInstFile(t, filepath.Join(dir, "game/csgo/cfg/gamemode_competitive.cfg"), "mp_maxrounds 24\n")
	writeInstFile(t, filepath.Join(dir, "game/csgo/old.txt"), "going away")
	writeInstFile(t, filepath.Join(dir, "game/csgo/replaced/a.txt"), "a")
	writeInstFile(t, filepath.Join(dir, "game/csgo/replaced/b.txt"), "b")
	writeInstFile(t, filepath.Join(dir, "steamapps/appmanifest_730.acf"), "\"buildid\" \""+build+"\"")
}

func TestApplyOverlayUpper(t *testing.T) {
	d := t.TempDir()
	src, dst, upper := filepath.Join(d, "src"), filepath.Join(d, "dst"), filepath.Join(d, "upper")
	fakeVersion(t, src, "100")
	if err := runUserCmdline(context.Background(), currentUsername(), "cp -al "+shellQuote(src)+" "+shellQuote(dst), io.Discard); err != nil {
		t.Fatalf("cp -al: %v", err)
	}
	cfg := "game/csgo/cfg/gamemode_competitive.cfg"
	if inode(t, filepath.Join(src, cfg)) != inode(t, filepath.Join(dst, cfg)) {
		t.Fatal("cp -al did not hardlink")
	}
	// What SteamCMD left in the overlay: a changed file (copied up, then
	// truncated/extended in place), a new file in a new directory, and an
	// opaque (replaced) directory.
	writeInstFile(t, filepath.Join(upper, cfg), "mp_maxrounds 30\n")
	writeInstFile(t, filepath.Join(upper, "game/csgo/steam.inf"), "ServerVersion=101\n")
	writeInstFile(t, filepath.Join(upper, "game/csgo/newdir/new.txt"), "new")
	writeInstFile(t, filepath.Join(upper, "game/csgo/replaced/c.txt"), "c")
	opaque := syscall.Setxattr(filepath.Join(upper, "game/csgo/replaced"), "user.overlay.opaque", []byte("y"), 0) == nil
	whiteout := syscall.Mknod(filepath.Join(upper, "game/csgo/old.txt"), syscall.S_IFCHR, 0) == nil

	created, err := applyOverlayUpper(upper, dst)
	if err != nil {
		t.Fatal(err)
	}
	wantCreated := 1 // newdir
	if opaque {
		wantCreated = 2 // and the replaced directory, made anew
	}
	if len(created) != wantCreated || !strings.HasSuffix(created[0], "game/csgo/newdir") {
		t.Fatalf("created = %v", created)
	}
	if got := readInst(t, filepath.Join(dst, cfg)); got != "mp_maxrounds 30\n" {
		t.Fatalf("dst cfg = %q", got)
	}
	// The old version is never written through the shared link.
	if got := readInst(t, filepath.Join(src, cfg)); got != "mp_maxrounds 24\n" {
		t.Fatalf("src cfg changed: %q", got)
	}
	if gameBuild(src) != 100 || gameBuild(dst) != 101 {
		t.Fatalf("builds src %d dst %d", gameBuild(src), gameBuild(dst))
	}
	if inode(t, filepath.Join(src, "game/csgo/pak01_dir.vpk")) != inode(t, filepath.Join(dst, "game/csgo/pak01_dir.vpk")) {
		t.Fatal("an unchanged file is no longer shared")
	}
	if readInst(t, filepath.Join(dst, "game/csgo/newdir/new.txt")) != "new" {
		t.Fatal("new file missing")
	}
	if opaque {
		if _, err := os.Stat(filepath.Join(dst, "game/csgo/replaced/a.txt")); !os.IsNotExist(err) {
			t.Fatal("opaque directory kept the old contents")
		}
		if _, err := os.Stat(filepath.Join(src, "game/csgo/replaced/a.txt")); err != nil {
			t.Fatal("opaque directory emptied the old version")
		}
	} else {
		t.Log("no user xattrs here: opaque directory case skipped")
	}
	if readInst(t, filepath.Join(dst, "game/csgo/replaced/c.txt")) != "c" {
		t.Fatal("file in the replaced directory missing")
	}
	if whiteout {
		if _, err := os.Stat(filepath.Join(dst, "game/csgo/old.txt")); !os.IsNotExist(err) {
			t.Fatal("whiteout did not delete")
		}
		if _, err := os.Stat(filepath.Join(src, "game/csgo/old.txt")); err != nil {
			t.Fatal("whiteout deleted from the old version")
		}
	} else {
		t.Log("cannot mknod a whiteout unprivileged: case covered by scripts/instance-integration-test.sh")
	}
}

func TestOverlayChanges(t *testing.T) {
	upper := t.TempDir()
	writeInstFile(t, filepath.Join(upper, "steamapps/appmanifest_730.acf"), "x")
	writeInstFile(t, filepath.Join(upper, "steamapps/downloading/730/x"), "x")
	if err := os.MkdirAll(filepath.Join(upper, "game/csgo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if c, r, err := overlayChanges(upper); err != nil || c != 0 || r != 0 {
		t.Fatalf("steamapps-only upper = %d %d %v (want no changes)", c, r, err)
	}
	writeInstFile(t, filepath.Join(upper, "game/csgo/steam.inf"), "x")
	if c, _, _ := overlayChanges(upper); c != 1 {
		t.Fatalf("changed = %d", c)
	}
}

// fakeSteamcmd stands in for SteamCMD: it writes what an update would leave
// in the overlay's upper directory.
func fakeSteamcmd(t *testing.T, m *InstanceManager, change func(upper string)) func() {
	t.Helper()
	bin := t.TempDir()
	writeInstFile(t, filepath.Join(bin, "steamcmd"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(bin, "steamcmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	real := runUserCmdline
	runUserCmdline = func(ctx context.Context, user, cmdline string, w io.Writer) error {
		if !strings.Contains(cmdline, "+app_update") {
			return real(ctx, user, cmdline, w)
		}
		ups, _ := filepath.Glob(filepath.Join(m.L.GamesDir(), ".update-*", "upper"))
		if len(ups) != 1 {
			t.Fatalf("update dirs = %v", ups)
		}
		change(ups[0])
		return nil
	}
	return func() { runUserCmdline = real }
}

func TestUpdateGameVersion(t *testing.T) {
	m := testInstanceManager(t)
	fakeVersion(t, m.L.Master, "100")
	if m.CurrentGame() != m.L.Master || m.MasterBuild() != 100 {
		t.Fatalf("before any update: %s %d", m.CurrentGame(), m.MasterBuild())
	}

	// No update available: nothing but SteamCMD's own bookkeeping changes.
	restore := fakeSteamcmd(t, m, func(upper string) {
		writeInstFile(t, filepath.Join(upper, "steamapps/appmanifest_730.acf"), "touched")
	})
	dir, err := m.updateGameVersion(context.Background(), io.Discard)
	restore()
	if err != nil || dir != "" || len(m.ListGames()) != 0 {
		t.Fatalf("no-op update made %q %v %v", dir, err, m.ListGames())
	}

	restore = fakeSteamcmd(t, m, func(upper string) {
		writeInstFile(t, filepath.Join(upper, "game/csgo/steam.inf"), "ServerVersion=101\n")
		writeInstFile(t, filepath.Join(upper, "game/csgo/cfg/gamemode_competitive.cfg"), "mp_maxrounds 30\n")
	})
	defer restore()
	dir, err = m.updateGameVersion(context.Background(), io.Discard)
	if err != nil || dir == "" {
		t.Fatalf("update: %q %v", dir, err)
	}
	if m.CurrentGame() != dir || m.MasterBuild() != 101 {
		t.Fatalf("current = %s build %d", m.CurrentGame(), m.MasterBuild())
	}
	if gameBuild(m.L.Master) != 100 || readInst(t, filepath.Join(m.L.Master, "game/csgo/cfg/gamemode_competitive.cfg")) != "mp_maxrounds 24\n" {
		t.Fatal("the master install was written")
	}
	if inode(t, filepath.Join(m.L.Master, "game/csgo/pak01_dir.vpk")) != inode(t, filepath.Join(dir, "game/csgo/pak01_dir.vpk")) {
		t.Fatal("unchanged files are not hardlinked")
	}
	info := m.ReadGameInfo(dir)
	if info.Build != 101 || info.Copy != "hardlink" || info.Changed != 2 || info.From != m.L.Master {
		t.Fatalf("info = %+v", info)
	}
	if left, _ := filepath.Glob(filepath.Join(m.L.GamesDir(), ".*-*")); len(left) != 0 {
		t.Fatalf("work directories left behind: %v", left)
	}
	if !strings.Contains(m.gameVersionsReport(), info.ID) {
		t.Fatal("report lacks the new version")
	}

	// The next update starts from the new version, not the master.
	dir2, err := m.updateGameVersion(context.Background(), io.Discard)
	if err != nil || dir2 == "" || m.ReadGameInfo(dir2).From != dir {
		t.Fatalf("second update: %q %v %+v", dir2, err, m.ReadGameInfo(dir2))
	}
}

func TestLayerGameAndGC(t *testing.T) {
	m := testInstanceManager(t)
	fakeVersion(t, m.L.Master, "100")
	mkGame := func(id, build string) string {
		d := filepath.Join(m.L.GamesDir(), id)
		fakeVersion(t, d, build)
		if err := writeJSONAtomic(d+".json", GameInfo{ID: id}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	mkLayer := func(id, game string) string {
		d := filepath.Join(m.L.LayersDir(), id)
		writeInstFile(t, filepath.Join(d, "game/csgo/readyup/installed.json"), `{"components":{"core":"0.1.0"}}`)
		if err := writeJSONAtomic(d+".json", LayerInfo{ID: id, MasterBuild: gameBuild(game)}); err != nil {
			t.Fatal(err)
		}
		if game != "" {
			writeInstFile(t, layerGameFile(d), game+"\n")
		}
		return d
	}
	g1, g2, g3, g4 := mkGame("20260101-000000", "101"), mkGame("20260102-000000", "102"), mkGame("20260103-000000", "103"), mkGame("20260104-000000", "104")
	legacy := mkLayer("20260101-000000", "") // built before game versions: on the master
	if m.layerGame(legacy) != m.L.Master {
		t.Fatalf("legacy layer game = %s", m.layerGame(legacy))
	}
	mkLayer("20260102-000000", g1)
	mkLayer("20260103-000000", g2) // previous layer, older game: no rollback target
	l4 := mkLayer("20260104-000000", g3)
	if err := m.setCurrentLayer(filepath.Base(l4)); err != nil {
		t.Fatal(err)
	}
	if err := m.setCurrentGame(filepath.Base(g3)); err != nil {
		t.Fatal(err)
	}
	if m.CurrentGame() != g3 || m.currentLayerBuild() != 103 {
		t.Fatalf("current game %s build %d", m.CurrentGame(), m.currentLayerBuild())
	}
	if stale, _ := m.layerStale(l4); stale {
		t.Fatal("layer on the current game reported stale")
	}
	if stale, why := m.layerStale(filepath.Join(m.L.LayersDir(), "20260103-000000")); !stale || !strings.Contains(why, "20260102-000000") {
		t.Fatalf("layer on an old game: %v %q", stale, why)
	}

	// Only the current layer survives (the others sit on older game
	// versions), so only its game version and the newer g4 stay.
	m.GC(io.Discard)
	for _, gone := range []string{g2} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s not collected", filepath.Base(gone))
		}
	}
	for _, kept := range []string{g3, g4, m.L.Master} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s collected: %v", kept, err)
		}
	}
	// g1 went too: no layer or running instance uses it once its layer is gone.
	if _, err := os.Stat(g1); !os.IsNotExist(err) {
		t.Fatal("g1 not collected")
	}
	left := m.ListLayers()
	if len(left) != 1 || filepath.Base(left[0]) != "20260104-000000" {
		t.Fatalf("layers after gc = %v", left)
	}
	if _, err := os.Stat(layerGameFile(legacy)); !os.IsNotExist(err) {
		t.Fatal("gc left a removed layer's .game file")
	}
}

func TestGameInUse(t *testing.T) {
	m := testInstanceManager(t)
	writeInstFile(t, m.L.InUseFile(3), "/i/layers/L2\n/i/games/G2\n")
	if m.LayerInUse(3) != "/i/layers/L2" || m.GameInUse(3) != "/i/games/G2" {
		t.Fatalf("in use: %q %q", m.LayerInUse(3), m.GameInUse(3))
	}
	// Written by an older csm: the layer only, which sat on the master.
	writeInstFile(t, m.L.InUseFile(4), "/i/layers/L1\n")
	if m.LayerInUse(4) != "/i/layers/L1" || m.GameInUse(4) != m.L.Master {
		t.Fatalf("legacy in use: %q %q", m.LayerInUse(4), m.GameInUse(4))
	}
	if m.GameInUse(5) != "" {
		t.Fatal("no in-use file must be unknown")
	}
}

func TestNewGameID(t *testing.T) {
	m := testInstanceManager(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	if err := os.MkdirAll(filepath.Join(m.L.GamesDir(), "20260929-120000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if id := m.newGameID(now); id != "20260929-120000-2" {
		t.Fatalf("id = %s", id)
	}
}

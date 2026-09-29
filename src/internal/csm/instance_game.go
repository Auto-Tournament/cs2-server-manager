package csm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// CS2 game versions (instance mode)
//
// Running instances mount the CS2 install as a lower overlay layer, and
// overlayfs does not define what a process sees when a lower layer changes
// under it. So csm never runs SteamCMD on an install an instance may have
// mounted. A CS2 update makes a new game version instead:
//
//  1. SteamCMD runs against a temporary overlay whose lower layer is the
//     current version and whose upper layer is empty, so every file SteamCMD
//     writes, replaces, truncates or deletes lands in the upper layer (as a
//     copy-up or a whiteout) and the current version is never written.
//     A hardlinked copy alone is not enough: SteamCMD replaces most changed
//     files with new ones, but trims a file that only lost trailing bytes in
//     place (checked with app 1007: same inode, both links changed).
//  2. The new version is a hardlinked copy of the current one (`cp -al`,
//     unchanged files cost nothing) with that upper layer applied on top:
//     changed files are moved in (replacing the link, never writing through
//     it), deleted ones removed. Where hardlinks cannot be made (another
//     filesystem, or files owned by another user with protected_hardlinks)
//     it is a full copy (reflinked where the filesystem can).
//  3. The Ready Up layer is rebuilt on the new version (gameinfo.gi comes
//     from it) and records which version it sits on (layers/<id>.game), so
//     an instance always mounts a layer together with its own game version.
//
// Idle instances are then restarted onto the new layer; busy ones keep their
// old layer and game version until csm monitor finds them idle. Versions live
// in instances/games/<id>/ (games/current is the newest); master-install is
// only the first one and is never written by instance mode. A version is
// removed once no layer and no running instance uses it.

// GameInfo is games/<id>.json.
type GameInfo struct {
	ID        string `json:"id"`
	Build     int64  `json:"build,omitempty"`
	From      string `json:"from"`
	Copy      string `json:"copy"` // hardlink | copy
	Changed   int    `json:"changed_files"`
	Removed   int    `json:"removed_files"`
	CreatedAt string `json:"created_at"`
}

func (l InstanceLayout) GamesDir() string        { return filepath.Join(l.Root, "games") }
func (l InstanceLayout) CurrentGameLink() string { return filepath.Join(l.GamesDir(), "current") }

// gameBuild is a CS2 install's ServerVersion (0 = unknown).
func gameBuild(dir string) int64 {
	return steamInfServerVersion(filepath.Join(dir, "game", "csgo", "steam.inf"))
}

// CurrentGame is the game version new layers are built on: games/current,
// or the master install before the first update.
func (m *InstanceManager) CurrentGame() string {
	if t, err := os.Readlink(m.L.CurrentGameLink()); err == nil {
		if !filepath.IsAbs(t) {
			t = filepath.Join(m.L.GamesDir(), t)
		}
		if fi, err := os.Stat(t); err == nil && fi.IsDir() {
			return filepath.Clean(t)
		}
	}
	return m.L.Master
}

// ListGames returns the game versions under games/ (not the master), oldest
// first.
func (m *InstanceManager) ListGames() []string {
	entries, err := os.ReadDir(m.L.GamesDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "current" {
			continue
		}
		out = append(out, filepath.Join(m.L.GamesDir(), e.Name()))
	}
	sort.Strings(out)
	return out
}

// ReadGameInfo reads games/<id>.json.
func (m *InstanceManager) ReadGameInfo(dir string) GameInfo {
	info := GameInfo{ID: filepath.Base(dir)}
	if data, err := os.ReadFile(dir + ".json"); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	if dir == m.L.Master {
		info.ID = "master"
	}
	if info.Build == 0 {
		info.Build = gameBuild(dir)
	}
	return info
}

// layerGameFile is where a layer records its game version.
func layerGameFile(layer string) string { return filepath.Clean(layer) + ".game" }

// layerGame is the game version a layer was built on (layers built before
// game versions existed sit on the master install).
func (m *InstanceManager) layerGame(layer string) string {
	data, err := os.ReadFile(layerGameFile(layer))
	if err != nil {
		return m.L.Master
	}
	if p := strings.TrimSpace(string(data)); p != "" {
		return filepath.Clean(p)
	}
	return m.L.Master
}

// GameInUse is the game version running instance n mounted ("" = unknown).
func (m *InstanceManager) GameInUse(n int) string {
	data, err := os.ReadFile(m.L.InUseFile(n))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		// Written by an older csm: the layer only, on the master.
		if len(lines) == 1 && lines[0] != "" {
			return m.layerGame(lines[0])
		}
		return ""
	}
	return filepath.Clean(strings.TrimSpace(lines[1]))
}

// setCurrentGame points games/current at id.
func (m *InstanceManager) setCurrentGame(id string) error {
	tmp := m.L.CurrentGameLink() + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(id, tmp); err != nil {
		return err
	}
	if canChown() {
		_ = exec.Command("chown", "-h", m.L.User+":"+m.L.User, tmp).Run()
	}
	return os.Rename(tmp, m.L.CurrentGameLink())
}

// runUserCmdline runs a shell command line as the CS2 user, killed when ctx
// ends. A variable so tests can stand in for SteamCMD.
var runUserCmdline = func(ctx context.Context, user, cmdline string, w io.Writer) error {
	cmd := userShellCommand(user, cmdline)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ctx.Err()
	}
}

// steamcmdOverlayCmdline mounts lower (read-only) + upper at mnt in a fresh
// user+mount namespace and runs SteamCMD against mnt. userxattr keeps the
// overlay's own metadata in user.* xattrs; metacopy stays off (and an
// unprivileged mount takes no redirect_dir=on), so every change is a plain
// file, directory or whiteout in upper; applyOverlayUpper refuses anything
// else.
func steamcmdOverlayCmdline(steamcmd, lower, upper, work, mnt string, validate bool) (string, error) {
	opts, err := overlayMountOptions([]string{lower}, upper, work)
	if err != nil {
		return "", err
	}
	if err := overlayPathOK(mnt); err != nil {
		return "", err
	}
	opts += ",userxattr,metacopy=off"
	args := []string{steamcmd, "+force_install_dir", mnt, "+login", "anonymous", "+app_update", "730"}
	if validate {
		args = append(args, "validate")
	}
	args = append(args, "+quit")
	for i, a := range args {
		args[i] = shellQuote(a)
	}
	inner := fmt.Sprintf("set -e; mount -t overlay overlay -o %s %s; exec nice -n 10 %s", shellQuote(opts), shellQuote(mnt), strings.Join(args, " "))
	return strings.Join(unshareArgv(), " ") + " bash -c " + shellQuote(inner), nil
}

// overlayChanges counts what an overlay upper layer changes outside
// steamapps/ (SteamCMD's own bookkeeping).
func overlayChanges(upper string) (changed, removed int, err error) {
	err = filepath.WalkDir(upper, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(upper, p)
		rel = filepath.ToSlash(rel)
		if rel == "steamapps" || strings.HasPrefix(rel, "steamapps/") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if isWhiteoutInfo(fi) {
			removed++
		} else {
			changed++
		}
		return nil
	})
	return changed, removed, err
}

func isWhiteoutInfo(fi fs.FileInfo) bool {
	var rdev uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		rdev = uint64(st.Rdev)
	}
	return isWhiteout(fi.Mode(), rdev)
}

// overlayXattr reads an overlay xattr (user.overlay.* under userxattr).
func overlayXattr(p, name string) string {
	buf := make([]byte, 256)
	n, err := syscall.Getxattr(p, "user.overlay."+name, buf)
	if err != nil || n <= 0 {
		return ""
	}
	return string(buf[:n])
}

// applyOverlayUpper makes dst (a copy of the overlay's lower layer) equal to
// the merged view: files and symlinks of upper are moved in (a hardlinked
// file in dst is replaced, never written through), whiteouts delete, opaque
// directories replace. created lists the directories it made.
func applyOverlayUpper(upper, dst string) (created []string, err error) {
	err = filepath.WalkDir(upper, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == upper {
			return nil
		}
		rel, err := filepath.Rel(upper, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case isWhiteoutInfo(fi):
			return os.RemoveAll(target)
		case fi.IsDir():
			if overlayXattr(p, "redirect") != "" {
				return fmt.Errorf("%s: overlay redirect (redirect_dir must be off)", rel)
			}
			tfi, terr := os.Lstat(target)
			if terr == nil && (!tfi.IsDir() || overlayXattr(p, "opaque") == "y") {
				if err := removeTreeForce(target); err != nil {
					return err
				}
				terr = os.ErrNotExist
			}
			if terr != nil {
				if err := os.Mkdir(target, fi.Mode().Perm()); err != nil {
					return err
				}
				created = append(created, target)
			}
			return os.Chmod(target, fi.Mode().Perm())
		default:
			if overlayXattr(p, "metacopy") != "" {
				return fmt.Errorf("%s: overlay metacopy file (metacopy must be off)", rel)
			}
			if tfi, err := os.Lstat(target); err == nil {
				if tfi.IsDir() {
					err = removeTreeForce(target)
				} else {
					err = os.Remove(target)
				}
				if err != nil {
					return err
				}
			}
			return os.Rename(p, target)
		}
	})
	return created, err
}

// treeSize is the apparent size of the regular files under dir, each inode
// counted once.
func treeSize(dir string) int64 {
	var total int64
	seen := map[uint64]bool{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			if seen[st.Ino] {
				return nil
			}
			seen[st.Ino] = true
		}
		total += fi.Size()
		return nil
	})
	return total
}

// copyGameVersion makes dst a copy of src: hardlinks where possible, a full
// (reflinked where possible) copy otherwise. It reports which one it made.
func (m *InstanceManager) copyGameVersion(ctx context.Context, w io.Writer, src, dst string) (string, error) {
	var out strings.Builder
	err := runUserCmdline(ctx, m.L.User, fmt.Sprintf("cp -al -- %s %s", shellQuote(src), shellQuote(dst)), &out)
	if err == nil {
		return "hardlink", nil
	}
	msg := strings.TrimSpace(out.String())
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i] + " ..."
	}
	fmt.Fprintf(w, "[CS2 update] no hardlinked copy of %s (%v: %s); making a full copy.\n", src, err, msg)
	if err := removeTreeForce(dst); err != nil {
		return "", err
	}
	if err := requireFreeDiskGB(filepath.Dir(dst), float64(treeSize(src))*1.05/(1<<30)); err != nil {
		return "", fmt.Errorf("a full copy of %s: %w", src, err)
	}
	out.Reset()
	if err := runUserCmdline(ctx, m.L.User, fmt.Sprintf("cp -a --reflink=auto -- %s %s", shellQuote(src), shellQuote(dst)), &out); err != nil {
		return "", fmt.Errorf("copy %s: %v %s", src, err, strings.TrimSpace(out.String()))
	}
	return "copy", nil
}

// updateGameVersion runs SteamCMD for the current game version and, when it
// changed anything, makes the result the new current version. It returns
// the new version's directory ("" when CS2 was already up to date). The
// caller holds the game update lock.
func (m *InstanceManager) updateGameVersion(ctx context.Context, w io.Writer) (string, error) {
	src := m.CurrentGame()
	if _, err := os.Stat(filepath.Join(src, "game", "csgo")); err != nil {
		return "", fmt.Errorf("no CS2 install at %s: %w", src, err)
	}
	for _, p := range []string{src, m.L.GamesDir()} {
		if err := overlayPathOK(p); err != nil {
			return "", err
		}
	}
	steamcmd, err := exec.LookPath("steamcmd")
	if err != nil {
		return "", errors.New("steamcmd not found on PATH")
	}
	if err := m.mkdirs(m.L.GamesDir()); err != nil {
		return "", err
	}
	id := m.newGameID(time.Now())
	build := filepath.Join(m.L.GamesDir(), ".update-"+id)
	upper, work, mnt := filepath.Join(build, "upper"), filepath.Join(build, "work"), filepath.Join(build, "mnt")
	newDir := filepath.Join(m.L.GamesDir(), ".new-"+id)
	final := filepath.Join(m.L.GamesDir(), id)
	defer func() { _ = removeTreeForce(build); _ = removeTreeForce(newDir) }()
	if err := m.mkdirs(upper, work, mnt); err != nil {
		return "", err
	}

	before := gameBuild(src)
	cmdline, err := steamcmdOverlayCmdline(steamcmd, src, upper, work, mnt, SteamcmdShouldValidate())
	if err != nil {
		return "", err
	}
	fmt.Fprintf(w, "[CS2 update] SteamCMD on %s (build %d) through an overlay; the running version is not written.\n", src, before)
	if err := runUserCmdline(ctx, m.L.User, cmdline, w); err != nil {
		return "", fmt.Errorf("SteamCMD: %w", err)
	}
	changed, removed, err := overlayChanges(upper)
	if err != nil {
		return "", err
	}
	if changed == 0 && removed == 0 {
		fmt.Fprintf(w, "[CS2 update] no changes: CS2 build %d is up to date.\n", before)
		return "", nil
	}

	fmt.Fprintf(w, "[CS2 update] %d changed and %d removed file(s); making game version %s...\n", changed, removed, id)
	mode, err := m.copyGameVersion(ctx, w, src, newDir)
	if err != nil {
		return "", err
	}
	created, err := applyOverlayUpper(upper, newDir)
	if err != nil {
		return "", fmt.Errorf("applying the update to %s: %w", newDir, err)
	}
	m.own(created...)
	if err := os.Rename(newDir, final); err != nil {
		return "", err
	}
	after := gameBuild(final)
	info := GameInfo{ID: id, Build: after, From: src, Copy: mode, Changed: changed, Removed: removed, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := writeJSONAtomic(final+".json", info); err != nil {
		return "", err
	}
	m.own(final + ".json")
	if err := m.setCurrentGame(id); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "[✓] CS2 game version %s (build %d -> %d, %s copy of %s) is current.\n", id, before, after, mode, filepath.Base(src))
	return final, nil
}

// newGameID is a sortable, unique game version name.
func (m *InstanceManager) newGameID(now time.Time) string {
	base := now.UTC().Format("20060102-150405")
	id := base
	for i := 2; ; i++ {
		_, e1 := os.Lstat(filepath.Join(m.L.GamesDir(), id))
		_, e2 := os.Lstat(filepath.Join(m.L.GamesDir(), ".update-"+id))
		if os.IsNotExist(e1) && os.IsNotExist(e2) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// gamesInUse are the game versions something still needs: the current one,
// every layer's, and every running instance's.
func (m *InstanceManager) gamesInUse() map[string]bool {
	used := map[string]bool{filepath.Clean(m.CurrentGame()): true}
	for _, l := range m.ListLayers() {
		used[m.layerGame(l)] = true
	}
	for _, n := range m.List() {
		if m.IsRunning(n) {
			if g := m.GameInUse(n); g != "" {
				used[g] = true
			}
		}
	}
	return used
}

// gcGames removes game versions nothing uses. The master install is never
// removed.
func (m *InstanceManager) gcGames(w io.Writer) {
	used := m.gamesInUse()
	cur := m.CurrentGame()
	for _, d := range m.ListGames() {
		// A version newer than current is one an update just made (it
		// becomes current next); never collect it.
		if used[filepath.Clean(d)] || cur == m.L.Master || filepath.Base(d) > filepath.Base(cur) {
			continue
		}
		if err := removeTreeForce(d); err != nil {
			fmt.Fprintf(w, "[CS2 update] could not remove old game version %s: %v\n", filepath.Base(d), err)
			continue
		}
		_ = os.Remove(d + ".json")
		fmt.Fprintf(w, "[CS2 update] removed unused game version %s\n", filepath.Base(d))
	}
}

// GC removes Ready Up layers and game versions nothing uses any more.
func (m *InstanceManager) GC(w io.Writer) {
	m.gcLayers(w)
	m.gcGames(w)
}

// GCNow is `csm instance gc`.
func (m *InstanceManager) GCNow(w io.Writer) error {
	if err := m.requireInstancePrivileges("instance gc"); err != nil {
		return err
	}
	m.GC(w)
	return nil
}

// GameVersionsReport is `csm instance game`.
func (m *InstanceManager) GameVersionsReport() string { return m.gameVersionsReport() }

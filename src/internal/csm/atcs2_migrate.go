package csm

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Carrying an install over to the renamed plugin.
//
// Plugin 2.0.0 renamed MatchZy to Auto Tournament CS2: the plugin folder, the
// DLL, the cfg folder, the SQLite file, and csm's own MySQL container. An
// install made before that has the old names on disk. csm moves what it owns
// the first time a 2.0.0-aware csm touches the install:
//
//   - cfg/MatchZy/ -> cfg/AutoTournamentCS2/ in the overrides/, cs2-config/
//     and game_files/ trees (carryOverLegacyCfg). An existing new-named file
//     is never overwritten; the old file is then left where it is.
//   - addons/counterstrikesharp/plugins/MatchZy/ is removed once the new
//     plugin sits next to it, so two copies never load
//     (removeLegacyATCS2Plugin).
//   - The per-server SQLite database survives the addons being replaced, and
//     matchzy.db becomes auto_tournament_cs2.db (withATCS2SQLitePreserved).
//   - The MySQL container is renamed in place; its volume keeps its name
//     (migrateLegacyATCS2Container).
//
// The plugin itself renames its tables and cfg keys on first start.

// atcs2CfgCarryOverMarker is written into a tree's root once its cfg folder
// has been carried over, so the move and its log happen once per tree.
const atcs2CfgCarryOverMarker = ".csm-atcs2-cfg-carried-over"

// atcs2RenameLog is the csm.log section the carry-over writes to.
const atcs2RenameLog = "atcs2-rename.log"

// carryOverLegacyCfg moves every regular file under cfgDir/MatchZy to the same
// relative path under cfgDir/AutoTournamentCS2. A file whose new path already
// exists is not touched and is returned in left; nothing is overwritten and
// nothing is deleted. Directories emptied by the move are removed, including
// cfgDir/MatchZy itself when nothing is left in it. A missing cfgDir/MatchZy
// is a no-op.
func carryOverLegacyCfg(cfgDir string) (moved, left []string, err error) {
	oldDir := filepath.Join(cfgDir, legacyATCS2CfgDirName)
	newDir := filepath.Join(cfgDir, ATCS2CfgDirName)
	if fi, statErr := os.Lstat(oldDir); statErr != nil || !fi.IsDir() {
		return nil, nil, nil
	}

	var dirs []string
	err = filepath.WalkDir(oldDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(oldDir, path)
		if relErr != nil {
			return relErr
		}
		dst := filepath.Join(newDir, rel)
		if _, statErr := os.Lstat(dst); statErr == nil {
			left = append(left, rel)
			return nil
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
		if err := moveFileNoClobber(path, dst); err != nil {
			return fmt.Errorf("move %s -> %s: %w", path, dst, err)
		}
		moved = append(moved, rel)
		return nil
	})

	// Remove directories the move emptied, deepest first. os.Remove refuses a
	// directory that still holds anything, so left-over files are safe.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		_ = os.Remove(d)
	}
	return moved, left, err
}

// moveFileNoClobber is defined in plugin_sqlite_keep.go and shared by both
// the SQLite-preservation and the legacy cfg/plugin carry-over below.

// carryOverLegacyCfgOnce runs carryOverLegacyCfg on cfgDir unless markerDir
// already holds atcs2CfgCarryOverMarker, logs what it did to w, and then
// writes the marker. label names the tree in the log. It returns whether it
// ran.
func carryOverLegacyCfgOnce(w io.Writer, label, markerDir, cfgDir string) (bool, error) {
	marker := filepath.Join(markerDir, atcs2CfgCarryOverMarker)
	if _, err := os.Stat(marker); err == nil {
		return false, nil
	}
	if fi, err := os.Stat(markerDir); err != nil || !fi.IsDir() {
		// The tree does not exist yet (fresh install): nothing to carry over,
		// and no marker, so a tree created later is still checked.
		return false, nil
	}

	moved, left, err := carryOverLegacyCfg(cfgDir)
	if len(moved) > 0 || len(left) > 0 || err != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "[Rename] %s: carrying over %s -> %s\n", label,
			filepath.Join(cfgDir, legacyATCS2CfgDirName), filepath.Join(cfgDir, ATCS2CfgDirName))
		for _, rel := range moved {
			fmt.Fprintf(&b, "  moved %s\n", rel)
		}
		for _, rel := range left {
			fmt.Fprintf(&b, "  left %s in place: %s already exists and was not overwritten\n",
				filepath.Join(legacyATCS2CfgDirName, rel), filepath.Join(ATCS2CfgDirName, rel))
		}
		for _, rel := range moved {
			if label == "game_files" {
				break // staging copies of the files reported for overrides/ and cs2-config/
			}
			if n := countLegacyConVarLines(filepath.Join(cfgDir, ATCS2CfgDirName, rel)); n > 0 {
				fmt.Fprintf(&b, "  [WARN] %s has %d matchzy_* setting(s). Auto Tournament CS2 2.0.0 does not read them; rename each to at_* (for example matchzy_knife_enabled_default -> at_knife_enabled_default)\n",
					filepath.Join(ATCS2CfgDirName, rel), n)
			}
		}
		if err != nil {
			fmt.Fprintf(&b, "  error: %v (will retry on the next run)\n", err)
		}
		fmt.Fprint(w, b.String())
		AppendLog(atcs2RenameLog, b.String())
	}
	if err != nil {
		return true, err
	}
	note := fmt.Sprintf("cfg/%s carried over to cfg/%s by csm on %s\n",
		legacyATCS2CfgDirName, ATCS2CfgDirName, time.Now().UTC().Format(time.RFC3339))
	if werr := os.WriteFile(marker, []byte(note), 0o644); werr != nil {
		return true, werr
	}
	return true, nil
}

// countLegacyConVarLines counts the lines in a .cfg file that set a
// pre-2.0.0 matchzy_* console variable. The renamed plugin ignores them.
func countLegacyConVarLines(path string) int {
	if !strings.HasSuffix(path, ".cfg") {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "matchzy_") {
			n++
		}
	}
	return n
}

// atcs2CfgTree is one tree whose cfg/ folder csm carries over.
type atcs2CfgTree struct {
	label     string
	markerDir string // the tree's root, outside anything csm syncs to servers
	cfgDir    string // <tree>/game/csgo/cfg
}

// atcs2CfgTrees lists csm's own copies of the plugin config: the operator's
// overrides, the shared cs2-config overlay, and the game_files staging tree.
func atcs2CfgTrees(cs2User string) []atcs2CfgTree {
	var trees []atcs2CfgTree
	if strings.TrimSpace(cs2User) != "" {
		ov := OverridesDir(cs2User)
		trees = append(trees, atcs2CfgTree{"overrides", ov, filepath.Join(ov, "game", "csgo", "cfg")})
		shared := filepath.Join(homeBaseDir, cs2User, "cs2-config")
		trees = append(trees, atcs2CfgTree{"cs2-config", shared, filepath.Join(shared, "game", "csgo", "cfg")})
	}
	gameFiles := filepath.Join(ResolveRoot(), "game_files")
	trees = append(trees, atcs2CfgTree{"game_files", gameFiles, filepath.Join(gameFiles, "game", "csgo", "cfg")})
	return trees
}

// EnsureATCS2CfgCarriedOver carries cfg/MatchZy/ over to cfg/AutoTournamentCS2/
// in every tree csm owns, once per tree. It must run before anything reads or
// writes the new cfg folder (database.json in particular), or csm would
// create a fresh default next to the operator's old file. Errors are logged
// and returned; the carry-over is retried on the next run.
func EnsureATCS2CfgCarriedOver(w io.Writer, cs2User string) error {
	var errs []error
	for _, t := range atcs2CfgTrees(cs2User) {
		ran, err := carryOverLegacyCfgOnce(w, t.label, t.markerDir, t.cfgDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.label, err))
			continue
		}
		if ran && os.Geteuid() == 0 && strings.TrimSpace(cs2User) != "" && t.label != "game_files" {
			_ = ensureTreeOwnedByUser(cs2User, filepath.Join(t.cfgDir, ATCS2CfgDirName))
			_ = ensureOwnedByUser(cs2User, filepath.Join(t.markerDir, atcs2CfgCarryOverMarker))
		}
	}
	return errors.Join(errs...)
}

// removeLegacyATCS2Plugin removes addons/counterstrikesharp/plugins/MatchZy/
// under csgoDir, so the old plugin never loads next to the new one. It only
// does so when the new plugin is present in the same tree: removing the old
// one without a replacement would leave a server with no plugin at all, so
// then it only warns. label names the tree in the log.
func removeLegacyATCS2Plugin(w io.Writer, label, csgoDir string) error {
	legacy := legacyATCS2PluginDir(csgoDir)
	if _, err := os.Lstat(legacy); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(atcs2PluginDir(csgoDir), ATCS2DLLName)); err != nil {
		fmt.Fprintf(w, "  [WARN] %s: the old plugin folder %s is still installed and %s is missing. Run: sudo csm update-plugins\n",
			label, legacy, ATCS2DLLName)
		return nil
	}
	if err := os.RemoveAll(legacy); err != nil {
		fmt.Fprintf(w, "  [ERROR] %s: could not remove the old plugin folder %s: %v\n", label, legacy, err)
		return err
	}
	msg := fmt.Sprintf("[Rename] %s: removed the old plugin folder %s (replaced by %s)\n",
		label, legacy, atcs2PluginDir(csgoDir))
	fmt.Fprint(w, "  "+msg)
	AppendLog(atcs2RenameLog, msg)
	return nil
}

// legacyATCS2SQLiteFile is the 1.x plugin's database file name. sqliteFileSuffixes
// (the database itself and its journal files) is defined in plugin_sqlite_keep.go.
const legacyATCS2SQLiteFile = "matchzy.db"

// atcs2SQLiteStashDir is where a server's plugin database waits while its
// addons are replaced. It is in the server's directory, outside addons/, so
// if csm is interrupted the files are still there on disk.
func atcs2SQLiteStashDir(serverDir string) string {
	return filepath.Join(serverDir, ".csm-plugin-db-stash")
}

// withATCS2SQLitePreserved runs replaceAddons, which replaces the server's
// addons/ tree, without losing the plugin's SQLite database stored inside it.
//
// The plugin keeps its SQLite file in its own plugin folder, which is inside
// addons/. Before replaceAddons it moves both the new
// plugins/AutoTournamentCS2/auto_tournament_cs2.db and the old
// plugins/MatchZy/matchzy.db (with their journals) into the stash; afterwards
// it moves them into plugins/AutoTournamentCS2/. The old matchzy.db is
// renamed to auto_tournament_cs2.db there, unless that file already exists,
// in which case it stays in the stash and the log says where.
func withATCS2SQLitePreserved(w io.Writer, serverDir string, replaceAddons func() error) error {
	csgoDir := filepath.Join(serverDir, "game", "csgo")
	stash := atcs2SQLiteStashDir(serverDir)
	newStash := filepath.Join(stash, ATCS2PluginDirName)
	oldStash := filepath.Join(stash, legacyATCS2PluginDirName)

	// A database still in a stash from an interrupted earlier run is older
	// than the one about to be stashed now (the plugin folder still has it,
	// or the earlier run would have restored it already). Set the leftover
	// aside under a timestamped name first, so the current database - not
	// the stale leftover - ends up under the plain name and is what gets
	// restored.
	setAsideLeftover := func(fromDir, base, toDir string) error {
		if _, err := os.Lstat(filepath.Join(fromDir, base)); err != nil {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(toDir, base)); err != nil {
			return nil
		}
		ts := time.Now().Unix()
		for _, sfx := range sqliteFileSuffixes {
			old := filepath.Join(toDir, base+sfx)
			if _, err := os.Lstat(old); err != nil {
				continue
			}
			if err := moveFileNoClobber(old, fmt.Sprintf("%s.%d", old, ts)); err != nil {
				return fmt.Errorf("could not set aside %s left over from an earlier run, so the addons were not replaced: %w", old, err)
			}
		}
		return nil
	}
	if err := setAsideLeftover(atcs2PluginDir(csgoDir), ATCS2SQLiteFile, newStash); err != nil {
		return err
	}
	if err := setAsideLeftover(legacyATCS2PluginDir(csgoDir), legacyATCS2SQLiteFile, oldStash); err != nil {
		return err
	}

	stashSet := func(fromDir, base, toDir string) error {
		for _, sfx := range sqliteFileSuffixes {
			src := filepath.Join(fromDir, base+sfx)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			dst := filepath.Join(toDir, base+sfx)
			if _, err := os.Lstat(dst); err == nil {
				// Left over from an interrupted run; keep both.
				dst = fmt.Sprintf("%s.%d", dst, time.Now().Unix())
			}
			if err := moveFileNoClobber(src, dst); err != nil {
				return fmt.Errorf("could not move %s out of addons/ before replacing it: %w", src, err)
			}
		}
		return nil
	}
	// If a file cannot be moved aside, the addons are not replaced, and
	// whatever was already moved is put back.
	if err := stashSet(atcs2PluginDir(csgoDir), ATCS2SQLiteFile, newStash); err != nil {
		return errors.Join(err, restoreATCS2SQLite(w, serverDir))
	}
	if err := stashSet(legacyATCS2PluginDir(csgoDir), legacyATCS2SQLiteFile, oldStash); err != nil {
		return errors.Join(err, restoreATCS2SQLite(w, serverDir))
	}

	runErr := replaceAddons()

	restoreErr := restoreATCS2SQLite(w, serverDir)
	return errors.Join(runErr, restoreErr)
}

// restoreATCS2SQLite moves the stashed database back into the new plugin
// folder. It never overwrites a file that is already there.
func restoreATCS2SQLite(w io.Writer, serverDir string) error {
	stash := atcs2SQLiteStashDir(serverDir)
	if fi, err := os.Stat(stash); err != nil || !fi.IsDir() {
		// Nothing to restore, or the stash path is unexpectedly not a
		// directory (e.g. a move never got to create it): leave it alone
		// rather than deleting whatever is there.
		return nil
	}
	pluginDir := atcs2PluginDir(filepath.Join(serverDir, "game", "csgo"))
	newStash := filepath.Join(stash, ATCS2PluginDirName)
	oldStash := filepath.Join(stash, legacyATCS2PluginDirName)

	var errs []error
	restoreSet := func(fromDir, fromBase, toBase string) bool {
		if _, err := os.Lstat(filepath.Join(fromDir, fromBase)); err != nil {
			return false
		}
		if _, err := os.Lstat(filepath.Join(pluginDir, toBase)); err == nil {
			return false
		}
		for _, sfx := range sqliteFileSuffixes {
			src := filepath.Join(fromDir, fromBase+sfx)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			if err := moveFileNoClobber(src, filepath.Join(pluginDir, toBase+sfx)); err != nil {
				errs = append(errs, err)
				return false
			}
		}
		return true
	}

	restoreSet(newStash, ATCS2SQLiteFile, ATCS2SQLiteFile)
	if restoreSet(oldStash, legacyATCS2SQLiteFile, ATCS2SQLiteFile) {
		msg := fmt.Sprintf("[Rename] %s: carried the plugin database over: plugins/%s/%s -> %s\n",
			filepath.Base(serverDir), legacyATCS2PluginDirName, legacyATCS2SQLiteFile,
			filepath.Join(pluginDir, ATCS2SQLiteFile))
		fmt.Fprint(w, "  "+msg)
		AppendLog(atcs2RenameLog, msg)
	}

	// Remove the stash when it is empty; anything still in it is reported.
	for _, d := range []string{newStash, oldStash, stash} {
		_ = os.Remove(d)
	}
	if entries, err := listFilesUnder(stash); err == nil && len(entries) > 0 {
		fmt.Fprintf(w, "  [WARN] %s: plugin database files were kept in %s because %s already exists: %s\n",
			filepath.Base(serverDir), stash, filepath.Join(pluginDir, ATCS2SQLiteFile), strings.Join(entries, ", "))
	}
	return errors.Join(errs...)
}

func listFilesUnder(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// legacyATCS2EnvVars maps each environment variable csm read before the
// rename to the one it reads now. The old names are not read.
var legacyATCS2EnvVars = [][2]string{
	{"MATCHZY_SKIP_DOCKER", "AT_SKIP_DOCKER"},
	{"MATCHZY_DB_ENGINE", "AT_DB_ENGINE"},
	{"MATCHZY_DB_CONTAINER", "AT_DB_CONTAINER"},
	{"MATCHZY_DB_VOLUME", "AT_DB_VOLUME"},
	{"MATCHZY_DB_IMAGE", "AT_DB_IMAGE"},
	{"MATCHZY_DB_ROOT_PASSWORD", "AT_DB_ROOT_PASSWORD"},
	{"CSM_MATCHZY_SCOPE_PREFIX", "CSM_AT_SCOPE_PREFIX"},
}

// legacyATCS2EnvProblems returns one line per old environment variable that
// is still set, naming its replacement.
func legacyATCS2EnvProblems() []string {
	var out []string
	for _, p := range legacyATCS2EnvVars {
		if _, ok := os.LookupEnv(p[0]); ok {
			out = append(out, fmt.Sprintf("%s is set but no longer read; rename it to %s", p[0], p[1]))
		}
	}
	return out
}

// dockerContainerNames lists every container, running or not.
func dockerContainerNames() (map[string]bool, error) {
	out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").Output()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if n := strings.TrimSpace(line); n != "" {
			names[n] = true
		}
	}
	return names, nil
}

// containerDataVolume is defined in bootstrap.go and shared here.

// migrateLegacyATCS2Container renames csm's plugin database container from
// LegacyATCS2ContainerName to target.
//
// `docker rename` renames the existing container in place: the same
// container, the same volume, port, environment and restart policy, and
// MySQL keeps running. There is no moment when the data sits in a stopped
// old container, or when a new container starts on an empty volume, so an
// interrupted update cannot strand the data.
//
// When both names exist csm cannot tell which one holds the data, so it
// changes nothing and returns an error that says how to resolve it.
func migrateLegacyATCS2Container(w io.Writer, target string) error {
	if target == "" || target == LegacyATCS2ContainerName {
		return nil
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return nil
	}
	names, err := dockerContainerNames()
	if err != nil {
		return fmt.Errorf("could not list Docker containers: %w", err)
	}
	if !names[LegacyATCS2ContainerName] {
		return nil
	}
	if names[target] {
		return fmt.Errorf("both Docker containers %q (volume %q) and %q (volume %q) exist, so csm renamed neither. "+
			"Keep the one that holds your database, remove the other with docker rm (not its volume), then rerun",
			LegacyATCS2ContainerName, containerDataVolume(LegacyATCS2ContainerName),
			target, containerDataVolume(target))
	}
	if out, err := exec.Command("docker", "rename", LegacyATCS2ContainerName, target).CombinedOutput(); err != nil {
		return fmt.Errorf("docker rename %s %s: %v: %s", LegacyATCS2ContainerName, target, err, strings.TrimSpace(string(out)))
	}
	vol := containerDataVolume(target)
	if vol == "" {
		vol = "(unknown)"
	}
	msg := fmt.Sprintf("[Rename] Renamed the plugin database container %s -> %s. Same container and data; volume %s keeps its name.\n",
		LegacyATCS2ContainerName, target, vol)
	fmt.Fprint(w, "  "+msg)
	AppendLog(atcs2RenameLog, msg)
	return nil
}

// atcs2DBContainerName is the container csm manages for the plugin database.
func atcs2DBContainerName() string {
	return getenvDefault("AT_DB_CONTAINER", DefaultATCS2ContainerName)
}

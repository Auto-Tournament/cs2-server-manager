package csm

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The MatchZy-based plugin (1.x) keeps its per-server SQLite database inside
// its own plugin folder: game/csgo/addons/counterstrikesharp/plugins/MatchZy/
// matchzy.db. `csm update-plugins` replaces each server's addons/ tree
// (RemoveAll + rsync --delete) and bootstrap's overlay rsyncs addons/ with
// --delete, so every update deleted each server's SQLite match stats.
//
// withPluginSQLitePreserved moves the database (and its journal files) out of
// addons/ before the tree is replaced and puts it back afterwards.

const (
	pluginSQLiteFile = "matchzy.db"
	pluginDirName    = "MatchZy"
	pluginDBLog      = "plugin-db-keep.log"
)

// sqliteFileSuffixes are the database itself and the journal files SQLite
// keeps next to it. A database and its journals are only valid together, so
// they always move as a set.
var sqliteFileSuffixes = []string{"", "-wal", "-shm", "-journal"}

// pluginDirIn returns the plugin folder under a csgo directory.
func pluginDirIn(csgoDir string) string {
	return filepath.Join(csgoDir, "addons", "counterstrikesharp", "plugins", pluginDirName)
}

// pluginSQLiteStashDir is where a server's plugin database waits while its
// addons are replaced. It is in the server's directory, outside addons/ (and
// on the same filesystem, so the move is a rename), and if csm is interrupted
// the files are still there on disk; the next run puts them back.
func pluginSQLiteStashDir(serverDir string) string {
	return filepath.Join(serverDir, ".csm-plugin-db-stash")
}

// moveFileNoClobber moves src to dst, creating dst's parent. It fails rather
// than replace an existing dst.
func moveFileNoClobber(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// A hard link is an atomic "create only if missing"; removing src
	// afterwards completes the move.
	if err := os.Link(src, dst); err == nil {
		return os.Remove(src)
	} else if errors.Is(err, fs.ErrExist) {
		return err
	}
	// Different filesystem, or links not supported: copy, then remove.
	if err := copyFileNoClobber(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

// withPluginSQLitePreserved runs replaceAddons, which replaces the server's
// addons/ tree, without losing the plugin's SQLite database stored inside it.
//
// If a file cannot be moved aside, replaceAddons is not run and whatever was
// already moved is put back, so the database is never deleted with addons/.
func withPluginSQLitePreserved(w io.Writer, serverDir string, replaceAddons func() error) error {
	pluginDir := pluginDirIn(filepath.Join(serverDir, "game", "csgo"))
	stash := pluginSQLiteStashDir(serverDir)

	// A database still in the stash from an interrupted run is older than the
	// one the server is using now. Set it aside under a timestamped name so
	// the current one is what gets put back; the older copy is kept and
	// reported.
	_, currentErr := os.Lstat(filepath.Join(pluginDir, pluginSQLiteFile))
	_, leftoverErr := os.Lstat(filepath.Join(stash, pluginSQLiteFile))
	if currentErr == nil && leftoverErr == nil {
		ts := time.Now().Unix()
		for _, sfx := range sqliteFileSuffixes {
			old := filepath.Join(stash, pluginSQLiteFile+sfx)
			if _, err := os.Lstat(old); err != nil {
				continue
			}
			if err := moveFileNoClobber(old, fmt.Sprintf("%s.%d", old, ts)); err != nil {
				return fmt.Errorf("could not set aside %s left over from an earlier run, so the addons were not replaced: %w", old, err)
			}
		}
	}

	for _, sfx := range sqliteFileSuffixes {
		src := filepath.Join(pluginDir, pluginSQLiteFile+sfx)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		dst := filepath.Join(stash, pluginSQLiteFile+sfx)
		if _, err := os.Lstat(dst); err == nil {
			// A journal left over without its database; keep both.
			dst = fmt.Sprintf("%s.%d", dst, time.Now().Unix())
		}
		if err := moveFileNoClobber(src, dst); err != nil {
			moveErr := fmt.Errorf("could not move %s out of addons/ before replacing it, so the addons were not replaced: %w", src, err)
			return errors.Join(moveErr, restorePluginSQLite(w, serverDir))
		}
	}

	runErr := replaceAddons()
	return errors.Join(runErr, restorePluginSQLite(w, serverDir))
}

// restorePluginSQLite moves a stashed database back into the plugin folder.
// It never overwrites a database that is already there: the stashed files
// then stay in the stash, and the output says where.
func restorePluginSQLite(w io.Writer, serverDir string) error {
	stash := pluginSQLiteStashDir(serverDir)
	if fi, err := os.Stat(stash); err != nil || !fi.IsDir() {
		return nil
	}
	pluginDir := pluginDirIn(filepath.Join(serverDir, "game", "csgo"))
	label := filepath.Base(serverDir)

	var errs []error
	_, stashedErr := os.Lstat(filepath.Join(stash, pluginSQLiteFile))
	_, presentErr := os.Lstat(filepath.Join(pluginDir, pluginSQLiteFile))
	if stashedErr == nil && presentErr != nil {
		for _, sfx := range sqliteFileSuffixes {
			src := filepath.Join(stash, pluginSQLiteFile+sfx)
			if _, err := os.Lstat(src); err != nil {
				continue
			}
			if err := moveFileNoClobber(src, filepath.Join(pluginDir, pluginSQLiteFile+sfx)); err != nil {
				errs = append(errs, fmt.Errorf("could not put %s back: %w", src, err))
			}
		}
	}

	// Remove the stash when it is empty; anything still in it is reported.
	_ = os.Remove(stash)
	if left, err := filesIn(stash); err == nil && len(left) > 0 {
		msg := fmt.Sprintf("%s: plugin SQLite files were kept in %s (%s); %s already exists or they could not be moved back\n",
			label, stash, strings.Join(left, ", "), filepath.Join(pluginDir, pluginSQLiteFile))
		fmt.Fprint(w, "  [WARN] "+msg)
		AppendLog(pluginDBLog, msg)
	}
	return errors.Join(errs...)
}

func filesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

// mysqlDataVolume picks the Docker volume a (re)created MySQL container
// mounts: the one the existing container already keeps /var/lib/mysql on,
// when known, rather than the configured name. Recreating the container (for
// a port change) on a different volume would bring the database back empty,
// e.g. when MATCHZY_DB_VOLUME was set at install time but not later.
func mysqlDataVolume(configured, mounted string) string {
	if strings.TrimSpace(mounted) != "" {
		return strings.TrimSpace(mounted)
	}
	return configured
}

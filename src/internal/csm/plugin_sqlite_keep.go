package csm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The plugin (both the pre-2.0.0 MatchZy name and the renamed Auto Tournament
// CS2) keeps its per-server SQLite database inside its own plugin folder
// under addons/. `csm update-plugins` replaces each server's addons/ tree
// (RemoveAll + rsync --delete) and bootstrap's overlay rsyncs addons/ with
// --delete, so every update would delete each server's SQLite match stats.
//
// moveFileNoClobber and sqliteFileSuffixes below are the shared building
// blocks; withATCS2SQLitePreserved (atcs2_migrate.go) is what actually moves
// the database aside before addons/ is replaced and puts it back afterwards,
// carrying an old MatchZy-named database over to the new name in the
// process.

// sqliteFileSuffixes are the database itself and the journal files SQLite
// keeps next to it. A database and its journals are only valid together, so
// they always move as a set.
var sqliteFileSuffixes = []string{"", "-wal", "-shm", "-journal"}

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

// mysqlDataVolume picks the Docker volume a (re)created MySQL container
// mounts: the one the existing container already keeps /var/lib/mysql on,
// when known, rather than the configured name. Recreating the container (for
// a port change) on a different volume would bring the database back empty,
// e.g. when AT_DB_VOLUME was set at install time but not later.
func mysqlDataVolume(configured, mounted string) string {
	if strings.TrimSpace(mounted) != "" {
		return strings.TrimSpace(mounted)
	}
	return configured
}

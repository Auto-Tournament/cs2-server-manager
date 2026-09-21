package csm

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The overrides folder holds the user's own config files. `csm update-plugins`
// and bootstrap copy it over the plugin defaults, so edits there survive
// updates.
//
// The canonical location is /home/<cs2 user>/overrides (game files live under
// its game/ subfolder). Older TUI builds wrote Config tab edits to
// <csm root>/overrides instead (usually /opt/cs2-server-manager/overrides),
// which nothing else reads. MigrateLegacyOverrides copies those files over.

// homeBaseDir is the parent of the CS2 user's home directory. It is a variable
// so tests can point it at a temp dir.
var homeBaseDir = "/home"

// OverridesDir returns the canonical overrides folder for cs2User:
// /home/<cs2User>/overrides. When the CS2 user is unknown it falls back to
// <csm root>/overrides so dev checkouts without a CS2 user still work.
func OverridesDir(cs2User string) string {
	return overridesDirFor(homeBaseDir, ResolveRoot(), cs2User)
}

// OverridesGameDir returns the game/ subfolder of OverridesDir, which mirrors
// a server's game/ tree.
func OverridesGameDir(cs2User string) string {
	return filepath.Join(OverridesDir(cs2User), "game")
}

// LegacyOverridesDir returns the folder older TUI builds wrote config edits to.
func LegacyOverridesDir() string {
	return filepath.Join(ResolveRoot(), "overrides")
}

func overridesDirFor(homeBase, root, cs2User string) string {
	cs2User = strings.TrimSpace(cs2User)
	if cs2User == "" {
		return filepath.Join(root, "overrides")
	}
	return filepath.Join(homeBase, cs2User, "overrides")
}

// MigrateLegacyOverrides copies every regular file under oldDir to the same
// relative path under newDir when newDir does not have that file yet. Files
// already in newDir are never overwritten, and nothing in oldDir is deleted.
// It returns the relative paths it copied. A missing oldDir is a no-op.
func MigrateLegacyOverrides(oldDir, newDir string) ([]string, error) {
	if oldDir == "" || newDir == "" {
		return nil, nil
	}
	oldAbs, err1 := filepath.Abs(oldDir)
	newAbs, err2 := filepath.Abs(newDir)
	if err1 == nil && err2 == nil && oldAbs == newAbs {
		return nil, nil
	}
	if fi, err := os.Stat(oldDir); err != nil || !fi.IsDir() {
		return nil, nil
	}

	var migrated []string
	err := filepath.WalkDir(oldDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(oldDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(newDir, rel)
		if _, err := os.Lstat(dst); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := copyFileNoClobber(path, dst); err != nil {
			return fmt.Errorf("copy %s -> %s: %w", path, dst, err)
		}
		migrated = append(migrated, rel)
		return nil
	})
	return migrated, err
}

func copyFileNoClobber(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var overridesMigrationOnce sync.Once

// EnsureOverridesMigrated runs MigrateLegacyOverrides from LegacyOverridesDir
// to OverridesDir(cs2User) once per process, fixes ownership of the new folder
// when running as root, and logs what it copied to overrides-migration.log.
// It returns the relative paths copied (empty on every call after the first).
func EnsureOverridesMigrated(cs2User string) []string {
	if strings.TrimSpace(cs2User) == "" {
		return nil
	}
	var migrated []string
	overridesMigrationOnce.Do(func() {
		oldDir, newDir := LegacyOverridesDir(), OverridesDir(cs2User)
		var err error
		migrated, err = MigrateLegacyOverrides(oldDir, newDir)
		if len(migrated) == 0 && err == nil {
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Migrating legacy overrides %s -> %s\n", oldDir, newDir)
		for _, rel := range migrated {
			fmt.Fprintf(&b, "  copied %s\n", rel)
		}
		if err != nil {
			fmt.Fprintf(&b, "  error: %v\n", err)
		}
		AppendLog("overrides-migration.log", b.String())
		if len(migrated) > 0 {
			_ = ensureTreeOwnedByUser(cs2User, newDir)
		}
	})
	return migrated
}

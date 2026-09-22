package csm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrInsufficientDiskSpace marks a disk-space pre-check that ran and found
// too little free space. Other pre-check failures (a path that cannot be
// inspected at all) are reported without it, so callers and users can tell
// "the disk is full" apart from "the check could not run".
var ErrInsufficientDiskSpace = errors.New("insufficient disk space")

// nearestExistingDir returns path itself, or its closest ancestor, that exists
// and is a directory. On a fresh install the target directory (for example
// /opt/cs2-server-manager/game_files/game or /home/<user>) often does not
// exist yet; the filesystem it will live on is the one holding that ancestor.
func nearestExistingDir(path string) (string, error) {
	p := filepath.Clean(path)
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("no existing directory found at or above %s", path)
		}
		p = parent
	}
}

// freeDiskGB returns the free space, in GB, on the filesystem that holds (or
// will hold) path. The path does not need to exist yet. It also returns the
// directory it actually inspected.
func freeDiskGB(path string) (freeGB float64, checked string, err error) {
	checked, err = nearestExistingDir(path)
	if err != nil {
		return 0, "", err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(checked, &stat); err != nil {
		return 0, checked, fmt.Errorf("could not read filesystem stats for %s: %w", checked, err)
	}
	return (float64(stat.Bavail) * float64(stat.Bsize)) / (1024 * 1024 * 1024), checked, nil
}

// requireFreeDiskGB checks that the filesystem holding path has at least
// requiredGB free. Only a real shortage wraps ErrInsufficientDiskSpace.
func requireFreeDiskGB(path string, requiredGB float64) error {
	freeGB, checked, err := freeDiskGB(path)
	if err != nil {
		return fmt.Errorf("disk space check could not run for %s: %w", path, err)
	}
	if freeGB < requiredGB {
		return fmt.Errorf("%w at %s: %.2f GB free, need at least %.2f GB", ErrInsufficientDiskSpace, checked, freeGB, requiredGB)
	}
	return nil
}
